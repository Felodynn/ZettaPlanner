# ZettaPlanner — Shared Contract

**Status:** Draft v1
**Owner:** Solo technical founder (Felodynn)
**Audience:** the backend agent AND the frontend agent, equally
**Companion documents:** `BACKEND.md`, `FRONTEND.md`

---

## 0. How to use this document

This file is the **single source of truth for anything that crosses the
backend/frontend boundary**: the node vocabulary, the data model, the field
ownership rules, the API surface, the event shapes, and the permission matrix.

**Rules of engagement:**

1. Neither agent owns this file. Changes to it are a decision, recorded as a
   `Dn` entry in whichever build document forced the change, and applied here
   first.
2. `BACKEND.md` and `FRONTEND.md` may **narrow** this contract (implement a
   subset, in phase order). Neither may **contradict** it.
3. The generated OpenAPI document is derived from §7 and is authoritative for
   request/response *shapes*. This file is authoritative for *meaning*.
4. Anything marked **[DEFERRED]** is deliberately out of scope for v0. Do not
   build it early. Deferring is a decision that has already been made.
5. Where this document names a constraint as a *hole* or a *failure mode*, it is
   describing something that has already gone wrong in a comparable system.
   Treat it as load-bearing, not as commentary.

---

## 1. What ZettaPlanner is

ZettaPlanner is an **AI-native technical planning substrate**: a durable, typed
project graph that humans and LLM agents both edit, under explicit governance,
with full provenance.

It has two entry paths into the same graph:

- **Draw-to-product** — hand-drawn HLD images are read by a vision model into a
  typed architecture graph, reviewed and corrected by a human, and stored.
- **Plan-and-execute** — milestones, work items, timelines, checklists,
  worklogs and deviations, maintained conversationally with a planner agent and
  directly by humans.

The architecture graph and the plan are **linked but not derived from each
other**. No agent infers plan structure from a diagram. (§3.5)

### 1.1 What it is not

| Not this | Because |
|---|---|
| A ticket tracker with a chatbot bolted on | The typed graph is the product; chat is one editor among several |
| A whiteboard tool | Free-form boxes and lines cannot be queried, and a plan that cannot be queried cannot be reasoned over |
| A multi-tenant SaaS | One deployment, one organisation, one backend. Sovereignty is the selling point (D3) |
| An autonomous planner | The agent proposes; a human with the right role disposes. Autonomy is scoped by field ownership (§4) |

### 1.2 The core bet

Long technical projects fail at the seams: a decision is made, the reasoning is
lost, and six weeks later someone re-litigates it or silently violates it.
Chat-based tools lose it in the transcript. Ticket trackers never had it.

**Therefore: the typed graph, the provenance record, and the proposal/approval
loop are not features. They are the product.** Everything that matters is
written into a typed object at the moment it is established, and the
conversation is disposable.

---

## 2. Settled decisions

Closed. Recorded here so they are not re-argued mid-implementation.

| # | Decision | Rationale |
|---|---|---|
| D1 | **PostgreSQL 16+ with pgvector** is the sole store of record | Rejected a separate vector DB: a second store means dual-write with no shared transaction, so a crash between the row insert and the vector insert leaves an un-retrievable worklog or an orphan vector, recoverable only by a reconciler nobody has written. Searches here are nearly always filtered (project, date range, assignee) — in Postgres that is a `WHERE`; in Milvus it is a duplicated copy of the relational model inside the collection metadata. HNSW is comfortable into the millions of vectors; a large project's unstructured spill is tens of thousands. Milvus stays available behind the `VectorStore` interface (§6.4) and earns its place at tens of millions, GPU indexes, or multi-tenancy — none of which v0 has |
| D2 | **Go backend, React+TypeScript frontend**, single binary with the SPA embedded | On-prem deployment is one artifact plus Postgres. TypeScript is not optional — the API client is generated, and a generated JS client is a generated lie |
| D3 | **Single-tenant per deployment.** No cross-organisation isolation | Target buyers will not put architecture IP in someone else's cloud. Multi-tenancy is the largest schema tax available and buys nothing for the actual deployment model. Multiple *projects* per deployment, yes; multiple *customers*, never |
| D4 | **Ollama-native `/api/chat`** for local and cloud, with mandatory startup validation | Inherited from LumixAgents D4a: `/v1/chat/completions` silently discards `num_ctx`, and a model that truncates at 4096 produces agents with apparent amnesia that cost days to diagnose. `/api/show` reports the model's maximum; only `/api/ps` proves what the server actually allocated |
| D5 | **Semantic layer and presentation layer are separate stores** | Coordinates are meaningless to a model and actively harmful in a prompt — the planner derives flow from relations, and a large diagram's geometry invites it to derive flow from position instead. Enforced structurally: the semantic serializer has no access to the layout tables (§3.2). Not "we filter coordinates before the call" |
| D6 | **Closed node and edge vocabulary** (§3.3), not per-project configurable | Configurable types mean prompts must be regenerated and versioned per project, and the query surface degrades to string matching. UX over flexibility. The same closed vocabulary is what makes VLM extraction reliable |
| D7 | **Field ownership is mechanical** (§4.2), computed from role and assignment | An agent judging whether its own change needs approval is not a control. The write path checks the field, not the intent |
| D8 | **Chat is transcript, never memory.** Context is assembled by querying the graph | The single biggest determinant of whether large projects hold together. A fact that exists only in a message will be lost at the first compaction and cannot be queried by anything |
| D9 | **Edit sessions are leases, not locks or CRDTs** | CRDTs on a typed graph is a project, not a feature. A lease with heartbeat plus atomic whole-diagram save gives one revision per human action, which is what makes history and audit readable |
| D10 | **Postgres is the store of record; git is an optional one-way projection** | Both-as-truth is the same dual-write failure as D1. Git is allowed to be stale and is regenerable from the DB, which is what makes it safe. It buys diff and blame ergonomics, nothing structural. Off by default |
| D11 | **Agents are principals**, subject to the same permission checks as humans | Otherwise "the user asked the agent to" becomes the way to write what the user could not write directly |
| D12 | **C++/libtorch inference is [DEFERRED] to v0.1+** | Applying LumixAgents D2a's standard: a native dependency is justified only when no pure-Go path exists. Every v0 intelligence is LLM inference, and Ollama serves generation *and* embeddings over HTTP. Revisit when a custom DNN/ML pipeline exists, not before |
| D13 | **The audit record is complete in v0; the CSV export is v0.1** (§6.5) | Recording and dumping are separable, and only one of them can be backfilled. Every day the product runs with an incomplete record is a day of retrospective data that does not exist when someone goes looking for it. The export is a query over a complete log — a weekend of work, deferred without cost. The log itself must be right from the first commit, so v0 carries the constraints that make it exportable later: no hard deletes, transition-specific actions, export indexes, populated `request_id`, and a stated retention of *never* |

