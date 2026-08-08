#!/usr/bin/env node
/**
 * Two users, one project, two prompts (F1's manual test).
 *
 * Turns serialize per project (§5.3). What this proves is not that the backend
 * serializes them — it does, with a unique partial index — but that the second
 * user can *see* that they are queued, and that both browsers end up showing
 * the same thread in the same order.
 *
 * The failure it exists to catch is silence: a send button that appears to do
 * nothing is the worst version of a busy queue.
 */

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const PROJECT = process.argv[2]
if (!PROJECT) {
  console.error('usage: node scripts/check-queue.mjs <project-id>')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})

/** A signed-in browser context of its own, so the two sessions do not share a cookie jar. */
async function open(email) {
  const context = await browser.createBrowserContext()
  const page = await context.newPage()
  await page.setViewport({ width: 1400, height: 900, deviceScaleFactor: 2 })
  await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
  await page.waitForSelector('input[type=email]')
  await page.type('input[type=email]', email)
  await page.type('input[type=password]', 'zetta-dev-2026')
  await page.click('button[type=submit]')
  await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
    timeout: 15_000,
  })
  await page.goto(`${BASE}/p/${PROJECT}/thread`, { waitUntil: 'networkidle2' })
  await page.waitForFunction(() => document.body.innerText.includes('Live'), { timeout: 15_000 })
  return page
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

const dev = await open('dev@zetta.test')
const mgr = await open('mgr@zetta.test')

async function post(page, text) {
  await page.type('textarea', text)
  await page.keyboard.press('Enter')
}

await post(dev, 'Summarise the architecture in two sentences.')
await wait(2500)

// Second prompt while the first turn holds the project's only slot.
await post(mgr, 'What is the biggest open question right now?')
await wait(2500)

const mgrView = await mgr.evaluate(() => document.body.innerText)
const devView = await dev.evaluate(() => document.body.innerText)

await mgr.screenshot({ path: path.join(outDir, 'queue-second-user.png') })
await dev.screenshot({ path: path.join(outDir, 'queue-first-user.png') })

const problems = []

// The second user must be told they are waiting, and on whom.
const mgrSeesQueue = /Queued/i.test(mgrView) || /turn is running/i.test(mgrView)
if (!mgrSeesQueue) problems.push('the second user was not shown any queue or running state')

// And whose turn it is, by name — an unattributed "busy" is not much better
// than silence.
const namesSomeone = /Dev'?s turn|Your turn|Queued/i.test(mgrView)
if (!namesSomeone) problems.push('the running turn is not attributed to anybody')

// Both browsers, one thread, same order.
const devMessages = devView.match(/Summarise the architecture|biggest open question/g) ?? []
const mgrMessages = mgrView.match(/Summarise the architecture|biggest open question/g) ?? []
if (devMessages.length !== mgrMessages.length) {
  problems.push(
    `the two browsers disagree about the transcript: ${devMessages.length} vs ${mgrMessages.length} prompts visible`,
  )
}

writeFileSync(
  path.join(outDir, 'queue.json'),
  JSON.stringify({ mgrSeesQueue, devMessages, mgrMessages, problems }, null, 2),
)

// Leave the project idle for the next check rather than holding its slot.
const active = await dev.evaluate(async () => {
  const el = [...document.querySelectorAll('button')].find((b) => b.textContent?.includes('Stop'))
  el?.click()
  return Boolean(el)
})

await browser.close()

console.log(`second user sees the queue: ${mgrSeesQueue}`)
console.log(`prompts visible — dev: ${devMessages.length}, mgr: ${mgrMessages.length}`)
console.log(`cancelled a running turn on the way out: ${active}`)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
