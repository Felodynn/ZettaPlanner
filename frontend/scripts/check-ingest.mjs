#!/usr/bin/env node
/**
 * Ingest review's load-bearing claims (F5).
 *
 *   1. **The source image is beside the extraction**, on the same screen, in
 *      both the page step and the correction step. A reviewer whose job is
 *      comparison and who has to switch views to do it approves without
 *      checking.
 *   2. **Corrections are sorted by ascending confidence**, and unreadable
 *      elements are their own group rather than item 27 of a flat list.
 *   3. **An unreadable element blocks accept until it is named or dropped.**
 *      No threshold, no heuristic, and no invented label reaching the canvas.
 *   4. **Merge review is unskippable.** Accept is not reachable around it.
 *   5. **Retry is per page**, offered without re-uploading anything.
 *   6. **Accept previews its effect** — and the preview matches what the write
 *      actually produced, which is the only version of "preview" worth having.
 *
 * Run:  node scripts/check-ingest.mjs <project-id> [ingest-id]
 *
 * With no ingest id it uploads the two generated sketch pages and waits for
 * the model, which costs one call per page. Pass an id in `review` to re-run
 * the assertions against an existing one for free.
 */

import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
const sketchDir = path.join(outDir, 'sketch')
mkdirSync(sketchDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const API = process.env.ZP_API ?? 'http://localhost:8090'
const PASSWORD = process.env.ZP_PASSWORD ?? 'zetta-dev-2026'
const [PROJECT, EXISTING] = process.argv.slice(2)
if (!PROJECT) {
  console.error('usage: node scripts/check-ingest.mjs <project-id> [ingest-id]')
  process.exit(2)
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))
const problems = []

// ── the drawing ─────────────────────────────────────────────────────────────
//
// Generated rather than committed as a binary: the properties that matter —
// one box scribbled illegible, an arrow leaving the page, two pages that
// disagree about a kind — are the point, and a checked-in photograph makes
// them unreadable to anybody maintaining this.
const pages = [
  path.join(sketchDir, 'sketch-page-1.jpg'),
  path.join(sketchDir, 'sketch-page-2.jpg'),
]
if (!EXISTING && !pages.every(existsSync)) {
  execFileSync('python3', [path.join(web, 'scripts', 'draw-sketch.py'), sketchDir], {
    stdio: 'inherit',
  })
}

