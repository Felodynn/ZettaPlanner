#!/usr/bin/env node
// Generates src/api/schema.d.ts from the backend's OpenAPI document.
//
// FRONTEND §0.3 and frontend-notes §1: the client is generated and never
// hand-edited. `zettaplanner openapi` needs no database and no model server by
// design, so this is a build step rather than an operation — which is why it
// prefers the binary over a running server.
//
//   node scripts/generate-api.mjs              # build the backend, emit, generate
//   ZP_OPENAPI_FILE=/openapi.json …            # or read a document already emitted
//   ZP_OPENAPI_URL=http://host/openapi.json …  # or pull from a running server
//
// ZP_OPENAPI_FILE is what the container image build uses. Its backend stage has
// already produced the document from the very binary that will serve it, and
// the Node stage has no Go toolchain to build a second one with — so the choice
// there is between reading that file and skipping generation, and skipping it
// would mean the image ships whatever client happened to be committed.
//
// The generated file is committed. A stale client that compiles is worse than
// a missing one, so CI runs this and fails on a diff.

import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync, existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import openapiTS, { astToString } from 'openapi-typescript'

const here = path.dirname(fileURLToPath(import.meta.url))
const web = path.resolve(here, '..')
const backend = path.resolve(web, '..', 'backend')
const outFile = path.join(web, 'src', 'api', 'schema.d.ts')

function documentFromBinary() {
  const bin = path.join(backend, 'bin', 'zettaplanner')
  if (!existsSync(backend)) return null
  process.stderr.write('generate-api: building the backend to emit its document\n')
  execFileSync('go', ['build', '-o', bin, './cmd/zettaplanner'], { cwd: backend, stdio: 'inherit' })
  return execFileSync(bin, ['openapi'], { cwd: backend, maxBuffer: 64 * 1024 * 1024 }).toString()
}