---

## 3. Core concepts

### 3.1 Object map

```
Deployment
 └── User, Actor
 └── Project
      ├── Diagram*            (architecture / flow — §3.2)
      │    ├── DiagramNode*  ──► CatalogEntity   (project-level identity)
      │    ├── DiagramEdge*
      │    ├── Layout         (separate store, D5)
      │    └── Revision*
      ├── Milestone*          (plan — §3.5)
      │    └── WorkItem*
      │         ├── SubTask*
      │         ├── ChecklistItem*
      │         ├── Worklog*
      │         └── Deviation*
      ├── Thread              (one per project, global — §3.6)
      │    └── Message*, AgentTurn*
      ├── Proposal*           (governance — §5)
      ├── Note*               (agent-maintained clarifications — §3.4)
      ├── Digest              (incremental project state — §6.3)
      └── AuditEntry*
```

### 3.2 Diagram: two layers, one identity

A diagram has a **semantic layer** and a **presentation layer**, stored
separately (D5).

| Layer | Contains | Fed to models? | Serialized to YAML/git? |
|---|---|---|---|
| Semantic | nodes (kind, entity ref, attributes), edges (relation, attributes) | **Yes** | Yes |
| Presentation | x, y, width, height, edge waypoints, collapsed, `unplaced` | **Never** | No |

The semantic serializer (§6.2) reads only semantic tables. This is a code
structure requirement, not a filtering step: the packages that produce model
context must not import the layout store.

**Layout mode.** A diagram is created `auto` (dagre-computed on render, no
coordinates stored). The first manual node drag flips it to `manual`
permanently, and coordinates begin to be stored. Re-running auto layout on a
`manual` diagram is destructive and requires explicit confirmation.

**Unplaced nodes.** When a node arrives on a `manual` diagram without
coordinates — from an accepted agent proposal, or a re-ingest — it is placed
near the centroid of its connected neighbours and flagged `unplaced: true`. The
UI must make unplaced nodes visually distinct. The flag clears on first human
move.

### 3.3 Node kinds and edge relations (closed vocabulary, D6)

**Node kinds:**

| Kind | Means |
|---|---|
| `system` | A whole system under discussion. Top-level box in a context diagram |
| `service` | An independently deployable process |
| `component` | A module inside a service. Not independently deployable |
| `datastore` | A database, cache, object store, or file store |
| `queue` | A message broker, topic, or stream |
| `external_system` | Something outside the team's control. Third-party API, vendor system |
| `actor` | A human role or class of user |
| `api` | A named interface surface. Use when the interface is the subject, not the process behind it |
| `job` | A scheduled or triggered batch process |
| `boundary` | A grouping container: trust zone, deployment unit, team ownership. May contain other nodes |
| `note` | Free text pinned to the diagram. Carries no relations |

**Edge relations:**

| Relation | Means | Directed |
|---|---|---|
| `calls` | Synchronous request/response | Yes |
| `depends_on` | Needs to exist or be available. Non-specific | Yes |
| `reads_from` | Reads data | Yes |
| `writes_to` | Writes data | Yes |
| `publishes_to` | Emits to a queue or topic | Yes |
| `subscribes_to` | Consumes from a queue or topic | Yes |
| `contains` | Structural containment. Only from `boundary` or `system` | Yes |
| `deploys_to` | Runtime placement | Yes |
| `data_flow` | Generic flow when nothing more specific is true | Yes |

Every edge carries an optional `label` (free text, e.g. `POST /auth/token`) and
an optional `attrs` map. `note` nodes participate in no relations.

**Vocabulary changes are a schema migration plus a prompt version bump.** They
are not configuration. If a project needs a kind that does not exist, that is a
`Dn` entry and a migration, deliberately.

**Diagram levels:** every diagram is tagged `context`, `container`, or
`component`. Sequence diagrams are **[DEFERRED to v0.1]** — a different shape
with a different editor, needed for LLDs of larger projects but not for v0.

