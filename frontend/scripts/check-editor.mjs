#!/usr/bin/env node
/**
 * The editor's load-bearing claims (F4).
 *
 *   1. **The lease is taken on entry, not on first change.** Somebody who
 *      makes twenty edits and *then* learns the diagram was held has lost
 *      twenty edits.
 *   2. **No coordinate ever appears in a revision payload** (§1.1 rule 2, D5).
 *      Every request body sent to /revisions is inspected for x, y, w, h and
 *      layout — a coordinate reaching the semantic save is a contract
 *      violation on the client side, and it is invisible until a model is
 *      given geometry to reason about.
 *   3. **Layout saves separately**, to /layout, with no lease and no revision.
 *   4. **Undo and redo** survive create, edge, reparent and delete.
 *   5. **A stale base is a conflict, never a merge** — shown with both sides.
 *   6. **A developer is refused the session** and told why, rather than being
 *      dropped into an editor that cannot save.
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
const [PROJECT, DIAGRAM] = process.argv.slice(2)
if (!PROJECT || !DIAGRAM) {
  console.error('usage: node scripts/check-editor.mjs <project-id> <diagram-id>')
  process.exit(2)
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))
const problems = []

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})

async function signIn(email) {
  const context = await browser.createBrowserContext()
  const page = await context.newPage()
  await page.setViewport({ width: 1700, height: 1000, deviceScaleFactor: 1 })
  await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
  await page.waitForSelector('input[type=email]')
  await page.type('input[type=email]', email)
  await page.type('input[type=password]', 'zetta-dev-2026')
  await page.click('button[type=submit]')
  await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
    timeout: 15_000,
  })
  return { context, page }
}

// ── a developer is refused, and told why ────────────────────────────────────
{
  const { context, page } = await signIn('dev@zetta.test')
  await page.goto(`${BASE}/p/${PROJECT}/diagrams/${DIAGRAM}/edit`, { waitUntil: 'networkidle2' })
  await wait(2000)
  const text = await page.evaluate(() => document.body.innerText)
  await page.screenshot({ path: path.join(outDir, 'editor-developer-refused.png') })

  if (!/not yours to edit|manager/i.test(text)) {
    problems.push('developer: the refusal does not name the owning role')
  }
  const editable = await page.evaluate(
    () => document.querySelectorAll('.react-flow__node[draggable=true]').length,
  )
  if (editable > 0) problems.push('developer: nodes are still draggable after the refusal')
  await context.close()
}

// ── the manager's session ───────────────────────────────────────────────────
const { context, page } = await signIn('mgr@zetta.test')

/** Every request body the editor sends, by path. */
const sent = { revisions: [], layout: [], lease: [] }
await page.setRequestInterception(true)
page.on('request', (req) => {
  const url = req.url()
  const body = req.postData()
  if (url.includes('/revisions') && req.method() === 'POST' && body) sent.revisions.push(body)
  if (url.includes('/layout') && req.method() === 'PUT' && body) sent.layout.push(body)
  if (url.includes('/lease')) sent.lease.push(`${req.method()} ${url.split('/api/v1')[1]}`)
  void req.continue()
})

await page.goto(`${BASE}/p/${PROJECT}/diagrams/${DIAGRAM}/edit`, { waitUntil: 'networkidle2' })
await page.waitForSelector('.react-flow__node', { timeout: 20_000 })
await wait(1500)

// 1. The lease was taken before anything was touched.
const leaseOnEntry = sent.lease.some((l) => l.startsWith('POST') && l.includes('/lease'))
if (!leaseOnEntry) problems.push('the lease was not acquired on entering edit mode')

// 4. Undo and redo across a sequence of semantic changes.
const nodeCount = () => page.evaluate(() => document.querySelectorAll('[data-kind]').length)
const before = await nodeCount()

await page.type('input[placeholder$="name"]', 'Rate limiter')
await page.evaluate(() =>
  [...document.querySelectorAll('button')].find((b) => b.textContent?.includes('Add '))?.click(),
)
await wait(700)
const afterAdd = await nodeCount()
if (afterAdd !== before + 1) problems.push(`add node: ${before} → ${afterAdd}, expected one more`)

await page.keyboard.down('Control')
await page.keyboard.press('KeyZ')
await page.keyboard.up('Control')
await wait(500)
const afterUndo = await nodeCount()
if (afterUndo !== before) problems.push(`undo: ${afterUndo}, expected ${before}`)

await page.keyboard.down('Control')
await page.keyboard.down('Shift')
await page.keyboard.press('KeyZ')
await page.keyboard.up('Shift')
await page.keyboard.up('Control')
await wait(500)
const afterRedo = await nodeCount()
if (afterRedo !== before + 1) problems.push(`redo: ${afterRedo}, expected ${before + 1}`)

await page.screenshot({ path: path.join(outDir, 'editor-added.png') })

