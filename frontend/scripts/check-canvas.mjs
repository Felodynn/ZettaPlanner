#!/usr/bin/env node
/**
 * The canvas claims that a screenshot cannot settle (F3).
 *
 *   1. **Determinism.** `auto` mode stores nothing and dagre runs on render,
 *      so the same graph must produce the same picture. Loaded twice, every
 *      node must land on the same coordinate — otherwise a diagram rearranges
 *      itself between visits and nobody can point at "the box on the left".
 *
 *   2. **Distinguishability.** All 11 kinds must differ by more than colour.
 *      Measured as the set of (border-radius, border-style, border-width,
 *      clip-path) signatures actually computed by the browser: if two kinds
 *      share a signature they are the same silhouette, whatever their hue.
 *
 *   3. **Pan and zoom at 100 nodes.** Measured as frame time during a scripted
 *      pan, not as a vibe.
 */

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const [PROJECT, DIAGRAM] = process.argv.slice(2)
if (!PROJECT || !DIAGRAM) {
  console.error('usage: node scripts/check-canvas.mjs <project-id> <diagram-id>')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1600, height: 1000, deviceScaleFactor: 1 })
await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
await page.waitForSelector('input[type=email]')
await page.type('input[type=email]', 'dev@zetta.test')
await page.type('input[type=password]', 'zetta-dev-2026')
await page.click('button[type=submit]')
await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
  timeout: 15_000,
})

const url = `${BASE}/p/${PROJECT}/diagrams/${DIAGRAM}`

/** Every node's transform, keyed by React Flow node id. */
async function positions() {
  await page.goto(url, { waitUntil: 'networkidle2' })
  await page.waitForSelector('.react-flow__node', { timeout: 20_000 })
  await wait(1200)
  return page.evaluate(() =>
    Object.fromEntries(
      [...document.querySelectorAll('.react-flow__node')].map((el) => [
        el.getAttribute('data-id'),
        getComputedStyle(el).transform,
      ]),
    ),
  )
}

const first = await positions()
const second = await positions()

const moved = Object.keys(first).filter((id) => first[id] !== second[id])

// ── silhouettes ─────────────────────────────────────────────────────────────
const shapes = await page.evaluate(() => {
  const out = {}
  for (const body of document.querySelectorAll('[data-kind]')) {
    const s = getComputedStyle(body)
    const kind = body.getAttribute('data-kind')
    if (!kind) continue
    const signature = [
      s.borderRadius,
      s.borderStyle,
      s.borderWidth,
      s.clipPath === 'none' ? '' : s.clipPath,
    ].join(' | ')
    out[kind] = signature
  }
  return out
})

const signatures = new Map()
for (const [kind, sig] of Object.entries(shapes)) {
  signatures.set(sig, [...(signatures.get(sig) ?? []), kind])
}
const collisions = [...signatures.values()].filter((kinds) => kinds.length > 1)

// ── pan performance ─────────────────────────────────────────────────────────
const perf = await page.evaluate(async () => {
  const pane = document.querySelector('.react-flow__pane')
  if (!pane) return { frames: 0, worst: 0, mean: 0 }

  const times = []
  let last = performance.now()
  let running = true
  const tick = () => {
    const now = performance.now()
    times.push(now - last)
    last = now
    if (running) requestAnimationFrame(tick)
  }
  requestAnimationFrame(tick)

  const rect = pane.getBoundingClientRect()
  const cx = rect.left + rect.width / 2
  const cy = rect.top + rect.height / 2
  const fire = (type, x, y, buttons = 1) =>
    pane.dispatchEvent(
      new PointerEvent(type, { clientX: x, clientY: y, buttons, bubbles: true, pointerId: 1 }),
    )

  fire('pointerdown', cx, cy)
  for (let i = 0; i < 60; i++) {
    fire('pointermove', cx + Math.sin(i / 6) * 180, cy + Math.cos(i / 6) * 120)
    await new Promise((r) => requestAnimationFrame(r))
  }
  fire('pointerup', cx, cy, 0)
  running = false

  const measured = times.slice(3)
  return {
    frames: measured.length,
    worst: Math.round(Math.max(...measured) * 10) / 10,
    mean: Math.round((measured.reduce((a, b) => a + b, 0) / measured.length) * 10) / 10,
  }
})

const domNodes = await page.evaluate(() => document.querySelectorAll('.react-flow__node').length)

await page.screenshot({ path: path.join(outDir, 'canvas-check.png') })
await browser.close()

const report = { domNodes, moved, kinds: shapes, collisions, perf }
writeFileSync(path.join(outDir, 'canvas.json'), JSON.stringify(report, null, 2))

console.log(`nodes rendered:     ${domNodes}`)
console.log(`moved on reload:    ${moved.length}`)
console.log(`distinct kinds:     ${Object.keys(shapes).length}`)
console.log(`shape collisions:   ${collisions.length}`)
for (const c of collisions) console.log(`  same silhouette: ${c.join(', ')}`)
console.log(`pan frames:         ${perf.frames}, mean ${perf.mean}ms, worst ${perf.worst}ms`)

const problems = []
if (moved.length) problems.push(`${moved.length} node(s) moved between identical loads — layout is not deterministic`)
if (collisions.length) problems.push(`${collisions.length} pair(s) of kinds share a silhouette`)
// requestAnimationFrame caps at ~16.7ms, so that number *is* 60fps.
// Anything past 20ms has started dropping frames.
if (perf.mean > 20) problems.push(`mean frame ${perf.mean}ms — dropping frames while panning`)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
