#!/usr/bin/env node
/**
 * Proves the error surface renders the backend's denial verbatim (F0).
 *
 * Signs in as a developer, provokes a genuine `field_not_owned` from the
 * running backend, and compares the sentence the API sent with the sentence
 * on screen. The comparison is the point: a toast test that builds its own
 * envelope proves only that the test can build an envelope, and the failure
 * this guards against — a UI that helpfully replaces the message with
 * "Permission denied" — would pass that test happily.
 */

import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const PROJECT = process.argv[2]
if (!PROJECT) {
  console.error('usage: node scripts/check-denial.mjs <project-id>')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1280, height: 800, deviceScaleFactor: 2 })
await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))

await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
await page.waitForSelector('input[type=email]')
await page.type('input[type=email]', 'dev@zetta.test')
await page.type('input[type=password]', 'zetta-dev-2026')
await page.click('button[type=submit]')
await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
  timeout: 10_000,
})
await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle2' })
await page.waitForFunction(() => '__zp' in window, { timeout: 10_000 })

// A developer renaming a project: manager-owned, so the backend refuses with a
// sentence naming the owning role and the route forward.
const sent = await page.evaluate(async (pid) => {
  const zp = window.__zp
  try {
    await zp.client.PATCH('/api/v1/projects/{pid}', {
      params: { path: { pid } },
      body: { name: 'a developer should not be able to do this' },
    })
    return { code: '(no error)', message: '', requestId: '' }
  } catch (e) {
    zp.notifyError(e)
    return { code: e.code, message: e.message, requestId: e.requestId }
  }
}, PROJECT)

await new Promise((r) => setTimeout(r, 500))

const shown = await page.evaluate(() => {
  const alert = document.querySelector('[role=alert]')
  return alert ? alert.innerText : ''
})

await page.screenshot({ path: path.join(outDir, 'denial.png') })
await browser.close()

console.log(`code:        ${sent.code}`)
console.log(`API said:    ${sent.message}`)
console.log(`screen says: ${shown.replace(/\n/g, ' | ')}`)

const failures = []
if (sent.code !== 'field_not_owned') failures.push(`expected field_not_owned, got ${sent.code}`)
if (!shown.includes(sent.message)) failures.push('the toast does not contain the backend sentence')
if (!sent.requestId || !shown.includes(sent.requestId)) {
  failures.push('the request_id is not on the error surface')
}
if (!/manager/.test(sent.message)) failures.push('the denial does not name the owning role')

if (failures.length) {
  console.error('\nFAIL')
  for (const f of failures) console.error(`  ${f}`)
  process.exit(1)
}
console.log('\nOK — denial rendered verbatim, with the owning role and the request id')