### 3.4 CatalogEntity: identity across diagrams

A `CatalogEntity` is a project-level named thing (`service:auth-service`).
`DiagramNode` is a *placement* of an entity in a diagram. One entity may appear
in a context diagram and a component diagram as the same identity.

This is what makes "what depends on the token store" answerable across the whole
project rather than per-picture.

**Entity resolution is not automatic.** On ingest or agent proposal, when a name
plausibly matches an existing entity, the backend records a **Note** rather than
merging. Notes are the agent's and the harness's channel for clarifications:
overlaps, conflicts, likely duplicates, missing pieces. A note has a `status`
(`open`, `resolved`, `dismissed`) and a subject reference.

**Notes live in Postgres and in the assembled context. They are never chunked
into the vector store.** They are structured facts, retrieved by traversal.

### 3.5 Plan: independent of the diagram

```
Project → Milestone → WorkItem → SubTask   (SubTask is a leaf; no deeper nesting)
```

Dependencies are edges **between WorkItems**, not a tree: `blocks` and
`relates`. Cycles in `blocks` are rejected at write time.

**A WorkItem may carry soft references to CatalogEntities** (`implements`), set
by a human or an agent, **never derived**. They exist purely so context
assembly can pull the relevant architecture slice when discussing the item.

No agent derives plan structure from a diagram, and no agent derives diagram
structure from the plan. This is deliberate: a derivation that is wrong 10% of
the time is a permanent source of confusion with no permanent fix.

**Timelines in v0** are explicit `start_date` / `due_date` on milestones and
work items, plus **dependency violation warnings** (item starts before its
blocker's due date; item due after its milestone's due date). **No
auto-scheduling and no critical-path computation.** [DEFERRED]

### 3.6 Conversation

**One thread per project, globally visible to every project member.** Everyone
posts into it; everyone reads it, including managers reading developers' turns.
Item-scoped and diagram-scoped threads exist and are *also* globally readable —
scope controls context assembly and UI placement, never visibility.

**Agent turns serialize per project** (§5.3). Chat is transcript, not memory
(D8).

### 3.7 Provenance

Every node, edge, work item, and plan field carries an origin:

| Value | Means |
|---|---|
| `vlm_extracted` | Produced by the vision model on ingest. Carries `confidence` (0.0–1.0) |
| `human_created` | A human created it directly |
| `human_edited` | A human modified something that originated elsewhere |
| `agent_proposed` | Created by an agent, pending approval |
| `agent_committed` | Created by an agent within its direct-write authority (§4.2) |
| `system` | Created by the harness (migration, ingest scaffold) |

Plus `origin_revision` (or `origin_proposal_id`) and `actor_id`.

**Why this earns its cost:** the re-evaluate flow (§5.4) shows the vision model
exactly which of its extractions a human corrected. That is the single
highest-signal input it will ever receive about where its reading was weak.

---

## 4. Roles, actors, permissions

### 4.1 Roles

| Role | Scope |
|---|---|
| `admin` | Everything, plus user management, model config, deployment settings |
| `manager` | All manager-owned fields on all items in their projects; approves proposals; project settings |
| `developer` | Dev-owned fields on items assigned to them; own subtasks; worklogs; may approve proposals confined entirely to their own dev-owned fields |

Roles are assigned globally (`users.role`) and may be overridden per project
(`project_members.role`). The effective role is the project override if present,
else the global role.

**Actors.** Every write carries an `actor_id`. An actor is a human user, an
agent, or the system. Agents are real principals (D11): an agent acting on a
developer's instruction has the developer's effective permissions **and no
more**, and the audit entry records both the agent and the instructing user.

### 4.2 Field ownership (D7)

This table is the enforcement mechanism. It is checked mechanically on every
write, by field, before the write lands.

| Object | Manager-owned | Dev-owned |
|---|---|---|
| Milestone | name, intent, start_date, due_date, status, seq | — |
| WorkItem | title, intent, acceptance_criteria, priority, start_date, due_date, assignee, milestone_id, dependencies, entity refs | status, progress_pct |
| SubTask | — | title, status, assignee, seq (within own item) |
| ChecklistItem | — | text, checked (within own item) |
| Worklog | — | body, kind (author only) |
| Deviation | resolution, resolved_at | summary, severity (reporter) |
| Diagram (semantic) | all | — |
| Project settings | all | — |

**The rule that follows:**

- An agent writing a **dev-owned** field on an item assigned to the requesting
  user → **direct write**, provenance `agent_committed`, audited.
- An agent writing **anything else** → **Proposal**, never a direct write.
- A human writing a field they do not own → `403`, with the owning role named.

**Creating and deleting** a Milestone or WorkItem is manager-owned. A developer
creating a SubTask under their own assigned item is dev-owned.

---

## 5. Governance

### 5.1 Proposal

```
pending → approved | rejected | superseded
```

A Proposal carries a title, a rationale, and an ordered list of typed changes:

```
ProposalChange { seq, op: create|update|delete, target_kind, target_id?, before?, after }
```

Applied **atomically as one revision**. If any change conflicts with the current
state (the target moved since the proposal was raised), the whole proposal is
rejected as `superseded` and the conflict is reported — never partially applied,
never auto-merged.

Approval authority: `admin` and `manager` for anything; `developer` only for a
proposal whose every change is confined to their own dev-owned fields.

### 5.2 Rejection rationale is first-class