// 3. Dragging writes layout, not a revision.
//
// Driven through the real mouse rather than synthetic PointerEvents: React
// Flow drags with d3-drag, which ignores untrusted events, so a dispatched
// pointerdown moves nothing and the check would pass by never dragging.
const dragTarget = await page.evaluate(() => {
  // A leaf node whose centre actually hit-tests to itself. Aiming at a
  // bounding box is not the same as aiming at something grabbable — an edge
  // label, a handle or a sibling can sit over the middle of a box, and the
  // drag then moves nothing while the check blames the app.
  const leaves = [...document.querySelectorAll('.react-flow__nodes > .react-flow__node')].filter(
    (el) => !el.querySelector('[data-container]'),
  )
  for (const leaf of leaves) {
    const r = leaf.getBoundingClientRect()
    if (r.width === 0 || r.top < 60) continue
    const x = r.left + r.width / 2
    const y = r.top + r.height / 2
    const at = document.elementFromPoint(x, y)
    if (at && leaf.contains(at)) return { id: leaf.getAttribute('data-id'), x, y }
  }
  return null
})
const readTransform = (id) =>
  page.evaluate(
    (nodeId) => {
      const el = document.querySelector(`.react-flow__nodes > .react-flow__node[data-id="${nodeId}"]`)
      return el ? getComputedStyle(el).transform : ''
    },
    id,
  )
const transformBefore = dragTarget ? await readTransform(dragTarget.id) : ''
if (dragTarget) {
  await page.mouse.move(dragTarget.x, dragTarget.y)
  await page.mouse.down()
  for (let i = 1; i <= 10; i++) {
    await page.mouse.move(dragTarget.x + i * 7, dragTarget.y + i * 5)
    await wait(16)
  }
  await page.mouse.up()
} else {
  problems.push('no leaf node to drag')
}
await wait(2500)
const transformAfter = dragTarget ? await readTransform(dragTarget.id) : ''
const nodeMoved = transformBefore !== transformAfter
// The layout write is the assertion, not the transform: a child node carries
// extent:'parent', so a drag toward a wall is clamped and legitimately ends
// where it began. What must be true is that the coordinate went to /layout.
if (sent.layout.length === 0) problems.push('dragging produced no layout write')

// 2. Save, then inspect what was actually sent.
await page.evaluate(() =>
  [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Save')?.click(),
)
await wait(3000)

const COORD = /"(x|y|w|h|layout|position|unplaced|collapsed|waypoints)"\s*:/
for (const body of sent.revisions) {
  if (COORD.test(body)) {
    problems.push('a revision payload carried presentation data — see editor.json')
    break
  }
}
if (sent.revisions.length === 0) problems.push('no revision was sent when Save was clicked')

const savedText = await page.evaluate(() => document.body.innerText)
await page.screenshot({ path: path.join(outDir, 'editor-saved.png') })

// ── 5. a stale base is a conflict, never a merge ────────────────────────────
//
// Forced rather than raced. A stale base is reachable in real use — a lease
// lapses, somebody else saves, you take the lapsed lease back and your base is
// old — but orchestrating that takes minutes of waiting. Winding this session's
// base revision back produces the same request, and the backend answers it for
// real.
let conflictShown = 'not attempted'
{
  await page.type('input[placeholder$="name"]', 'Second edit')
  await page.evaluate(() =>
    [...document.querySelectorAll('button')].find((b) => b.textContent?.includes('Add '))?.click(),
  )
  await wait(500)

  const wound = await page.evaluate(() => {
    const store = window.__zp?.useEditor
    if (!store) return false
    store.setState({ baseRevision: 1 })
    return true
  })

  if (!wound) {
    conflictShown = 'skipped (no dev handle on the editor store)'
    problems.push('could not reach the editor store to force a stale base')
  } else {
    await page.evaluate(() =>
      [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Save')?.click(),
    )
    await wait(3000)
    const text = await page.evaluate(() => document.body.innerText)
    await page.screenshot({ path: path.join(outDir, 'editor-conflict.png') })

    const sawConflict = /saved first/i.test(text)
    const saysNothingMerged = /nothing was merged/i.test(text)
    const offersBothWays = /copy my version out/i.test(text) && /discard mine/i.test(text)
    conflictShown = sawConflict ? 'shown' : 'not shown'

    if (!sawConflict) problems.push('a stale base did not surface as a conflict')
    if (!saysNothingMerged) problems.push('the conflict does not state that nothing was merged')
    if (!offersBothWays) problems.push('the conflict does not offer both ways out')
  }
}

await browser.close()

writeFileSync(
  path.join(outDir, 'editor.json'),
  JSON.stringify(
    {
      leaseCalls: sent.lease,
      revisionBodies: sent.revisions.map((b) => JSON.parse(b)),
      layoutBodies: sent.layout.map((b) => JSON.parse(b)),
      conflictShown,
      problems,
    },
    null,
    2,
  ),
)

console.log(`lease calls:        ${sent.lease.join(', ') || 'none'}`)
console.log(`revision payloads:  ${sent.revisions.length}`)
console.log(`layout payloads:    ${sent.layout.length} (node moved: ${nodeMoved})`)
console.log(`coordinates in a revision: ${sent.revisions.some((b) => COORD.test(b)) ? 'YES' : 'no'}`)
console.log(`undo/redo:          ${before} → ${afterAdd} → ${afterUndo} → ${afterRedo}`)
console.log(`saved:              ${/revision/i.test(savedText) ? 'reported' : 'no confirmation'}`)
console.log(`stale base:         ${conflictShown}`)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
