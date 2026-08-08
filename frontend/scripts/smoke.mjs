#!/usr/bin/env node
/**
 * Headless smoke check.
 *
 * FRONTEND §3 is clear that screenshot QA does not replace hands-on testing —
 * it cannot tell you whether a lease survives a sleeping laptop or whether an
 * unowned field was read-only *before* the attempt. What it does catch, every
 * phase and in seconds, is the runtime error that makes a screen blank, and
 * that is worth a script.
 *
 *   node scripts/smoke.mjs                     signed-out screens
 *   node scripts/smoke.mjs --as dev            sign in first
 *   node scripts/smoke.mjs --route /p/<id>/plan --name plan
 *
 * Writes PNGs to .smoke/ and fails the process on any console error.
 */

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const here = path.dirname(fileURLToPath(import.meta.url))
const web = path.resolve(here, '..')
const outDir = path.join(web, '.smoke')

const args = process.argv.slice(2)
const flag = (name, fallback) => {
  const i = args.indexOf(`--${name}`)
  return i === -1 ? fallback : args[i + 1]
}

const BASE = flag('base', 'http://localhost:5173')
const ROUTE = flag('route', '/')
const NAME = flag('name', 'app')
const AS = flag('as', null)
const PASSWORD = flag('password', 'zetta-dev-2026')
const WIDTH = Number(flag('width', 1440))
const HEIGHT = Number(flag('height', 900))
const THEMES = (flag('themes', 'dark,light') ?? '').split(',').filter(Boolean)
const ACCENT = flag('accent', 'gold')

const ACCOUNTS = {
  admin: 'admin@zetta.test',
  mgr: 'mgr@zetta.test',
  dev: 'dev@zetta.test',
  outsider: 'outsider@zetta.test',
}

mkdirSync(outDir, { recursive: true })

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage', `--window-size=${WIDTH},${HEIGHT}`],
})

const problems = []
const page = await browser.newPage()
await page.setViewport({ width: WIDTH, height: HEIGHT, deviceScaleFactor: 2 })

// Expected noise. A 401 on /auth/me while signed out is the answer to "is
// anyone signed in", and Chrome asks for a favicon nobody serves. Reporting
// either would train whoever reads this output to ignore it.
const EXPECTED = [/status of 401/]
const expected = (text) => EXPECTED.some((re) => re.test(text))

page.on('console', (msg) => {
  if (msg.type() !== 'error') return
  const text = msg.text()
  if (!expected(text)) problems.push(`console.error: ${text}`)
})
page.on('pageerror', (err) => problems.push(`pageerror: ${err.message}`))
// Everything before sign-in completes is signed-out traffic, including the
// 401 that decides which page to show.
let signedIn = false

page.on('response', (res) => {
  if (!res.url().includes('/api/')) return
  const status = res.status()
  if (status < 400) return
  if (status === 401 && !signedIn) return
  problems.push(`${status} ${res.url().replace(BASE, '')}`)
})

page.on('requestfailed', (req) => {
  const url = req.url()
  if (url.includes('/api/') && !url.includes('/events')) {
    problems.push(`request failed: ${url.replace(BASE, '')}`)
  }
})

async function settle(ms = 700) {
  await new Promise((r) => setTimeout(r, ms))
}

// Seed the appearance choice before first paint, the same way the app does.
await page.evaluateOnNewDocument((accent) => {
  localStorage.setItem('zp.appearance.accent', accent)
}, ACCENT)

await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })

if (AS) {
  const email = ACCOUNTS[AS] ?? AS
  await page.waitForSelector('input[type=email]', { timeout: 10_000 })
  await page.type('input[type=email]', email)
  await page.type('input[type=password]', PASSWORD)
  await Promise.all([page.click('button[type=submit]'), settle(1500)])
  const stillLogin = await page.$('input[type=password]')
  if (stillLogin) problems.push(`sign-in as ${email} did not complete`)
  else signedIn = true
}

if (ROUTE !== '/') {
  await page.goto(`${BASE}${ROUTE}`, { waitUntil: 'networkidle2' })
}
await settle(900)

const shots = []
for (const theme of THEMES) {
  await page.evaluate((t) => {
    localStorage.setItem('zp.appearance.mode', t)
    document.documentElement.setAttribute('data-theme', t)
  }, theme)
  await settle(350)
  const file = path.join(outDir, `${NAME}-${theme}.png`)
  await page.screenshot({ path: file, fullPage: false })
  shots.push(path.relative(web, file))
}

// A blank screen is the failure this script exists to catch, and it is
// invisible to a console-error check: React renders nothing and says nothing.
const textLength = await page.evaluate(() => document.body.innerText.trim().length)
if (textLength < 20) problems.push(`the page rendered ${textLength} characters of text — blank?`)

const summary = {
  route: ROUTE,
  as: AS ?? '(signed out)',
  accent: ACCENT,
  shots,
  textLength,
  problems,
}
writeFileSync(path.join(outDir, `${NAME}.json`), JSON.stringify(summary, null, 2))

await browser.close()

console.log(`smoke: ${NAME} ${ROUTE} as ${summary.as} — ${textLength} chars`)
for (const s of shots) console.log(`  ${s}`)
if (problems.length) {
  console.error(`\nsmoke: ${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('smoke: clean')