A rejected proposal **requires** a rationale, and rejected proposals with their
rationales are fed into assembled context (§6.3).

Without this the agent re-proposes what was already refused, and the human
concludes the tool does not listen. This is the `dead_ends` field of
LumixAgents' compaction summary, in a durable form: the highest-value record is
what was tried and why it was rejected.

### 5.3 Turn serialization

One agent turn per project at a time, held by a lease. A second user's prompt
while a turn is running is **queued and shown as queued**, never run
concurrently — two turns writing to the same plan produce interleaved changes
with no coherent audit story.

Turn leases expire on heartbeat failure. An expired turn is marked `abandoned`
and its partial output is retained as transcript but applies nothing.

### 5.4 Re-evaluate

The user may ask the vision model to re-examine a diagram they have hand-edited.
**It returns a Proposal, never a write** — rendered as a diff on the canvas with
per-change accept/reject.

A stray click must not silently rewrite a plan a human corrected by hand.

---

## 6. Data and context

### 6.1 Schema

Postgres 16+, pgvector. `snake_case`, `TIMESTAMPTZ`, UUIDv7 primary keys
(sortable, index-friendly). Large text stays in the DB; uploaded images go to
disk under `data/uploads/<project>/`, referenced by path.

```sql
-- ── identity ─────────────────────────────────────────────
CREATE TABLE users (
  id UUID PRIMARY KEY, email TEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL, password_hash TEXT NOT NULL,
  role TEXT NOT NULL,                       -- admin | manager | developer
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE actors (
  id UUID PRIMARY KEY,
  kind TEXT NOT NULL,                       -- human | agent | system
  user_id UUID REFERENCES users(id),        -- null for agent/system
  agent_name TEXT, model_ref TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE projects (
  id UUID PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
  description TEXT, status TEXT NOT NULL, settings JSONB NOT NULL DEFAULT '{}',
  git_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE project_members (
  project_id UUID NOT NULL REFERENCES projects(id),
  user_id UUID NOT NULL REFERENCES users(id),
  role TEXT NOT NULL, PRIMARY KEY (project_id, user_id)
);

-- ── diagram: semantic ────────────────────────────────────
CREATE TABLE catalog_entities (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  kind TEXT NOT NULL, name TEXT NOT NULL, slug TEXT NOT NULL,
  summary TEXT, attrs JSONB NOT NULL DEFAULT '{}',
  provenance TEXT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (project_id, slug)
);

CREATE TABLE diagrams (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  name TEXT NOT NULL, level TEXT NOT NULL,          -- context|container|component
  layout_mode TEXT NOT NULL DEFAULT 'auto',         -- auto|manual
  current_revision INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE diagram_nodes (
  id UUID PRIMARY KEY, diagram_id UUID NOT NULL REFERENCES diagrams(id),
  entity_id UUID NOT NULL REFERENCES catalog_entities(id),
  label_override TEXT, attrs JSONB NOT NULL DEFAULT '{}',
  parent_node_id UUID REFERENCES diagram_nodes(id),  -- boundary containment
  provenance TEXT NOT NULL, confidence REAL,
  actor_id UUID NOT NULL REFERENCES actors(id),
  origin_revision INT NOT NULL, deleted_at TIMESTAMPTZ,
  UNIQUE (diagram_id, entity_id)
);

CREATE TABLE diagram_edges (
  id UUID PRIMARY KEY, diagram_id UUID NOT NULL REFERENCES diagrams(id),
  from_node_id UUID NOT NULL REFERENCES diagram_nodes(id),
  to_node_id UUID NOT NULL REFERENCES diagram_nodes(id),
  relation TEXT NOT NULL, label TEXT, attrs JSONB NOT NULL DEFAULT '{}',
  provenance TEXT NOT NULL, confidence REAL,
  actor_id UUID NOT NULL REFERENCES actors(id),
  origin_revision INT NOT NULL, deleted_at TIMESTAMPTZ,
  UNIQUE (diagram_id, from_node_id, to_node_id, relation)
);

-- ── diagram: presentation (never read by the serializer, D5) ──
CREATE TABLE diagram_layout (
  diagram_id UUID NOT NULL REFERENCES diagrams(id),
  node_id UUID NOT NULL REFERENCES diagram_nodes(id),
  x DOUBLE PRECISION, y DOUBLE PRECISION,
  w DOUBLE PRECISION, h DOUBLE PRECISION,
  collapsed BOOLEAN NOT NULL DEFAULT FALSE,
  unplaced BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (diagram_id, node_id)
);

CREATE TABLE diagram_edge_layout (
  diagram_id UUID NOT NULL REFERENCES diagrams(id),
  edge_id UUID NOT NULL REFERENCES diagram_edges(id),
  waypoints JSONB NOT NULL DEFAULT '[]',
  PRIMARY KEY (diagram_id, edge_id)
);

CREATE TABLE diagram_revisions (
  id UUID PRIMARY KEY, diagram_id UUID NOT NULL REFERENCES diagrams(id),
  seq INT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  summary TEXT, changes JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (diagram_id, seq)
);

CREATE TABLE diagram_leases (
  diagram_id UUID PRIMARY KEY REFERENCES diagrams(id),
  actor_id UUID NOT NULL REFERENCES actors(id),
  base_revision INT NOT NULL,
  acquired_at TIMESTAMPTZ NOT NULL, heartbeat_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL
);

-- ── ingest ───────────────────────────────────────────────
CREATE TABLE ingests (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  status TEXT NOT NULL,          -- uploaded|extracting|merging|review|accepted|failed
  source_kind TEXT NOT NULL,     -- image|pdf
  diagram_id UUID REFERENCES diagrams(id),
  actor_id UUID NOT NULL REFERENCES actors(id),
  error_class TEXT, error_detail TEXT,
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE ingest_pages (
  id UUID PRIMARY KEY, ingest_id UUID NOT NULL REFERENCES ingests(id),
  page_idx INT NOT NULL, image_path TEXT NOT NULL,
  status TEXT NOT NULL, extracted JSONB, model TEXT,
  tokens_in INT, tokens_out INT, error_class TEXT
);

-- ── plan ─────────────────────────────────────────────────
CREATE TABLE milestones (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  name TEXT NOT NULL, intent TEXT, seq INT NOT NULL,
  start_date DATE, due_date DATE, status TEXT NOT NULL,
  provenance TEXT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE work_items (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  milestone_id UUID REFERENCES milestones(id),
  ref TEXT NOT NULL,                          -- human-facing, e.g. "ZP-14"
  title TEXT NOT NULL, intent TEXT,
  acceptance_criteria TEXT,
  status TEXT NOT NULL,                       -- todo|in_progress|blocked|review|done|dropped
  progress_pct INT NOT NULL DEFAULT 0,
  priority TEXT, assignee_id UUID REFERENCES users(id),
  start_date DATE, due_date DATE,
  provenance TEXT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (project_id, ref)
);

CREATE TABLE sub_tasks (
  id UUID PRIMARY KEY, work_item_id UUID NOT NULL REFERENCES work_items(id),
  title TEXT NOT NULL, status TEXT NOT NULL, seq INT NOT NULL,
  assignee_id UUID REFERENCES users(id),
  provenance TEXT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE item_dependencies (
  from_item_id UUID NOT NULL REFERENCES work_items(id),
  to_item_id UUID NOT NULL REFERENCES work_items(id),
  kind TEXT NOT NULL,                         -- blocks | relates
  actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (from_item_id, to_item_id, kind)
);

CREATE TABLE item_entity_refs (
  work_item_id UUID NOT NULL REFERENCES work_items(id),
  entity_id UUID NOT NULL REFERENCES catalog_entities(id),
  relation TEXT NOT NULL DEFAULT 'implements',
  actor_id UUID NOT NULL REFERENCES actors(id),
  PRIMARY KEY (work_item_id, entity_id, relation)
);

CREATE TABLE checklist_items (
  id UUID PRIMARY KEY, work_item_id UUID NOT NULL REFERENCES work_items(id),
  text TEXT NOT NULL, checked BOOLEAN NOT NULL DEFAULT FALSE,
  checked_by UUID REFERENCES actors(id), checked_at TIMESTAMPTZ, seq INT NOT NULL
);

CREATE TABLE worklogs (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  work_item_id UUID REFERENCES work_items(id),
  actor_id UUID NOT NULL REFERENCES actors(id),
  kind TEXT NOT NULL,                         -- progress | deviation | note
  body TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE deviations (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  work_item_id UUID REFERENCES work_items(id),
  worklog_id UUID REFERENCES worklogs(id),
  summary TEXT NOT NULL, severity TEXT NOT NULL,   -- low|medium|high
  resolution TEXT, resolved_at TIMESTAMPTZ,
  actor_id UUID NOT NULL REFERENCES actors(id), created_at TIMESTAMPTZ NOT NULL
);

-- ── conversation ─────────────────────────────────────────
CREATE TABLE threads (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  scope_kind TEXT NOT NULL,                   -- project | work_item | diagram
  scope_id UUID, created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (project_id, scope_kind, scope_id)
);

CREATE TABLE messages (
  id UUID PRIMARY KEY, thread_id UUID NOT NULL REFERENCES threads(id),
  actor_id UUID NOT NULL REFERENCES actors(id),
  role TEXT NOT NULL,                         -- user | assistant | tool | system
  content TEXT NOT NULL, tool_calls JSONB,
  turn_id UUID, seq BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (thread_id, seq)
);

CREATE TABLE agent_turns (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  thread_id UUID NOT NULL REFERENCES threads(id),
  requested_by UUID NOT NULL REFERENCES users(id),
  status TEXT NOT NULL,                       -- queued|running|complete|failed|abandoned
  model TEXT, tokens_in INT, tokens_out INT, turns INT,
  error_class TEXT, heartbeat_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ, ended_at TIMESTAMPTZ
);

-- ── governance ───────────────────────────────────────────
CREATE TABLE proposals (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  actor_id UUID NOT NULL REFERENCES actors(id), turn_id UUID,
  title TEXT NOT NULL, rationale TEXT NOT NULL,
  target_kind TEXT NOT NULL,                  -- diagram | plan | mixed
  status TEXT NOT NULL,                       -- pending|approved|rejected|superseded
  decided_by UUID REFERENCES users(id), decided_at TIMESTAMPTZ,
  decision_rationale TEXT, applied_revision INT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE proposal_changes (
  id UUID PRIMARY KEY, proposal_id UUID NOT NULL REFERENCES proposals(id),
  seq INT NOT NULL, op TEXT NOT NULL,         -- create|update|delete
  target_kind TEXT NOT NULL, target_id UUID,
  before JSONB, after JSONB,
  UNIQUE (proposal_id, seq)
);

CREATE TABLE notes (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  subject_kind TEXT NOT NULL, subject_id UUID,
  kind TEXT NOT NULL,                         -- overlap|conflict|missing|clarification
  body TEXT NOT NULL, status TEXT NOT NULL,   -- open|resolved|dismissed
  provenance TEXT NOT NULL, actor_id UUID NOT NULL REFERENCES actors(id),
  created_at TIMESTAMPTZ NOT NULL, resolved_at TIMESTAMPTZ
);

-- ── context and audit ────────────────────────────────────
CREATE TABLE project_digest (
  project_id UUID PRIMARY KEY REFERENCES projects(id),
  revision INT NOT NULL,
  harness_fields JSONB NOT NULL,              -- computed, never model-written
  model_fields JSONB NOT NULL,                -- hypothesis, dead_ends, open_questions
  model_fields_stale BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE chunks (
  id UUID PRIMARY KEY, project_id UUID NOT NULL REFERENCES projects(id),
  source_kind TEXT NOT NULL,                  -- worklog|message|document|note
  source_id UUID NOT NULL, chunk_idx INT NOT NULL,
  content TEXT NOT NULL, embedding VECTOR(1024),
  model TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (source_kind, source_id, chunk_idx)
);

CREATE TABLE audit_log (
  id UUID PRIMARY KEY, project_id UUID REFERENCES projects(id),
  actor_id UUID NOT NULL REFERENCES actors(id),
  on_behalf_of UUID REFERENCES users(id),     -- set when an agent acts for a user
  action TEXT NOT NULL, subject_kind TEXT NOT NULL, subject_id UUID,
  before JSONB, after JSONB,
  request_id TEXT NOT NULL,                   -- correlates one burst of writes
  at TIMESTAMPTZ NOT NULL
);

-- export indexes. Present from the first migration (D13)
CREATE INDEX audit_project_at   ON audit_log (project_id, at);
CREATE INDEX audit_subject_at   ON audit_log (subject_kind, subject_id, at);
CREATE INDEX audit_actor_at     ON audit_log (actor_id, at);
CREATE INDEX audit_action_at    ON audit_log (project_id, action, at);
CREATE INDEX audit_request      ON audit_log (request_id);
```