// ── an API session, for setup and for checking the write ────────────────────
const login = await fetch(`${API}/api/v1/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: 'mgr@zetta.test', password: PASSWORD }),
})
if (!login.ok) throw new Error(`login: ${login.status}`)
const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ')
const api = async (method, p, body) => {
  const r = await fetch(`${API}${p}`, {
    method,
    headers: { cookie, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    ...(body ? { body: JSON.stringify(body) } : {}),
  })
  const text = await r.text()
  let json
  try {
    json = JSON.parse(text)
  } catch {
    json = { raw: text }
  }
  return { status: r.status, json }
}

let ingestId = EXISTING
if (!ingestId) {
  const form = new FormData()
  for (const p of pages) {
    const { readFileSync } = await import('node:fs')
    form.append('files', new Blob([readFileSync(p)], { type: 'image/jpeg' }), path.basename(p))
  }
  const up = await fetch(`${API}/api/v1/projects/${PROJECT}/ingests`, {
    method: 'POST',
    headers: { cookie },
    body: form,
  })
  if (!up.ok) throw new Error(`upload: ${up.status} ${await up.text()}`)
  ingestId = (await up.json()).id
  process.stderr.write(`check-ingest: uploaded ${ingestId}, waiting for the model\n`)
}

// Extraction is minutes of model time on a cold cloud model.
let ingest
for (let i = 0; i < 60; i++) {
  const { json } = await api('GET', `/api/v1/ingests/${ingestId}`)
  ingest = json
  if (json.status === 'review' || json.status === 'failed' || json.status === 'accepted') break
  await wait(5000)
}
if (ingest?.status !== 'review') {
  console.error(`the ingest is ${ingest?.status}, not review — nothing to check`)
  process.exit(1)
}

const merged = ingest.merged
const illegible = (merged.nodes ?? []).filter((n) => !n.legible)
const counts = ingest.counts

// ── the browser ─────────────────────────────────────────────────────────────
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
  await page.type('input[type=password]', PASSWORD)
  await page.click('button[type=submit]')
  await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
    timeout: 15_000,
  })
  return { context, page }
}

/** Clicks a step in the rail by its label. */
const step = (page, label) =>
  page.evaluate((l) => {
    const b = [...document.querySelectorAll('button')].find((x) => x.textContent?.includes(l))
    b?.click()
    return Boolean(b)
  }, label)

const { context, page } = await signIn('mgr@zetta.test')
await page.goto(`${BASE}/p/${PROJECT}/ingest/${ingestId}`, { waitUntil: 'networkidle2' })
await page.waitForSelector('.react-flow__node', { timeout: 30_000 })
await wait(1200)

// ── 1. the page image is beside the extraction ──────────────────────────────
const sideBySide = await page.evaluate(() => {
  // The largest page image, not the first: the first is a page-strip thumbnail,
  // and a check that passes on the thumbnail would pass on a screen with no
  // readable source image at all.
  const img = [...document.querySelectorAll('img')]
    .filter((i) => /\/pages\/\d+\/image/.test(i.src))
    .sort((a, b) => b.getBoundingClientRect().width - a.getBoundingClientRect().width)[0]
  const canvas = document.querySelector('.react-flow')
  if (!img || !canvas) return null
  const a = img.getBoundingClientRect()
  const b = canvas.getBoundingClientRect()
  // Both on screen at once, and horizontally apart rather than stacked out of
  // view: "side by side" is the claim, not "both in the DOM".
  const visible = a.width > 100 && a.height > 100 && b.width > 100 && b.height > 100
  return { visible, imageRight: a.right, canvasLeft: b.left, apart: b.left >= a.right - 4 }
})
if (!sideBySide?.visible) problems.push('pages step: the page image and the extraction are not both on screen')
else if (!sideBySide.apart) problems.push('pages step: the image and the extraction overlap rather than sitting side by side')
await page.screenshot({ path: path.join(outDir, 'ingest-pages.png') })

// ── 5. retry is offered per page, without re-uploading ──────────────────────
const retryOffered = await page.evaluate(() =>
  [...document.querySelectorAll('button')].some((b) => /re-read this page/i.test(b.textContent ?? '')),
)
if (!retryOffered) problems.push('pages step: no per-page retry is offered on a reviewable page')

// ── 2 & 3. corrections: grouped, ascending, and blocking ────────────────────
await step(page, 'Corrections')
await wait(900)

const corrections = await page.evaluate(() => {
  const rows = [...document.querySelectorAll('input[aria-label^="Name for "]')].map((i) => {
    const row = i.closest('div.rounded-md')
    return {
      slug: i.getAttribute('aria-label').replace('Name for ', ''),
      text: row?.textContent ?? '',
    }
  })
  const body = document.body.innerText
  return {
    rows,
    imageOnScreen: [...document.querySelectorAll('img')].some((i) => {
      const r = i.getBoundingClientRect()
      return /\/pages\/\d+\/image/.test(i.src) && r.width > 200 && r.height > 200
    }),
    saysCouldNotBeRead: /could not be read/i.test(body),
    cleanCollapsed: /read cleanly and not flagged/i.test(body),
  }
})

// 1. the source image follows the reviewer into the correction step
if (!corrections.imageOnScreen) {
  problems.push('corrections step: the source page is not on screen beside the corrections')
}
if (illegible.length > 0 && !corrections.saysCouldNotBeRead) {
  problems.push('corrections step: unreadable elements are not called out as their own group')
}
if (!corrections.cleanCollapsed) {
  problems.push('corrections step: cleanly-read elements are not collapsed away from the flagged ones')
}

// Ascending confidence, read off the rendered order rather than off the data.
const shownPercents = await page.evaluate(() =>
  [...document.querySelectorAll('input[aria-label^="Name for "]')].map((i) => {
    const row = i.closest('div.rounded-md')
    const m = /(\d+)%/.exec(row?.textContent ?? '')
    return m ? Number(m[1]) : null
  }),
)
const rendered = shownPercents.filter((p) => p !== null)
const ascending = rendered.every((p, i) => i === 0 || rendered[i - 1] <= p)
if (!ascending) {
  problems.push(`corrections are not in ascending confidence order: ${rendered.join(', ')}`)
}

await page.screenshot({ path: path.join(outDir, 'ingest-corrections.png') })

// ── 4. merge review is unskippable ──────────────────────────────────────────
await step(page, 'Accept')
await wait(600)
const jumpedTheQueue = await page.evaluate(() => /What accepting will do/i.test(document.body.innerText))
if (jumpedTheQueue) problems.push('accept was reachable without passing merge review')

await step(page, 'Merge review')
await wait(700)
const mergeText = await page.evaluate(() => document.body.innerText)
if (!/Arrows that found nothing/i.test(mergeText)) {
  problems.push('merge review does not surface arrows that found nothing')
}
if (!/Arrows joined across pages/i.test(mergeText)) {
  problems.push('merge review does not surface cross-page joins')
}
await page.screenshot({ path: path.join(outDir, 'ingest-merge.png') })

await page.evaluate(() => document.querySelector('input[type=checkbox]')?.click())
await wait(400)

// ── 3. the unreadable gate, before and after naming ─────────────────────────
await step(page, 'Accept')
await wait(900)
const blockedByIllegible = await page.evaluate(() => {
  const b = [...document.querySelectorAll('button')].find((x) =>
    /^(Create the diagram|Add to the diagram)$/.test(x.textContent?.trim() ?? ''),
  )
  return { present: Boolean(b), disabled: b?.disabled ?? null, body: document.body.innerText }
})
if (illegible.length > 0) {
  if (!blockedByIllegible.disabled) {
    problems.push('accept is not blocked while an unreadable element has no name')
  }
  if (!/could not be read/i.test(blockedByIllegible.body)) {
    problems.push('accept does not say why it is blocked')
  }
}

// ── 6. the preview, and whether the write matched it ────────────────────────
const preview = await page.evaluate(() => {
  const t = document.body.innerText
  const nodes = /(\d+) nodes · (\d+) edges/.exec(t)
  return {
    additive: /Nothing is removed/i.test(t),
    previewsGraph: Boolean(document.querySelector('.react-flow__node')),
    nodes: nodes ? Number(nodes[1]) : null,
    edges: nodes ? Number(nodes[2]) : null,
    arrowsAsNotes: /unplaceable arrow/i.test(t),
  }
})
if (!preview.previewsGraph) problems.push('accept does not preview the graph it will write')
if (!preview.additive) problems.push('accept does not state that nothing is removed')
if (counts && preview.nodes !== null && preview.nodes !== counts.nodes) {
  problems.push(`accept previews ${preview.nodes} nodes; the merge has ${counts.nodes}`)
}
if ((counts?.unresolved ?? 0) > 0 && !preview.arrowsAsNotes) {
  problems.push('accept does not say that unplaceable arrows become notes')
}

await page.screenshot({ path: path.join(outDir, 'ingest-accept.png') })

// Name the unreadable elements, then accept for real and compare.
for (const n of illegible) {
  await step(page, 'Corrections')
  await wait(500)
  await page.evaluate(
    (slug, name) => {
      const input = document.querySelector(`input[aria-label="Name for ${slug}"]`)
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
      setter.call(input, name)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    },
    n.slug,
    `Named by the check (${n.slug})`,
  )
  await wait(300)
}

await step(page, 'Merge review')
await wait(600)
await page.evaluate(() => {
  const cb = document.querySelector('input[type=checkbox]')
  if (cb && !cb.checked) cb.click()
})
await wait(400)
await step(page, 'Accept')
await wait(900)

// A name of this check's own. The default is the title read off the page, and
// re-running the check would otherwise collide with the diagram the last run
// created — a slug is identity and is never reissued.
await page.evaluate((n) => {
  const input = document.querySelector('input[aria-label="Diagram name"]')
  if (!input) return
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
  setter.call(input, n)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}, `Ingest check ${ingestId.slice(0, 8)}`)
await wait(400)

const predicted = await page.evaluate(() => {
  const m = /(\d+) nodes · (\d+) edges/.exec(document.body.innerText)
  return m ? { nodes: Number(m[1]), edges: Number(m[2]) } : null
})

await page.evaluate(() => {
  const b = [...document.querySelectorAll('button')].find((x) =>
    /^(Create the diagram|Add to the diagram)$/.test(x.textContent?.trim() ?? ''),
  )
  b?.click()
})
await page.waitForFunction(() => location.pathname.includes('/diagrams/'), { timeout: 20_000 }).catch(
  () => problems.push('accept did not land on the diagram it created'),
)
await wait(1500)
await page.screenshot({ path: path.join(outDir, 'ingest-accepted.png') })

const diagramId = await page.evaluate(() => location.pathname.split('/diagrams/')[1] ?? '')
let actual = null
if (diagramId) {
  const { json } = await api('GET', `/api/v1/diagrams/${diagramId}`)
  actual = {
    nodes: (json.nodes ?? []).length,
    edges: (json.edges ?? []).length,
    notes: (json.notes ?? []).length,
    humanEdited: (json.nodes ?? []).filter((n) => n.provenance === 'human_edited').length,
    vlmExtracted: (json.nodes ?? []).filter((n) => n.provenance === 'vlm_extracted').length,
  }
  // The preview is only worth having if it was right.
  if (predicted && actual.nodes !== predicted.nodes) {
    problems.push(`preview promised ${predicted.nodes} nodes; the write produced ${actual.nodes}`)
  }
  if (predicted && actual.edges !== predicted.edges) {
    problems.push(`preview promised ${predicted.edges} edges; the write produced ${actual.edges}`)
  }
  // Anything a human named carries human_edited and no confidence (§3.7).
  if (illegible.length > 0 && actual.humanEdited < illegible.length) {
    problems.push('elements named by hand were not recorded as human_edited')
  }
  const withConfidence = await api('GET', `/api/v1/diagrams/${diagramId}`)
  const bad = (withConfidence.json.nodes ?? []).filter(
    (n) => n.provenance === 'human_edited' && n.confidence !== undefined,
  )
  if (bad.length > 0) {
    problems.push(`${bad.length} human-named element(s) kept the model's confidence`)
  }
  // Unplaceable arrows are kept as notes, never dropped.
  if ((counts?.unresolved ?? 0) > 0 && actual.notes < counts.unresolved) {
    problems.push(`${counts.unresolved} unplaceable arrow(s) produced only ${actual.notes} note(s)`)
  }
}

