#!/usr/bin/env node
/**
 * Layout's load-bearing claims, through the workflow that broke them.
 *
 * Assembling a diagram — add nodes from the palette, wire them, save, drag
 * one, add another, reload — and asserting four things at every step:
 *
 *   1. **Nothing overlaps.** Two boxes sharing space is the report this check
 *      exists for. It appeared only after a reload, because that is when a
 *      backend-assigned `unplaced` coordinate first reaches the renderer.
 *   2. **An `unplaced` coordinate is not a placement.** The backend has to put
 *      a new node somewhere and says so with the flag; with no placed
 *      neighbour that somewhere is the origin. Honouring it drops the node on
 *      whatever is already there.
 *   3. **Unwired nodes are not a column.** Three boxes with no edges between
 *      them should read as a block, not a vertical stack.
 *   4. **Dragging one node moves only that node.** The fix for (1) must not
 *      pick up every unplaced box and restack it beside the one that moved.
 */

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const API = process.env.ZP_API ?? 'http://localhost:8090'
const PASSWORD = process.env.ZP_PASSWORD ?? 'zetta-dev-2026'
const [PROJECT] = process.argv.slice(2)
if (!PROJECT) {
  console.error('usage: node scripts/check-layout.mjs <project-id>')
  process.exit(2)
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

const login = await fetch(`${API}/api/v1/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: 'mgr@zetta.test', password: PASSWORD }),
})
const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ')
const api = async (method, p, body) => {
  const r = await fetch(`${API}${p}`, {
    method,
    headers: { cookie, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    ...(body ? { body: JSON.stringify(body) } : {}),
  })
  return { status: r.status, json: await r.json().catch(() => null) }
}

// A diagram of its own, so the run is repeatable.
const name = `Layout repro ${Date.now().toString(36)}`
const created = await api('POST', `/api/v1/projects/${PROJECT}/diagrams`, {
  name,
  level: 'container',
})
const diagramId = created.json.id
process.stderr.write(`check-layout: ${diagramId}\n`)

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const context = await browser.createBrowserContext()
const page = await context.newPage()
await page.setViewport({ width: 1700, height: 1000, deviceScaleFactor: 1 })
await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))
await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
await page.waitForSelector('input[type=email]')
await page.type('input[type=email]', 'mgr@zetta.test')
await page.type('input[type=password]', PASSWORD)
await page.click('button[type=submit]')
await page.waitForFunction(() => !document.querySelector('input[type=password]'), { timeout: 15_000 })

/** Every layout PUT the editor makes. */
const layoutPuts = []
await page.setRequestInterception(true)
page.on('request', (req) => {
  if (req.url().includes('/layout') && req.method() === 'PUT' && req.postData()) {
    layoutPuts.push(JSON.parse(req.postData()))
  }
  void req.continue()
})

await page.goto(`${BASE}/p/${PROJECT}/diagrams/${diagramId}/edit`, { waitUntil: 'networkidle2' })
await wait(2000)

/** Flow-space position of every rendered node, read off the DOM transform. */
const positions = () =>
  page.evaluate(() => {
    const parse = (t) => {
      const m = /matrix\(([^)]+)\)/.exec(t)
      if (!m) return null
      const p = m[1].split(',').map(Number)
      return { x: p[4], y: p[5] }
    }
    const vp = document.querySelector('.react-flow__viewport')
    return {
      zoom: parse(getComputedStyle(vp).transform) ? Number(/matrix\(([^,]+)/.exec(getComputedStyle(vp).transform)[1]) : null,
      nodes: [...document.querySelectorAll('.react-flow__nodes > .react-flow__node')].map((n) => ({
        id: n.getAttribute('data-id'),
        ...parse(getComputedStyle(n).transform),
        w: Math.round(n.getBoundingClientRect().width),
        h: Math.round(n.getBoundingClientRect().height),
      })),
    }
  })

/** Adds one node through the palette, exactly as a person would. */
async function addNode(kindLabel, nodeName) {
  await page.evaluate((label) => {
    const b = [...document.querySelectorAll('button')].find(
      (x) => x.textContent?.trim() === label,
    )
    b?.click()
  }, kindLabel)
  await wait(150)
  await page.evaluate((n) => {
    const input = document.querySelector('input[placeholder$="name"]')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
    setter.call(input, n)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }, nodeName)
  await wait(150)
  await page.evaluate(() => {
    const b = [...document.querySelectorAll('button')].find((x) =>
      x.textContent?.includes('Add '),
    )
    b?.click()
  })
  await wait(400)
}

await addNode('Actor', 'Human')
await addNode('System', 'MimitaAgent')
await addNode('Job', 'Execution')
await wait(600)

const afterAdd = await positions()

// Wire them, through the real connect gesture: drag from one node's source
// handle to the next node's target handle.
async function connect(fromId, toId, relationLabel) {
  const handles = await page.evaluate(
    (a, b) => {
      const src = document.querySelector(
        `.react-flow__node[data-id="${a}"] .react-flow__handle.source`,
      )
      const tgt = document.querySelector(
        `.react-flow__node[data-id="${b}"] .react-flow__handle.target`,
      )
      if (!src || !tgt) return null
      const s = src.getBoundingClientRect()
      const t = tgt.getBoundingClientRect()
      return {
        from: [s.left + s.width / 2, s.top + s.height / 2],
        to: [t.left + t.width / 2, t.top + t.height / 2],
      }
    },
    fromId,
    toId,
  )
  if (!handles) return false
  await page.mouse.move(handles.from[0], handles.from[1])
  await page.mouse.down()
  for (let i = 1; i <= 12; i++) {
    await page.mouse.move(
      handles.from[0] + ((handles.to[0] - handles.from[0]) * i) / 12,
      handles.from[1] + ((handles.to[1] - handles.from[1]) * i) / 12,
    )
    await wait(16)
  }
  await page.mouse.up()
  await wait(600)
  // The relation picker opens; choose one.
  await page.evaluate((label) => {
    const b = [...document.querySelectorAll('button')].find((x) =>
      x.textContent?.trim().startsWith(label),
    )
    b?.click()
  }, relationLabel)
  await wait(500)
  return true
}

const wired1 = await connect('human', 'mimitaagent', 'calls')
const wired2 = await connect('mimitaagent', 'execution', 'publishes to')

const afterWire = await positions()
await page.screenshot({ path: path.join(outDir, 'layout-before-save.png') })

// Save, then reload the editor from scratch — the state the report describes.
await page.evaluate(() => {
  const b = [...document.querySelectorAll('button')].find((x) => x.textContent?.trim() === 'Save')
  b?.click()
})
await wait(3000)
const afterSave = await positions()

// The half the pure-auto path never reaches: drag one node by hand, which
// flips the diagram to `manual` and gives that one node a stored coordinate
// while every other node keeps a computed one. Then add another node, which is
// what "completing the assembly" actually looks like.
const target = await page.evaluate(() => {
  const el = document.querySelector('.react-flow__node[data-id="mimitaagent"]')
  const r = el.getBoundingClientRect()
  return { x: r.left + r.width / 2, y: r.top + r.height / 2 }
})
await page.mouse.move(target.x, target.y)
await page.mouse.down()
for (let i = 1; i <= 12; i++) {
  await page.mouse.move(target.x + i * 6, target.y + i * 9)
  await wait(16)
}
await page.mouse.up()
await wait(2500)
const afterDrag = await positions()

await addNode('Datastore', 'Postgres')
await wait(800)
const afterLateAdd = await positions()
await page.screenshot({ path: path.join(outDir, 'layout-after-late-add.png') })

await page.evaluate(() => {
  const b = [...document.querySelectorAll('button')].find((x) => x.textContent?.trim() === 'Save')
  b?.click()
})
await wait(3000)

await page.goto(`${BASE}/p/${PROJECT}/diagrams/${diagramId}/edit`, { waitUntil: 'networkidle2' })
await wait(2500)
const afterReload = await positions()
await page.screenshot({ path: path.join(outDir, 'layout-after-reload.png') })

const stored = await api('GET', `/api/v1/diagrams/${diagramId}`)
const storedLayout = (stored.json.nodes ?? []).map((n) => ({
  slug: n.slug,
  layout: n.layout ?? null,
}))

await context.close()
await browser.close()

/** Do any two boxes share space? */
function overlaps(nodes) {
  const hits = []
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const a = nodes[i]
      const b = nodes[j]
      if (a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h) {
        hits.push(`${a.id} ∩ ${b.id}`)
      }
    }
  }
  return hits
}

const report = {
  diagramId,
  wired: [wired1, wired2],
  afterAdd,
  afterWire,
  afterSave,
  afterDrag,
  afterLateAdd,
  afterReload,
  storedLayout,
  layoutPuts,
  overlaps: {
    afterAdd: overlaps(afterAdd.nodes),
    afterWire: overlaps(afterWire.nodes),
    afterSave: overlaps(afterSave.nodes),
    afterDrag: overlaps(afterDrag.nodes),
    afterLateAdd: overlaps(afterLateAdd.nodes),
    afterReload: overlaps(afterReload.nodes),
  },
}
writeFileSync(path.join(outDir, 'layout.json'), JSON.stringify(report, null, 2))

const problems = []

// 1. Nothing overlaps, at any point.
for (const [stage, hits] of Object.entries(report.overlaps)) {
  if (hits.length > 0) problems.push(`${stage}: boxes share space — ${hits.join(', ')}`)
}

// 2. A node the backend flagged `unplaced` is not rendered at that coordinate
//    when doing so would collide. The flag means "I had to put it somewhere".
for (const { slug, layout } of storedLayout) {
  if (!layout?.unplaced) continue
  const rendered = afterReload.nodes.find((n) => n.id === slug)
  if (rendered && rendered.x === layout.x && rendered.y === layout.y) {
    const others = afterReload.nodes.filter((n) => n.id !== slug)
    if (overlaps([rendered, ...others]).some((h) => h.includes(slug))) {
      problems.push(`${slug}: an unplaced fallback coordinate was honoured into a collision`)
    }
  }
}

// 3. A drag sticks. The whole point of manual layout: a box stays where it was
//    put, across the save and a full reload.
const draggedBefore = afterSave.nodes.find((n) => n.id === 'mimitaagent')
const draggedAfter = afterDrag.nodes.find((n) => n.id === 'mimitaagent')
const draggedReloaded = afterReload.nodes.find((n) => n.id === 'mimitaagent')
if (draggedBefore && draggedAfter) {
  const moved = Math.hypot(draggedAfter.x - draggedBefore.x, draggedAfter.y - draggedBefore.y)
  if (moved < 20) problems.push(`the drag moved the node only ${Math.round(moved)}px — it did not take`)
}
if (draggedAfter && draggedReloaded) {
  const drift = Math.hypot(draggedReloaded.x - draggedAfter.x, draggedReloaded.y - draggedAfter.y)
  if (drift > 2) {
    problems.push(
      `the dragged node did not stay put: dropped at (${Math.round(draggedAfter.x)},${Math.round(draggedAfter.y)}), ` +
        `reloaded at (${Math.round(draggedReloaded.x)},${Math.round(draggedReloaded.y)})`,
    )
  }
}

// 4. Unwired nodes read as a block, not a column.
const addedXs = new Set(afterAdd.nodes.map((n) => Math.round(n.x)))
if (afterAdd.nodes.length > 2 && addedXs.size < 2) {
  problems.push(`unwired nodes stacked in one column at x=${[...addedXs][0]}`)
}

// 5. Dragging one node leaves the others where they were.
const draggedId = 'mimitaagent'
for (const before of afterLateAdd.nodes) {
  if (before.id === draggedId) continue
  const after = afterReload.nodes.find((n) => n.id === before.id)
  if (!after) continue
  if (Math.abs(after.x - before.x) > 1 || Math.abs(after.y - before.y) > 1) {
    problems.push(
      `${before.id} moved when only ${draggedId} was dragged: ` +
        `(${Math.round(before.x)},${Math.round(before.y)}) → (${Math.round(after.x)},${Math.round(after.y)})`,
    )
  }
}

const show = (label, snap) =>
  console.log(
    `${label.padEnd(13)} zoom ${String(snap.zoom).padEnd(6)} ` +
      snap.nodes.map((n) => `${n.id}(${Math.round(n.x)},${Math.round(n.y)})`).join('  '),
  )

show('after add', afterAdd)
show('after wire', afterWire)
show('after save', afterSave)
show('after drag', afterDrag)
show('after +node', afterLateAdd)
show('after reload', afterReload)
console.log('\nlayout PUTs:', layoutPuts.length ? JSON.stringify(layoutPuts) : 'none')
console.log('stored:     ', JSON.stringify(storedLayout))
console.log(
  `\ndrag stuck:   dropped (${Math.round(draggedAfter?.x ?? 0)},${Math.round(draggedAfter?.y ?? 0)}) → ` +
    `reloaded (${Math.round(draggedReloaded?.x ?? 0)},${Math.round(draggedReloaded?.y ?? 0)})`,
)
console.log('\noverlaps:')
for (const [k, v] of Object.entries(report.overlaps)) {
  console.log(`  ${k.padEnd(12)} ${v.length ? v.join(', ') : 'none'}`)
}

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