### 6.5 Audit permanence (D13)

The CSV export is v0.1. The **record** is v0, and it carries five constraints
that cannot be retrofitted:

**1. No hard deletes, anywhere.** Not a happy accident of the current API — an
invariant. A hard delete removes the row *and* orphans every audit entry
pointing at it, which is the one failure that makes a retrospective unanswerable.
Every object with a lifecycle carries `deleted_at` and is filtered in queries:
`milestones`, `work_items`, `sub_tasks`, `checklist_items`, `deviations`,
`catalog_entities`, `diagram_nodes`, `diagram_edges`, `notes`. Worklogs and
messages are never deleted at all; correct by adding.

**2. State transitions are their own action.** `work_item.status_changed`, not a
generic `work_item.updated` whose reader has to diff two JSONB blobs to discover
what moved. Bottleneck analysis is almost entirely transition analysis — how
long an item sat in `blocked`, how often it bounced between `review` and
`in_progress` — and making the transition first-class means the v0.1 export is a
`SELECT` rather than a parser. Same for `milestone.status_changed`,
`proposal.decided`, `deviation.resolved`, `checklist.checked`,
`diagram.revised`, `ingest.accepted`, and `turn.ended`.

**3. Every entry carries a `request_id`, populated.** It is what distinguishes
*an agent applied a 14-change proposal in one operation* from *someone made 14
edits over an afternoon*. Non-null in the schema so it cannot be quietly skipped.