await context.close()
await browser.close()

writeFileSync(
  path.join(outDir, 'ingest.json'),
  JSON.stringify(
    { ingestId, counts, illegible: illegible.map((n) => n.slug), preview, predicted, actual, problems },
    null,
    2,
  ),
)

console.log(`ingest:             ${ingestId}`)
console.log(`merge:              ${counts.nodes} nodes, ${counts.edges} edges, ${counts.needs_review} flagged`)
console.log(`unreadable:         ${illegible.length} (${illegible.map((n) => n.slug).join(', ') || 'none'})`)
console.log(`unplaceable arrows: ${counts.unresolved}`)
console.log(`side by side:       ${sideBySide?.visible ? 'yes' : 'NO'}`)
console.log(`ascending order:    ${ascending ? 'yes' : 'NO'} (${rendered.join(', ') || 'n/a'})`)
console.log(`merge unskippable:  ${jumpedTheQueue ? 'NO' : 'yes'}`)
console.log(`illegible gate:     ${blockedByIllegible.disabled ? 'blocks accept' : 'does not block'}`)
console.log(`preview → write:    ${predicted?.nodes}/${predicted?.edges} → ${actual?.nodes}/${actual?.edges}`)
console.log(`provenance:         ${actual?.humanEdited} human_edited, ${actual?.vlmExtracted} vlm_extracted`)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK')
