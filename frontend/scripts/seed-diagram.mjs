#!/usr/bin/env node
/**
 * Seeds a diagram that exercises the whole closed vocabulary.
 *
 * F3's exit criteria are about legibility at scale — all 11 node kinds and 9
 * relations distinct, boundaries containing their children, a 40+ node graph
 * laying out readably. None of that can be judged against the three-node
 * diagrams a fresh project has, and "can you tell a queue from a datastore
 * without reading the label" is not a question a small example can answer.
 *
 * Mixed provenance and confidence on purpose: low-confidence elements are
 * marked in *every* mode, not only in ingest review, because a hand-corrected
 * diagram still contains extracted elements.
 */

const API = process.env.ZP_API ?? 'http://localhost:8090'
const PROJECT = process.argv[2]
const PASSWORD = process.env.ZP_PASSWORD ?? 'zetta-dev-2026'
if (!PROJECT) {
  console.error('usage: node scripts/seed-diagram.mjs <project-id>')
  process.exit(2)
}

const res = await fetch(`${API}/api/v1/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: 'mgr@zetta.test', password: PASSWORD }),
})
if (!res.ok) throw new Error(`login: ${res.status}`)
const cookie = (res.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ')

const api = async (method, path, body) => {
  const r = await fetch(`${API}${path}`, {
    method,
    headers: { cookie, 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
  })
  const text = await r.text()
  let json
  try {
    json = JSON.parse(text)
  } catch {
    json = { raw: text }
  }
  if (!r.ok) throw new Error(`${method} ${path} → ${r.status}: ${JSON.stringify(json).slice(0, 300)}`)
  return json
}

// Existing diagram of this slug, or a new one.
const { diagrams } = await api('GET', `/api/v1/projects/${PROJECT}/diagrams`)
const SLUG = 'platform-overview'
let diagram = (diagrams ?? []).find((d) => d.slug === SLUG)
if (!diagram) {
  diagram = await api('POST', `/api/v1/projects/${PROJECT}/diagrams`, {
    name: 'Platform Overview',
    level: 'container',
    slug: SLUG,
  })
}
const current = await api('GET', `/api/v1/diagrams/${diagram.id}`)

const n = (slug, kind, name, extra = {}) => ({ slug, kind, name, ...extra })

const nodes = [
  // Boundaries first — they are the containers everything else names.
  n('edge-zone', 'boundary', 'Edge / DMZ', { parent: 'platform', summary: 'Public trust zone' }),
  n('core-zone', 'boundary', 'Core services', { parent: 'platform', summary: 'Internal trust zone' }),
  n('data-zone', 'boundary', 'Stateful tier', { parent: 'platform' }),
  n('platform', 'system', 'ZettaPlanner Platform', { summary: 'The system under discussion' }),

  // Actors
  n('end-user', 'actor', 'Developer', { parent: 'edge-zone' }),
  n('manager-actor', 'actor', 'Engineering manager', { parent: 'edge-zone' }),
  n('operator', 'actor', 'Operator', { provenance: 'human_edited' }),

  // APIs and edge
  n('public-api', 'api', 'Public API', { parent: 'edge-zone', summary: 'REST over /api/v1' }),
  n('sse-stream', 'api', 'Event stream', { parent: 'edge-zone', summary: 'text/event-stream' }),
  n('web-spa', 'component', 'Web SPA', { parent: 'edge-zone' }),
  n('reverse-proxy', 'service', 'Reverse proxy', { parent: 'edge-zone' }),

  // Core services
  n('auth-svc', 'service', 'Auth Service', { parent: 'core-zone' }),
  n('plan-svc', 'service', 'Plan Service', { parent: 'core-zone' }),
  n('diagram-svc', 'service', 'Diagram Service', { parent: 'core-zone' }),
  n('ingest-svc', 'service', 'Ingest Service', { parent: 'core-zone' }),
  n('governance-svc', 'service', 'Governance Service', { parent: 'core-zone' }),
  n('context-svc', 'service', 'Context Assembly', { parent: 'core-zone' }),
  n('agent-runner', 'service', 'Agent Runner', { parent: 'core-zone' }),
  n('export-svc', 'service', 'Export Service', { parent: 'core-zone' }),

  // Components inside services
  n('policy-engine', 'component', 'Policy engine', { parent: 'core-zone', summary: 'Field ownership (§4.2)' }),
  n('serializer', 'component', 'Semantic serializer', { parent: 'core-zone' }),
  n('merge-step', 'component', 'Merge step', { parent: 'core-zone', provenance: 'vlm_extracted', confidence: 0.42 }),
  n('layout-store', 'component', 'Layout store', { parent: 'core-zone' }),
  n('vector-index', 'component', 'Vector index', { parent: 'core-zone' }),

  // Datastores
  n('postgres', 'datastore', 'PostgreSQL 16', { parent: 'data-zone', summary: 'Sole store of record (D1)' }),
  n('pgvector', 'datastore', 'pgvector', { parent: 'data-zone' }),
  n('uploads', 'datastore', 'Upload store', { parent: 'data-zone', summary: 'data/uploads/<project>' }),
  n('session-store', 'datastore', 'Session store', { parent: 'data-zone' }),
  n('audit-log', 'datastore', 'Audit log', { parent: 'data-zone', summary: 'Append-only, retention never' }),
  n('git-tree', 'datastore', 'Git projection', { parent: 'data-zone', provenance: 'agent_committed' }),

  // Queues
  n('turn-queue', 'queue', 'Turn queue', { parent: 'core-zone', summary: 'One running turn per project' }),
  n('event-hub', 'queue', 'Event hub', { parent: 'core-zone' }),
  n('ingest-queue', 'queue', 'Ingest queue', { parent: 'core-zone', provenance: 'vlm_extracted', confidence: 0.31 }),

  // Jobs
  n('git-worker', 'job', 'Git projection worker', { parent: 'core-zone' }),
  n('lease-sweeper', 'job', 'Lease sweeper', { parent: 'core-zone' }),
  n('reaper', 'job', 'Session reaper', { parent: 'core-zone' }),
  n('digest-refresh', 'job', 'Digest refresh', { parent: 'core-zone', provenance: 'agent_proposed' }),

  // External
  n('ollama', 'external_system', 'Ollama daemon', { summary: 'Local and cloud, one code path (D4)' }),
  n('planner-model', 'external_system', 'Planner model', { summary: 'glm-5.2:cloud' }),
  n('vision-model', 'external_system', 'Vision model', { provenance: 'vlm_extracted', confidence: 0.66 }),
  n('embed-model', 'external_system', 'Embedding model', { summary: 'bge-m3, local only' }),
  n('smtp', 'external_system', 'SMTP', { summary: 'Deferred — no notifications in v0', provenance: 'vlm_extracted', confidence: 0.18 }),

  // Notes carry no relations.
  n('note-d5', 'note', 'D5: the semantic serializer cannot import the layout store. Structural, not a filtering step.'),
  n('note-vocab', 'note', 'Vocabulary changes are a migration plus a prompt version bump, never configuration.'),
]

const e = (from, to, relation, extra = {}) => ({ from, to, relation, ...extra })

const edges = [
  // contains — only from a boundary or a system
  e('platform', 'core-zone', 'contains'),
  e('core-zone', 'policy-engine', 'contains'),
  e('data-zone', 'postgres', 'contains'),

  // calls
  e('end-user', 'web-spa', 'calls', { label: 'browser' }),
  e('manager-actor', 'web-spa', 'calls'),
  e('web-spa', 'reverse-proxy', 'calls', { label: 'HTTPS' }),
  e('reverse-proxy', 'public-api', 'calls'),
  e('public-api', 'auth-svc', 'calls', { label: 'POST /auth/login' }),
  e('public-api', 'plan-svc', 'calls'),
  e('public-api', 'diagram-svc', 'calls'),
  e('public-api', 'ingest-svc', 'calls'),
  e('public-api', 'governance-svc', 'calls'),
  e('public-api', 'export-svc', 'calls'),
  e('agent-runner', 'ollama', 'calls', { label: '/api/chat' }),
  e('ingest-svc', 'ollama', 'calls', { label: 'one call per page' }),
  e('context-svc', 'ollama', 'calls', { label: 'embeddings' }),
  e('operator', 'git-worker', 'calls', { label: 'enable per project' }),

  // depends_on
  e('plan-svc', 'policy-engine', 'depends_on'),
  e('diagram-svc', 'policy-engine', 'depends_on'),
  e('governance-svc', 'policy-engine', 'depends_on'),
  e('diagram-svc', 'serializer', 'depends_on'),
  e('ingest-svc', 'merge-step', 'depends_on', { confidence: 0.44, provenance: 'vlm_extracted' }),
  e('ollama', 'planner-model', 'depends_on'),
  e('ollama', 'vision-model', 'depends_on'),
  e('ollama', 'embed-model', 'depends_on'),

  // reads_from / writes_to
  e('auth-svc', 'session-store', 'writes_to', { label: 'session rows' }),
  e('auth-svc', 'session-store', 'reads_from'),
  e('plan-svc', 'postgres', 'writes_to'),
  e('diagram-svc', 'postgres', 'writes_to'),
  e('diagram-svc', 'layout-store', 'writes_to', { label: 'coordinates only' }),
  e('serializer', 'postgres', 'reads_from', { label: 'semantic tables only' }),
  e('context-svc', 'pgvector', 'reads_from'),
  e('vector-index', 'pgvector', 'writes_to'),
  e('ingest-svc', 'uploads', 'writes_to'),
  e('export-svc', 'postgres', 'reads_from'),
  e('reaper', 'session-store', 'reads_from'),
  e('git-worker', 'postgres', 'reads_from'),
  e('git-worker', 'git-tree', 'writes_to'),
  e('policy-engine', 'audit-log', 'writes_to'),
  e('governance-svc', 'audit-log', 'writes_to'),
  e('digest-refresh', 'postgres', 'reads_from'),

  // publishes_to / subscribes_to
  e('agent-runner', 'event-hub', 'publishes_to', { label: 'message.delta' }),
  e('governance-svc', 'event-hub', 'publishes_to', { label: 'proposal.created' }),
  e('diagram-svc', 'event-hub', 'publishes_to'),
  e('ingest-svc', 'ingest-queue', 'publishes_to', { confidence: 0.29, provenance: 'vlm_extracted' }),
  e('sse-stream', 'event-hub', 'subscribes_to'),
  e('agent-runner', 'turn-queue', 'subscribes_to'),
  e('plan-svc', 'turn-queue', 'publishes_to'),

  // deploys_to
  e('auth-svc', 'core-zone', 'deploys_to'),
  e('reverse-proxy', 'edge-zone', 'deploys_to'),
  e('postgres', 'data-zone', 'deploys_to'),

  // data_flow — when nothing more specific is true
  e('merge-step', 'diagram-svc', 'data_flow', { label: 'accepted graph' }),
  e('context-svc', 'agent-runner', 'data_flow', { label: 'assembled context' }),
  e('lease-sweeper', 'diagram-svc', 'data_flow'),
  e('smtp', 'operator', 'data_flow', { confidence: 0.2, provenance: 'vlm_extracted' }),
]

// A semantic save is lease-gated (D9): edit sessions are leases, and the
// whole-diagram atomic write is what makes one revision per human action.
const lease = await api('POST', `/api/v1/diagrams/${diagram.id}/lease`)
let saved
try {
  saved = await api('POST', `/api/v1/diagrams/${diagram.id}/revisions`, {
    base_revision: lease.base_revision ?? current.revision,
    summary: 'Seed: the whole closed vocabulary, for legibility testing',
    nodes,
    edges,
  })
} finally {
  await api('DELETE', `/api/v1/diagrams/${diagram.id}/lease`).catch(() => {})
}

console.log(`diagram ${diagram.id} (${SLUG})`)
console.log(`  revision ${saved.revision}`)
console.log(`  ${nodes.length} nodes, ${edges.length} edges`)
console.log(`  kinds: ${new Set(nodes.map((x) => x.kind)).size}/11`)
console.log(`  relations: ${new Set(edges.map((x) => x.relation)).size}/9`)
if ((saved.notes_raised ?? []).length) {
  console.log(`  notes raised: ${saved.notes_raised.length}`)
}