**4. Durations are derived, never stored.** Given complete transitions with
timestamps, "how long was ZP-14 blocked" is a window function over the log. A
stored duration field is a second source of truth that will disagree.

**5. Retention is: never.** The log is not pruned, archived, or rotated. Written
here so a later cleanup job to keep the table small is recognisable as a
regression rather than housekeeping.

**`audit_log` is append-only.** No `UPDATE`, no `DELETE`, enforced by a trigger.
Every mutating handler writes exactly one entry per logical operation, inside
the same transaction as the write.

**What v0.1 adds** is the export shape, which is a design question best answered
with real data in front of it. The likely answer is several exports rather than
one — transitions, worklogs and deviations, turns and cost, and a raw event dump
— because flattening JSONB `before`/`after` into a single CSV is lossy in
precisely the ways that matter. **[DEFERRED to v0.1]**

### 6.2 Canonical semantic serialization

One serializer, two consumers: the model context (§6.3) and the optional git
projection (D10). Same bytes.

**Requirements:**

- **Deterministic.** The same graph state produces byte-identical output. Sorted
  keys, fixed field order, stable node ordering (by kind, then slug), normalized
  numbers. This is what makes an empty diff mean *nothing changed* rather than
  *the marshaller felt different today*.
- **YAML, not JSON.** Comments and multiline prose diff readably; the planner
  model reads it directly.
- **No coordinates.** Structurally impossible, not filtered (D5).
- **Stable identity in paths.** For the git projection: `nodes/<entity_slug>.yaml`,
  never a path derived from a display label. A rename must not look like a delete
  plus an add.
- **One file per node, one per edge** in the git projection, keyed by
  `(from, to, relation)`. A single big document makes every diff useless.

Shape:

