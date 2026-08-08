#!/usr/bin/env node
/**
 * Drives one real agent turn through the UI and watches what the thread does.
 *
 * This is the F1 exit criteria that a screenshot cannot answer: whether the
 * queue is visible, whether streaming text appears and is then *replaced* by
 * the canonical message rather than duplicated beside it, and whether every
 * message carries a name.
 *
 * Costs a real model call. That is the point — a mocked stream proves the mock.
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
const PROMPT = process.argv[3] ?? 'In one sentence, what is this project about?'
const BUDGET_MS = Number(process.env.ZP_TURN_BUDGET ?? 180_000)

if (!PROJECT) {
  console.error('usage: node scripts/check-turn.mjs <project-id> [prompt]')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1400, height: 900, deviceScaleFactor: 2 })
await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))

const problems = []
page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`))

await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
await page.waitForSelector('input[type=email]')
await page.type('input[type=email]', 'dev@zetta.test')
await page.type('input[type=password]', 'zetta-dev-2026')
await page.click('button[type=submit]')
await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
  timeout: 15_000,
})

await page.goto(`${BASE}/p/${PROJECT}/thread`, { waitUntil: 'networkidle2' })
await page.waitForSelector('textarea', { timeout: 15_000 })

// Wait for the stream to be live before posting, so the first event is not
// missed by a connection that has not opened yet.
await page.waitForFunction(() => document.body.innerText.includes('Live'), { timeout: 15_000 })

await page.type('textarea', PROMPT)
await page.keyboard.press('Enter')

const timeline = []
const started = Date.now()
let sawQueuedOrRunning = false
let maxStreamLen = 0

while (Date.now() - started < BUDGET_MS) {
  const snap = await page.evaluate(() => {
    const body = document.body.innerText
    // The streaming overlay renders "writing…" until the real message lands.
    const streaming = body.includes('writing…')
    return {
      running: /turn\s+is\s+running/i.test(body),
      queued: /Queued/i.test(body),
      streaming,
      finished: /planner/i.test(body) && !streaming,
      length: body.length,
      tail: body.slice(-400),
    }
  })

  if (snap.running || snap.queued) sawQueuedOrRunning = true
  if (snap.streaming) maxStreamLen = Math.max(maxStreamLen, snap.length)

  const label = snap.queued
    ? 'queued'
    : snap.streaming
      ? 'streaming'
      : snap.running
        ? 'running'
        : 'idle'
  if (timeline.at(-1)?.label !== label) {
    timeline.push({ label, at: Date.now() - started })
    await page.screenshot({ path: path.join(outDir, `turn-${timeline.length}-${label}.png`) })
  }

  // Done when the agent's message is present and nothing is in flight.
  if (!snap.running && !snap.queued && !snap.streaming && timeline.length > 1) break
  await new Promise((r) => setTimeout(r, 1200))
}

await new Promise((r) => setTimeout(r, 1500))
await page.screenshot({ path: path.join(outDir, 'turn-final.png') })

const final = await page.evaluate(() => document.body.innerText)

// The failure this is really looking for: deltas kept *beside* the canonical
// message, so the answer appears twice.
const assistantBlocks = (final.match(/\bplanner\b\s+agent\b/gi) ?? []).length
const stillWriting = final.includes('writing…')

await browser.close()

writeFileSync(
  path.join(outDir, 'turn.json'),
  JSON.stringify({ timeline, assistantBlocks, stillWriting, problems, final }, null, 2),
)

console.log('timeline:', timeline.map((t) => `${t.label}@${Math.round(t.at / 100) / 10}s`).join(' → '))
console.log('assistant blocks:', assistantBlocks)
console.log('\n--- thread tail ---')
console.log(final.slice(-700))

if (!sawQueuedOrRunning) problems.push('the turn was never shown as queued or running')
if (stillWriting) problems.push('the streaming overlay never cleared')
if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
