#!/usr/bin/env node
/**
 * Field ownership must be visible *before* it is enforced (F2).
 *
 * The same item is opened as its assignee and as a manager, and each is
 * checked for the inverse of the other: the developer must find the planning
 * fields uneditable and the status field editable, the manager the reverse.
 *
 * The failure this exists to catch is subtle and common — a UI that renders
 * every control and lets the server sort it out. That version passes every
 * screenshot review, because a form full of inputs looks *more* finished than
 * one that tells you what you may not touch.
 *
 * Editability is measured by counting real form controls inside each panel,
 * not by looking for a lock icon: an icon can be present while the input
 * beside it still accepts typing.
 */

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(web, '.smoke')
mkdirSync(outDir, { recursive: true })

const BASE = process.env.ZP_BASE ?? 'http://localhost:5173'
const [PROJECT, ITEM] = process.argv.slice(2)
if (!PROJECT || !ITEM) {
  console.error('usage: node scripts/check-ownership.mjs <project-id> <item-id>')
  process.exit(2)
}

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/google-chrome',
  headless: 'new',
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})

async function inspect(email) {
  const context = await browser.createBrowserContext()
  const page = await context.newPage()
  await page.setViewport({ width: 1500, height: 1000, deviceScaleFactor: 1 })
  await page.evaluateOnNewDocument(() => localStorage.setItem('zp.appearance.mode', 'dark'))

  await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' })
  await page.waitForSelector('input[type=email]')
  await page.type('input[type=email]', email)
  await page.type('input[type=password]', 'zetta-dev-2026')
  await page.click('button[type=submit]')
  await page.waitForFunction(() => !document.querySelector('input[type=password]'), {
    timeout: 15_000,
  })
  await page.goto(`${BASE}/p/${PROJECT}/plan/${ITEM}`, { waitUntil: 'networkidle2' })
  await page.waitForFunction(() => document.body.innerText.includes('Planning'), { timeout: 15_000 })
  await new Promise((r) => setTimeout(r, 900))

  const result = await page.evaluate(() => {
    /** The card whose header contains this text. */
    const panel = (title) => {
      const heading = [...document.querySelectorAll('h2')].find((h) =>
        h.textContent?.includes(title),
      )
      return heading?.closest('div.rounded-card') ?? null
    }
    const controls = (el) =>
      el ? el.querySelectorAll('input:not([disabled]), select:not([disabled]), textarea:not([disabled])').length : -1
    const text = (el) => (el ? el.innerText : '')

    const progress = panel('Status and progress')
    const planning = panel('Planning')
    const subtasks = panel('Sub-tasks')
    const checklist = panel('Checklist')

    return {
      progressControls: controls(progress),
      planningControls: controls(planning),
      subtaskControls: controls(subtasks),
      checklistControls: controls(checklist),
      progressText: text(progress),
      planningText: text(planning),
      subtaskText: text(subtasks),
      checklistText: text(checklist),
    }
  })

  await page.screenshot({ path: path.join(outDir, `ownership-${email.split('@')[0]}.png`) })
  await context.close()
  return result
}

const dev = await inspect('dev@zetta.test')
const mgr = await inspect('mgr@zetta.test')
await browser.close()

const problems = []

// The assignee reports status and progress; a manager reads them.
if (dev.progressControls < 1) problems.push('developer: status/progress is not editable')
if (mgr.progressControls !== 0) {
  problems.push(`manager: status/progress has ${mgr.progressControls} editable control(s)`)
}
if (!/developer-owned/.test(mgr.progressText)) {
  problems.push('manager: the status panel does not name the owning role')
}

// Planning belongs to a manager.
if (mgr.planningControls < 4) problems.push('manager: planning fields are not editable')
if (dev.planningControls !== 0) {
  problems.push(`developer: planning has ${dev.planningControls} editable control(s)`)
}
if (!/manager-owned/.test(dev.planningText)) {
  problems.push('developer: the planning panel does not name the owning role')
}
if (!/raise a proposal/.test(dev.planningText)) {
  problems.push('developer: the planning panel offers no route forward')
}

// Sub-tasks and checklist are the assignee's, on their own item.
if (dev.subtaskControls < 1) problems.push('developer: cannot add a sub-task on their own item')
if (mgr.subtaskControls !== 0) problems.push('manager: sub-tasks are editable')
if (dev.checklistControls < 1) problems.push('developer: cannot use the checklist on their own item')
if (mgr.checklistControls !== 0) problems.push('manager: the checklist is editable')

writeFileSync(path.join(outDir, 'ownership.json'), JSON.stringify({ dev, mgr, problems }, null, 2))

const row = (name, a, b) => console.log(`${name.padEnd(22)} dev ${String(a).padStart(2)}   mgr ${String(b).padStart(2)}`)
console.log('editable controls per panel')
row('status and progress', dev.progressControls, mgr.progressControls)
row('planning', dev.planningControls, mgr.planningControls)
row('sub-tasks', dev.subtaskControls, mgr.subtaskControls)
row('checklist', dev.checklistControls, mgr.checklistControls)

if (problems.length) {
  console.error(`\n${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('\nOK — each role sees the other half read-only, with the owner named and a route offered')