```yaml
diagram: auth-context
level: context
nodes:
  - id: auth-service
    kind: service
    name: Auth Service
    summary: Issues and validates session tokens
    attrs:
      language: go
  - id: token-store
    kind: datastore
    name: Token Store
edges:
  - from: auth-service
    to: token-store
    relation: writes_to
    label: session rows
notes:
  - kind: overlap
    subject: token-store
    body: >
      May be the same store as "session-db" in the container diagram.
      Unresolved.
```

### 6.3 Context assembly

Three layers, assembled deterministically per turn. **Never a replay of chat**
(D8).

**Layer 1 — Skeleton (always present).** Project meta; active milestone; open
work items with status and assignee; open deviations; recent accepted decisions;
**recent rejected proposals with their rationales** (§5.2); open notes.

**Layer 2 — Graph expansion (query, not search).** From whatever the message
references: the work item and its dependencies both ways, its milestone, its
referenced entities, and the diagram slice around those entities (nodes plus
one hop). Recursive CTEs. This is the part a vector store cannot do.

**Layer 3 — Vector spill (pgvector).** Only over unstructured text: worklogs,
past messages, imported documents. Always filtered by project and usually by
date or item. Structured objects are never chunked — notes especially (§3.4).

**Digest.** `project_digest` is maintained incrementally on every accepted
change. Harness-computed fields (open items, blocked items, recent decisions,
deviation counts) are recomputed from the DB and **cannot drift**. Model-written
fields (`current_hypothesis`, `dead_ends`, `open_questions`) are refreshed on
demand and marked stale when the graph moves.

The split is the point. In LumixAgents Phase 4, a model asked to summarise
produced `dead_ends` describing events that never happened, reconstructed from
its own role prompt. Harness-owned fields cannot do that. Model-owned fields are
a hint, not a record, and the UI must not present them as fact.

### 6.4 Interfaces held open

```go
type VectorStore interface {
    Upsert(ctx context.Context, chunks []Chunk) error
    Search(ctx context.Context, q Query) ([]Hit, error)
    DeleteBySource(ctx context.Context, kind string, id uuid.UUID) error
}
```

pgvector is the only implementation in v0 (D1). The interface exists so a Milvus
implementation is a package, not a refactor. No caller may reach past it into
SQL for similarity.

---

## 7. API surface

REST over HTTP, JSON, `/api/v1`. Session cookie auth (`HttpOnly`, `SameSite=Lax`).
**OpenAPI 3.1 is emitted by the backend and is authoritative for shapes; the
frontend's TypeScript client is generated from it and never hand-written.**

### 7.1 Endpoints

```
POST   /api/v1/auth/login                       → session
POST   /api/v1/auth/logout
GET    /api/v1/auth/me                          → user + effective roles

GET    /api/v1/projects
POST   /api/v1/projects                         (admin)
GET    /api/v1/projects/{pid}
PATCH  /api/v1/projects/{pid}                   (manager+)
GET    /api/v1/projects/{pid}/members
PUT    /api/v1/projects/{pid}/members/{uid}     (admin)

GET    /api/v1/projects/{pid}/entities
GET    /api/v1/entities/{id}                    → entity + placements + refs

GET    /api/v1/projects/{pid}/diagrams
POST   /api/v1/projects/{pid}/diagrams
GET    /api/v1/diagrams/{id}                    → semantic + layout + revision
GET    /api/v1/diagrams/{id}/yaml                → canonical serialization
POST   /api/v1/diagrams/{id}/lease              → acquire (409 if held)
POST   /api/v1/diagrams/{id}/lease/heartbeat
DELETE /api/v1/diagrams/{id}/lease
POST   /api/v1/diagrams/{id}/revisions          → atomic save; 409 on stale base
PUT    /api/v1/diagrams/{id}/layout             → presentation only, no lease needed
GET    /api/v1/diagrams/{id}/revisions
POST   /api/v1/diagrams/{id}/reevaluate         → creates a Proposal

POST   /api/v1/projects/{pid}/ingests           multipart: images or pdf
GET    /api/v1/ingests/{id}                     → status + per-page extraction
POST   /api/v1/ingests/{id}/accept              → creates or updates a Diagram

GET    /api/v1/projects/{pid}/milestones
POST   /api/v1/projects/{pid}/milestones
PATCH  /api/v1/milestones/{id}
GET    /api/v1/projects/{pid}/work-items        ?assignee=&status=&milestone=
POST   /api/v1/projects/{pid}/work-items
GET    /api/v1/work-items/{id}
PATCH  /api/v1/work-items/{id}                  → field ownership enforced (§4.2)
POST   /api/v1/work-items/{id}/sub-tasks
PATCH  /api/v1/sub-tasks/{id}
PUT    /api/v1/work-items/{id}/dependencies
PUT    /api/v1/work-items/{id}/entity-refs
POST   /api/v1/work-items/{id}/checklist
PATCH  /api/v1/checklist/{id}
POST   /api/v1/work-items/{id}/worklogs
GET    /api/v1/projects/{pid}/worklogs          ?since=&item=&kind=
POST   /api/v1/projects/{pid}/deviations
PATCH  /api/v1/deviations/{id}
GET    /api/v1/projects/{pid}/warnings          → dependency/date violations

GET    /api/v1/projects/{pid}/threads
GET    /api/v1/threads/{id}/messages            ?before=&limit=
POST   /api/v1/threads/{id}/messages            → enqueues a turn
GET    /api/v1/projects/{pid}/events            → SSE (§7.2)
POST   /api/v1/turns/{id}/cancel

GET    /api/v1/projects/{pid}/proposals         ?status=
GET    /api/v1/proposals/{id}                   → changes + current conflicts
POST   /api/v1/proposals/{id}/approve
POST   /api/v1/proposals/{id}/reject            body: { rationale } — required

GET    /api/v1/projects/{pid}/notes             ?status=
PATCH  /api/v1/notes/{id}

GET    /api/v1/projects/{pid}/digest
POST   /api/v1/work-items/{id}/spec-export      → spec.md (manual only)
POST   /api/v1/milestones/{id}/spec-export

GET    /api/v1/projects/{pid}/audit             ?subject=&actor=&since=
GET    /api/v1/admin/models                     (admin) → registry + live status
GET    /api/v1/admin/users                      (admin)
```

