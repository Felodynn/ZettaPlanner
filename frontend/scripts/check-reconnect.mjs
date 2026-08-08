#!/usr/bin/env node
/**
 * Breaks the stream and checks that the client closes the gap (F1).
 *
 * Two failures are being hunted, and they are opposites:
 *
 *   - **A gap.** Something happened while the client was away and the client
 *     never learns of it. This is the quiet one: the view is simply wrong, and
 *     nothing on screen says so.
 *   - **A duplicate.** The client reconnects, replays from `Last-Event-ID`,
 *     and appends replayed deltas onto text it already had.
 *
 * The break is a **backend restart**, not a simulated offline mode, because a
 * restart is unambiguous: it drops the in-memory replay ring, so the client
 * cannot be replayed to and must be told `resync` and refetch. That is the
 * widest gap the system can produce, and the one the recovery path exists for.
 *
 *   node scripts/check-reconnect.mjs <project-id>
 *
 * Needs ZP_RESTART to be a command that restarts the backend.
 */

import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const API = process.env.ZP_API ?? 'http://localhost:8090'
const PROJECT = process.argv[2]
if (!PROJECT) {
  console.error('usage: node scripts/check-reconnect.mjs <project-id>')
  process.exit(2)
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

/** Signs in over plain fetch, so a second actor can write while the browser watches. */
async function apiSession(email) {
  const res = await fetch(`${API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password: 'zetta-dev-2026' }),
  })
  if (!res.ok) throw new Error(`login as ${email}: ${res.status}`)
  const cookie = (res.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ')
  return {
    get: (p) => fetch(`${API}${p}`, { headers: { cookie } }).then((r) => r.json()),
    post: (p, body) =>
      fetch(`${API}${p}`, {
        method: 'POST',
        headers: { cookie, 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).then((r) => r.json()),
  }
}

const mgr = await apiSession('mgr@zetta.test')
const threads = await mgr.get(`/api/v1/projects/${PROJECT}/threads`)
const projectThread = (threads.threads ?? []).find((t) => t.scope_kind === 'project')
if (!projectThread) throw new Error('no project thread')

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1400, height: 900, deviceScaleFactor: 2 })
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
await page.waitForFunction(() => document.body.innerText.includes('Live'), { timeout: 15_000 })

const before = await page.evaluate(() => document.body.innerText)
await page.screenshot({ path: path.join(outDir, 'reconnect-1-live.png') })

// ── break it ────────────────────────────────────────────────────────────────
//
// The restart is started in the background and watched *while it happens*.
// Waiting for it to finish first would mean looking for the outage only after
// the backend was healthy again — which is how a check reports that the UI
// never showed a loss it did in fact show.
console.log('restarting the backend…')
let restartDone = false
const restarting = new Promise((resolve, reject) => {
  const child = spawn(process.env.ZP_RESTART ?? 'true', { shell: true, stdio: 'inherit' })
  child.on('exit', (code) => {
    restartDone = true
    code === 0 ? resolve() : reject(new Error(`restart exited ${code}`))
  })
})

let sawLoss = false
for (let i = 0; i < 120; i++) {
  const t = await page.evaluate(() => document.body.innerText)
  if (/Disconnected|Reconnecting/.test(t)) {
    sawLoss = true
    await page.screenshot({ path: path.join(outDir, 'reconnect-2-lost.png') })
    break
  }
  if (restartDone && i > 20) break
  await wait(250)
}
await restarting

// ── write into the gap ──────────────────────────────────────────────────────
// A message the watching browser cannot have been told about, posted while it
// is not connected. If it is on screen at the end, the gap closed.
const marker = `gap-marker-${Date.now().toString(36)}`
// Counted as a whole sentence, not by the marker alone: the agent tends to
// quote the prompt back, and a bare marker would then look like the very
// duplication this is checking for.
const posted = `Please ignore this, it is a reconnection test. ${marker}`
await wait(1500)
const mgr2 = await apiSession('mgr@zetta.test')
await mgr2.post(`/api/v1/threads/${projectThread.id}/messages`, { content: posted })

// ── let it recover ──────────────────────────────────────────────────────────
let cameBack = false
for (let i = 0; i < 60; i++) {
  const t = await page.evaluate(() => document.body.innerText)
  if (t.includes('Live')) {
    cameBack = true
    break
  }
  await wait(1000)
}

// The marker arrives either by replay or by the refetch that `resync` forces.
let closedGap = false
for (let i = 0; i < 60; i++) {
  const t = await page.evaluate(() => document.body.innerText)
  if (t.includes(posted)) {
    closedGap = true
    break
  }
  await wait(1000)
}

await wait(1500)
const after = await page.evaluate(() => document.body.innerText)
await page.screenshot({ path: path.join(outDir, 'reconnect-3-recovered.png') })

// Cancel whatever turn the marker started, so the project is left idle.
await page.evaluate(() => {
  ;[...document.querySelectorAll('button')].find((b) => b.textContent?.includes('Stop'))?.click()
})

await browser.close()

const markerCount = after.split(posted).length - 1

const problems = []
if (!sawLoss) problems.push('the UI never showed the connection as lost')
if (!cameBack) problems.push('the stream never came back')
if (!closedGap) problems.push('the message posted during the outage never appeared — a silent gap')
if (markerCount > 1) problems.push(`the recovered message appears ${markerCount} times`)

writeFileSync(
  path.join(outDir, 'reconnect.json'),
  JSON.stringify(
    { sawLoss, cameBack, closedGap, markerCount, beforeChars: before.length, afterChars: after.length, problems },
    null,
    2,
  ),
)

console.log(`showed the loss:            ${sawLoss}`)
console.log(`came back live:             ${cameBack}`)
console.log(`closed the gap:             ${closedGap}`)
console.log(`marker appears:             ${markerCount}×`)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK — lost the stream, said so, recovered, and closed the gap exactly once')
