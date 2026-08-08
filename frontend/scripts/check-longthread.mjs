#!/usr/bin/env node
/**
 * The transcript at 600 messages (F1: "scroll without jank at 500+").
 *
 * The cache is seeded directly rather than by posting six hundred messages —
 * what is under test is the list, not the API, and a check that costs an hour
 * of model time is a check nobody runs twice.
 *
 * Two things are measured, because only one of them is obvious:
 *   - how many rows are actually in the DOM (virtualization working at all)
 *   - how long a burst of scrolling takes (whether measuring rows makes it
 *     quadratic, which is the way a virtualized list usually goes wrong)
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
const COUNT = Number(process.argv[3] ?? 600)
if (!PROJECT) {
  console.error('usage: node scripts/check-longthread.mjs <project-id> [count]')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1400, height: 900, deviceScaleFactor: 1 })
await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))

await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
await page.waitForSelector('input[type=email]')
await page.type('input[type=email]', 'dev@zetta.test')
await page.type('input[type=password]', 'zetta-dev-2026')
await page.click('button[type=submit]')
await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
  timeout: 15_000,
})
await page.goto(`${BASE}/p/${PROJECT}/thread`, { waitUntil: 'networkidle2' })
await page.waitForFunction(() => '__zpQueryClient' in window, { timeout: 15_000 })
await new Promise((r) => setTimeout(r, 1200))

const seeded = await page.evaluate((count) => {
  const qc = window.__zpQueryClient
  // Find whichever message list the thread page is showing.
  const entry = qc
    .getQueryCache()
    .getAll()
    .find((q) => q.queryKey[0] === 'messages' && Array.isArray(q.state.data))
  if (!entry) return { ok: false, reason: 'no message query in the cache' }

  const now = Date.now()
  const roles = ['user', 'assistant', 'tool']
  const messages = Array.from({ length: count }, (_, i) => ({
    id: `seed-${i}`,
    seq: i + 1,
    role: roles[i % 3],
    content:
      i % 7 === 0
        ? `Message ${i}. ` + 'A longer paragraph that wraps across several lines. '.repeat(6)
        : `Message ${i}. A short line.`,
    created_at: new Date(now - (count - i) * 60_000).toISOString(),
    actor_name: i % 3 === 1 ? 'planner' : 'Dev',
    ...(i % 3 === 2 ? { tool_calls: [{ name: 'get_item', outcome: 'ok' }] } : {}),
  }))
  qc.setQueryData(entry.queryKey, messages)
  return { ok: true, key: String(entry.queryKey) }
}, COUNT)

if (!seeded.ok) {
  await browser.close()
  console.error(`could not seed: ${seeded.reason}`)
  process.exit(1)
}

await new Promise((r) => setTimeout(r, 900))

const scroller = await page.evaluateHandle(() => {
  const els = [...document.querySelectorAll('div')]
  return els.find((e) => e.scrollHeight > e.clientHeight * 3 && e.clientHeight > 300)
})

const measured = await page.evaluate((count) => {
  const scrollers = [...document.querySelectorAll('div')].filter(
    (e) => e.scrollHeight > e.clientHeight * 3 && e.clientHeight > 300,
  )
  const el = scrollers[0]
  if (!el) return { ok: false }

  // Rows carry data-index; count how many exist at once.
  const rows = () => document.querySelectorAll('[data-index]').length

  const domAtRest = rows()
  const totalHeight = el.scrollHeight

  const started = performance.now()
  const steps = 40
  let maxRows = domAtRest
  for (let i = 0; i < steps; i++) {
    el.scrollTop = (totalHeight / steps) * i
    // Force layout so the loop measures real work rather than queuing it.
    void el.offsetHeight
    maxRows = Math.max(maxRows, rows())
  }
  const elapsed = performance.now() - started

  return {
    ok: true,
    count,
    domAtRest,
    maxRows,
    totalHeight: Math.round(totalHeight),
    scrollMs: Math.round(elapsed),
    perStepMs: Math.round((elapsed / steps) * 100) / 100,
  }
}, COUNT)

await scroller.dispose()
await page.screenshot({ path: path.join(outDir, 'longthread.png') })
await browser.close()

writeFileSync(path.join(outDir, 'longthread.json'), JSON.stringify(measured, null, 2))

console.log(`messages:          ${measured.count}`)
console.log(`rows in the DOM:   ${measured.domAtRest} at rest, ${measured.maxRows} peak`)
console.log(`total height:      ${measured.totalHeight}px`)
console.log(`40 scroll steps:   ${measured.scrollMs}ms (${measured.perStepMs}ms each)`)

const problems = []
if (measured.maxRows > 120) {
  problems.push(`${measured.maxRows} rows in the DOM — the list is not virtualizing`)
}
if (measured.perStepMs > 16) {
  problems.push(`${measured.perStepMs}ms per scroll step — slower than a frame`)
}

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