### 7.2 SSE events

One stream per project: `GET /api/v1/projects/{pid}/events`. Every event carries
`{ id, type, project_id, at, payload }`.

| Type | Payload | Frontend must |
|---|---|---|
| `turn.queued` | turn, position | show queue state |
| `turn.started` | turn, requested_by | show who, disable send |
| `message.delta` | turn_id, message_id, text | append streaming text |
| `message.complete` | message | finalise |
| `turn.ended` | turn, status, error_class? | re-enable send |
| `proposal.created` | proposal summary | surface in review queue |
| `proposal.decided` | proposal, status | update queue and canvas |
| `plan.changed` | subject_kind, subject_id, revision | refetch affected view |
| `diagram.revised` | diagram_id, revision, actor | refetch or warn if editing |
| `lease.lost` | diagram_id, reason | block save, offer diff |
| `ingest.progress` | ingest_id, page_idx, status | update progress |
| `note.created` | note | badge the subject |

**Reconnect:** `Last-Event-ID` is honoured; the backend replays from the last
event id where it can and otherwise emits `resync` and the client refetches.

### 7.3 Errors

One shape, always:

```json
{
  "error": {
    "code": "field_not_owned",
    "message": "start_date is manager-owned. Your effective role on this project is developer.",
    "details": { "field": "start_date", "owning_role": "manager" },
    "request_id": "01J..."
  }
}
```

Codes: `unauthenticated`, `forbidden`, `field_not_owned`, `not_found`,
`conflict`, `lease_held`, `stale_base_revision`, `validation_failed`,
`turn_in_progress`, `model_unavailable`, `context_overflow`, `internal`.

**A denial's wording is a prompt layer.** LumixAgents D34 measured this: a
refusal that leads with the reason gets prose; a refusal that leads with the
action gets the action. Every `403` names the owning role **and** the available
route ("raise a proposal"), whether the caller is a human or an agent.

---

## 8. Model configuration

`~/.zettaplanner/models.yaml`, machine-scoped, never committed.

```yaml
models:
  planner-local:
    endpoint: http://localhost:11434
    model: qwen3.6:35b
    num_ctx: 262144            # MUST be explicit
    caps: [tools]
    concurrency: 1
  vision-local:
    endpoint: http://localhost:11434
    model: gemma4:31b
    num_ctx: 262144
    caps: [vision]
    concurrency: 1
  embed-local:
    endpoint: http://localhost:11434
    model: bge-m3
    caps: [embedding]
    dim: 1024
  planner-cloud:
    endpoint: https://ollama.com
    model: glm-5.2:cloud
    auth_env: OLLAMA_API_KEY
    caps: [tools, long_context]
    concurrency: 4

roles:
  planner: { requires: [tools],     prefer: [planner-local, planner-cloud] }
  vision:  { requires: [vision],    prefer: [vision-local] }
  embed:   { requires: [embedding], prefer: [embed-local] }
```

**Startup validation is mandatory and fails loudly.** For each configured model:
`/api/show` for capability and maximum window; a live one-token completion; and
**`/api/ps` for the actually-allocated `context_length`**, which is the only
check that proves the window took effect. Embedding models additionally assert
that the returned vector length equals the configured `dim` and matches the
`chunks.embedding` column — a silent dimension change corrupts every subsequent
retrieval.

Every request sets `options.num_ctx` explicitly and a `keep_alive` long enough
to survive a turn.

**Changing the embedding model requires re-embedding the corpus.** The `chunks`
table records `model` per row; a mismatch at query time is an error, never a
silent mixed-space search.

---

## 9. Vocabulary

| Term | Means |
|---|---|
| **Entity** | A project-level named architectural thing. Identity |
| **Node** | A placement of an entity in a diagram |
| **Revision** | One atomic diagram save. Monotonic per diagram |
| **Turn** | One agent invocation, serialized per project |
| **Proposal** | A set of changes awaiting approval |
| **Digest** | The maintained project state summary fed into context |
| **Spill** | Unstructured text eligible for vector search |
| **Owned field** | A field whose write requires a specific role (§4.2) |

---

## 10. Deferred

Sequence diagrams (v0.1) · Lumix integration and automated spec handoff (v0.1) ·
**audit CSV export (v0.1 — the record is v0, see §6.5 and D13)** ·
C++/libtorch inference (D12) · Milvus · auto-scheduling and critical path ·
OIDC/SSO · notifications and email · multi-tenancy · mobile · offline editing ·
CRDT co-editing · ZettaCAMS hooks.