async function documentFromURL(url) {
  process.stderr.write(`generate-api: fetching ${url}\n`)
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${url} answered ${res.status}`)
  return await res.text()
}

function documentFromFile(file) {
  if (!existsSync(file)) {
    throw new Error(`ZP_OPENAPI_FILE names ${file}, which does not exist`)
  }
  process.stderr.write(`generate-api: reading ${file}\n`)
  return readFileSync(file, 'utf8')
}

const raw = process.env.ZP_OPENAPI_FILE
  ? documentFromFile(process.env.ZP_OPENAPI_FILE)
  : process.env.ZP_OPENAPI_URL
    ? await documentFromURL(process.env.ZP_OPENAPI_URL)
    : documentFromBinary()

if (!raw) {
  throw new Error(
    'no OpenAPI document: the backend directory is missing and neither ' +
      'ZP_OPENAPI_FILE nor ZP_OPENAPI_URL is set',
  )
}

const doc = JSON.parse(raw)
const pathCount = Object.keys(doc.paths ?? {}).length

// A document describing only /health is what an unwired server emits, and a
// client generated from it type-checks perfectly while containing no API at
// all. Refuse it rather than write a convincing lie to disk.
if (pathCount < 10) {
  throw new Error(
    `the OpenAPI document describes ${pathCount} path(s). That is an unwired backend, ` +
      'not an API. Refusing to generate a client from it.',
  )
}

const ast = await openapiTS(doc, {
  alphabetize: true,
  arrayLength: false,
  emptyObjectsUnknown: true,
})

const banner = `/**
 * GENERATED FILE — DO NOT EDIT.
 *
 * Emitted from the backend's OpenAPI document by \`npm run api:generate\`.
 * CONTRACT §7: that document is authoritative for request and response shapes.
 * If a shape here looks wrong, the fix is in the Go handler — a local
 * interface that patches over it is the copy nobody is looking at.
 *
 * Paths described: ${pathCount}
 */

`

mkdirSync(path.dirname(outFile), { recursive: true })
writeFileSync(outFile, banner + astToString(ast))
process.stderr.write(`generate-api: wrote ${path.relative(web, outFile)} (${pathCount} paths)\n`)

// ── runtime vocabularies ─────────────────────────────────────────────────────
//
// The generated types refuse a bad value at compile time, but a <select> needs
// the options at run time and `type` erases. frontend-notes §1: the document
// carries the closed vocabularies as enums and they are the source for
// dropdowns, because a hand-kept option list is how a vocabulary quietly gains
// a seventh status.
//
// Each entry points at the property the backend declares the enum on. The
// emitted array is ordered as the backend declares it — `todo … dropped` is a
// progression, and alphabetising it would put `blocked` first.

const VOCABULARIES = [
  ['role', 'UserSummary', 'role', 'A deployment-wide role, before any per-project override'],
  ['projectStatus', 'ProjectSummary', 'status', 'Project lifecycle'],
  ['workItemStatus', 'WorkItemDetail', 'status', 'Shared by work items and subtasks (§3.5a)'],
  ['milestoneStatus', 'MilestoneSummary', 'status', 'Deliberately coarser than an item status'],
  ['priority', 'WorkItemDetail', 'priority', 'Unset is not `normal` — it means untriaged'],
  ['severity', 'DeviationSummary', 'severity', 'Deviation severity'],
  ['worklogKind', 'WorklogSummary', 'kind', 'Worklog kind'],
  ['dependencyKind', 'DependencySummary', 'kind', 'Between work items; `blocks` cycles are rejected'],
  ['nodeKind', 'NodeView', 'kind', 'The closed node vocabulary (D6, §3.3)'],
  ['relation', 'EdgeView', 'relation', 'The closed edge vocabulary (D6, §3.3)'],
  ['diagramLevel', 'DiagramSummary', 'level', 'Sequence diagrams are deferred to v0.1'],
  ['layoutMode', 'DiagramSummary', 'layout_mode', 'The first manual drag flips auto to manual'],
  ['provenance', 'NodeView', 'provenance', 'Where an object came from (§3.7)'],
  ['actorKind', 'AuditEntryView', 'actor_kind', 'Agents are principals (D11)'],
  ['noteKind', 'NoteView', 'kind', 'What a note is about'],
  ['noteStatus', 'NoteView', 'status', 'Note lifecycle'],
  ['proposalStatus', 'ProposalSummary', 'status', 'Proposal lifecycle (§5.1)'],
  ['proposalTarget', 'ProposalSummary', 'target_kind', 'What a proposal touches'],
  ['changeOp', 'ProposalChange', 'op', 'One typed change within a proposal'],
  ['turnStatus', 'TurnSummary', 'status', 'Agent turn lifecycle (§5.3)'],
  ['messageRole', 'MessageSummary', 'role', 'Who a message is from'],
  ['threadScope', 'ThreadSummary', 'scope_kind', 'Scope drives context assembly, never visibility'],
  ['ingestStatus', 'IngestView', 'status', 'Ingest lifecycle'],
  ['ingestPageStatus', 'IngestPageView', 'status', 'Failure is per page, and so is retry'],
  ['ingestSource', 'IngestView', 'source_kind', 'What was uploaded'],
  ['warningKind', 'WarningSummary', 'kind', 'Computed on read and stored nowhere (§3.5)'],
  ['ownershipScope', 'FieldRuleView', 'scope', 'What a caller needs beyond the role'],
  ['errorCode', 'ErrorBody', 'code', 'The closed error set (§7.3)'],
]

function enumAt(schema, property) {
  const s = doc.components?.schemas?.[schema]
  if (!s) throw new Error(`vocabulary: no schema ${schema}`)
  const p = s.properties?.[property]
  if (!p) throw new Error(`vocabulary: ${schema} has no property ${property}`)
  const values = p.enum ?? p.items?.enum
  if (!values) throw new Error(`vocabulary: ${schema}.${property} declares no enum`)
  return values
}

let vocab = `/**
 * GENERATED FILE — DO NOT EDIT.
 *
 * The closed vocabularies (D6, §3.3, §3.5a), read out of the same OpenAPI
 * document the types come from. Use these for every dropdown and every
 * exhaustive switch: a hand-kept option list drifts, and the drift is silent
 * until somebody's work item has a status no query finds again.
 *
 * Each array is \`satisfies\`-checked against the generated type, so a
 * vocabulary change in the backend breaks this build rather than the product.
 */

import type { components } from './schema'

type S = components['schemas']

`

for (const [name, schema, property, note] of VOCABULARIES) {
  const values = enumAt(schema, property)
  const literal = values.map((v) => JSON.stringify(v)).join(', ')
  const Name = name[0].toUpperCase() + name.slice(1)
  const CONST = `${name.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toUpperCase()}_VALUES`
  vocab += `/** ${note}. */\n`
  vocab += `export type ${Name} = NonNullable<S[${JSON.stringify(schema)}][${JSON.stringify(property)}]>\n`
  vocab += `export const ${CONST} = [${literal}] as const satisfies readonly ${Name}[]\n\n`
}

const vocabFile = path.join(web, 'src', 'api', 'vocabulary.ts')
writeFileSync(vocabFile, vocab)
process.stderr.write(
  `generate-api: wrote ${path.relative(web, vocabFile)} (${VOCABULARIES.length} vocabularies)\n`,
)
