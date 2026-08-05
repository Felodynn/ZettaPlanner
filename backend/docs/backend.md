# ZettaPlanner — Backend Build Plan

**Status:** Draft v1
**Owner:** Solo technical founder (Felodynn)
**Implementation partner:** Claude Code (sequential, phase-gated)
**Required reading:** `CONTRACT.md` in full, plus this document's §1–§3. Read
one phase at a time. Feeding the whole document in as context invites premature
abstraction.

---

## 0. How to use this document

1. Build **one phase at a time**. Do not start phase N+1 until phase N's exit
   criteria pass.
2. Every phase has a **manual test procedure**. Run it by hand as a human before
   automating anything on top of it. Every phase is also testable **without a
   frontend** — the frontend is built afterwards, against a working backend.
3. `CONTRACT.md` is authoritative for anything crossing the API boundary. If
   this document and the contract disagree, the contract wins and this document
   is wrong — fix it here, then in code.
4. **[DEFERRED]** means do not build it early.
5. Append `Dn` decision entries per phase, including the honest ones. A
   measurement that failed to separate two options is worth more written down
   than quietly discarded.

---

## 1. Shape

```
   ┌────────────────────────────────────────────────────────────┐
   │  zettaplanner (single binary, embedded SPA)                │
   │                                                            │
   │  ┌──────────┐  ┌───────────┐  ┌──────────────────────┐     │
   │  │ HTTP API │  │ SSE Hub   │  │ OpenAPI emitter      │     │
   │  └────┬─────┘  └─────┬─────┘  └──────────────────────┘     │
   │       │              │                                     │
   │  ┌────▼──────────────▼──────────────────────────────┐      │
   │  │  Application services                            │      │
   │  │  auth · project · diagram · ingest · plan ·      │      │
   │  │  thread · proposal · export                      │      │
   │  └────┬───────────────┬──────────────┬──────────────┘      │
   │       │               │              │                     │
   │  ┌────▼────┐  ┌───────▼───────┐  ┌───▼─────────────┐       │
   │  │ Policy  │  │ Agent runtime │  │ Context builder │       │
   │  │ (§4.2)  │  │ turn · tools  │  │ skeleton/graph/ │       │
   │  └────┬────┘  └───────┬───────┘  │ spill · digest  │       │
   │       │               │          └───┬─────────────┘       │
   │  ┌────▼───────────────▼──────────────▼──────────────┐      │
   │  │  Repositories (pgx) · Serializer · VectorStore   │      │
   │  └────┬─────────────────────────────────┬───────────┘      │
   │       │                                 │                  │
   │  ┌────▼───────────┐              ┌──────▼──────────┐       │
   │  │ PostgreSQL     │              │ Ollama client   │       │
   │  │ + pgvector     │              │ /api/chat,show, │       │
   │  └────────────────┘              │ ps, embeddings  │       │
   │                                  └─────────────────┘       │
   │  ┌───────────────────────────────────────────────┐         │
   │  │ Git projector (optional, one-way, D10)        │         │
   │  └───────────────────────────────────────────────┘         │
   └────────────────────────────────────────────────────────────┘
```

### 1.1 Layout

Standard Go project layout. Nothing exported that does not need to be.

```
cmd/zettaplanner/          main, flags, embedded SPA
internal/
  config/                  models.yaml, server config, validation
  store/                   pgx pool, migrations, tx helper
    migrations/            NNNN_name.up.sql / .down.sql
  domain/                  pure types + invariants, no I/O
    diagram/  plan/  proposal/  thread/  note/
  repo/                    one file per aggregate, SQL only
  policy/                  roles, field ownership, denial text
  service/                 orchestration; owns transactions
    auth/ project/ diagram/ ingest/ plan/ thread/ proposal/ export/
  agent/                   loop, tools, turn lease, streaming
    tools/                 one file per tool
  contextbuild/            skeleton, expansion, spill, digest
  llm/                     ollama client, registry, error normalisation
  serialize/               canonical YAML (§6.2). Imports no layout package
  gitproj/                 optional one-way projection
  httpapi/                 handlers, middleware, OpenAPI, SSE hub
  audit/                   append-only writer
  telemetry/               slog setup, request ids
web/                       React app (FRONTEND.md); built and embedded
```

**One structural rule, enforced by a test:** `internal/serialize` and
`internal/contextbuild` must not import any package that reads
`diagram_layout`. This is how D5 stops being a good intention. Write the import
test in Phase 2 and never delete it.

### 1.2 Conventions

- `context.Context` first argument everywhere; request-scoped `request_id` in
  every log line and every audit row.
- **Transactions are owned by the service layer**, never by repos. One logical
  operation = one transaction = one audit entry.
- Repos take a `Querier` interface satisfied by both pool and tx.
- No ORM. `pgx` with hand-written SQL. Keep the schema portable and legible.
- Errors: typed sentinel errors in domain, mapped to contract codes (§7.3) in
  exactly one place in `httpapi`.
- Tests: table-driven, real Postgres via testcontainers or a disposable local
  DB. **No mocking the database.** The whole product is the schema.

---

## 2. Settled decisions

See `CONTRACT.md` §2 for D1–D12 (product-level). Backend-specific decisions are
appended as `Dn` from D20 in the phases below.

---

## 3. Phased build plan

Every phase: **implement → manual test → exit criteria → commit → next**.

> **Phases 0–8 run on local Qwen3.5:9b / qwen3.6:35b and gemma4:31b.**
>
> **Do not judge ZettaPlanner by the model's output quality in these phases.**
> Weak models produce weak plans; that is expected and irrelevant here. Judge
> only: does the tool fire, is the policy enforced, is the audit row written, is
> the proposal well-formed, does the turn terminate, does the context assemble
> deterministically. You are testing the harness.
>
> A weak local model is the better test subject — it surfaces every harness
> weakness a strong one papers over.

---

### Phase 0 — Foundations

**Build:** config loading; `models.yaml` registry with mandatory startup
validation (`/api/show`, live one-token completion, `/api/ps` allocated context,
embedding dimension assertion); Ollama client for `/api/chat`, `/api/embed`;
error normalisation; Postgres pool; migration runner; the full schema from
`CONTRACT.md` §6.1 **including the audit export indexes**; append-only audit
table with its trigger; the audit action taxonomy as a typed enum; structured
logging with request ids; health endpoint; OpenAPI document emission.

**Notes for the implementer:**

- Migrations must **take a write lock before reading the version**. Two
  processes opening the same fresh database will otherwise both see version 0,
  both run `0001`, and the second dies on a unique constraint. The whole
  sequence runs in one transaction that locks first.
- Error normalisation produces: `ErrContextOverflow`, `ErrToolParse`,
  `ErrRateLimit`, `ErrModelUnavailable`, `ErrTimeout`. Local and cloud Ollama
  return different shapes for the same conditions.
- **The audit export indexes ship in the first migration** (contract §6.5).
  Adding them a year later means building an index on a table you are trying to
  read, at the moment you most want the data.
- **`audit.Action` is a Go enum, not a string literal at each call site.**
  Transition actions (`work_item.status_changed`, `proposal.decided`,
  `deviation.resolved`, `checklist.checked`, `turn.ended`, …) are distinct
  constants from the generic `*.updated`. A handler that writes a raw string is
  a handler whose events cannot be found by the v0.1 export.
- **`request_id` is `NOT NULL`.** Generated in middleware, threaded through
  `context.Context`, written on every audit row. It is what separates one
  14-change proposal application from fourteen afternoon edits.

**Manual test:**
```bash
zettaplanner models check          # validates every model, prints ctx + caps
zettaplanner migrate up && zettaplanner migrate up   # idempotent
zettaplanner openapi > openapi.json
```
Deliberately set `num_ctx` above the model's maximum and confirm boot fails with
an actionable message. Change the embedding model to one with a different
dimension and confirm boot fails rather than silently corrupting `chunks`.

**Exit criteria:**
- [ ] All configured models validate; `/api/ps` allocated context matches config
- [ ] Embedding dimension asserted against the `chunks` column at boot
- [ ] Bad config fails at startup with a message naming the fix
- [ ] Migrations idempotent and safe under two concurrent processes
- [ ] `audit_log` rejects `UPDATE` and `DELETE`
- [ ] Export indexes present in the first migration
- [ ] `request_id` is `NOT NULL` and populated by middleware
- [ ] Audit actions are typed constants; transitions distinct from `*.updated`
- [ ] A comment in the schema states retention: never pruned, never rotated
- [ ] OpenAPI document emits and validates

---

### Phase 1 — Auth, users, projects

**Build:** local accounts with argon2id; session cookie (`HttpOnly`,
`SameSite=Lax`, rotation on login); users CRUD (admin); projects; project
members with per-project role override; the **policy package** — effective role
resolution and the field ownership table from `CONTRACT.md` §4.2 as data, not as
scattered `if` statements; actors table with human/agent/system principals;
audit middleware.

**Notes for the implementer:**

- **Field ownership is a table, checked mechanically.** A `PATCH` computes the
  set of fields being written, resolves each to its owning role, and rejects
  before any write. Scattering the check across handlers is how a field
  eventually gets missed.
- **Denial text is prompt text** (contract §7.3). Every `403` names the owning
  role *and* the available route. Lead with the action.
- Agents get actor rows at creation, not on first use, so an audit row can never
  reference a principal that does not exist.
- **No repository exposes a hard delete** (contract §6.5.1). `Delete` sets
  `deleted_at` and writes an audit entry; every read filters it. Establish this
  in the repo base helpers here, in Phase 1, while there are two tables — not in
  Phase 4 when there are ten and one of them has already grown a `DELETE FROM`.
- A logical operation touching several rows writes **one** audit entry with the
  shared `request_id`, not one per row. The distinction is what makes the v0.1
  transition export readable.

**Manual test:**
```bash
curl -X POST /api/v1/auth/login -d '{"email":"...","password":"..."}'
# as developer, attempt to PATCH a manager-owned field
curl -X PATCH /api/v1/work-items/{id} -d '{"due_date":"2026-09-01"}'
# EXPECT 403 field_not_owned, naming manager and naming "raise a proposal"
```

**Exit criteria:**
- [ ] Login, logout, session rotation, `me` with effective per-project role
- [ ] Field ownership enforced from the table; adding a field to the table
      changes behaviour with no handler change
- [ ] Every mutating request writes exactly one audit row, in the same tx
- [ ] Per-project role override beats the global role
- [ ] No hard-delete path exists in any repository; a test greps for `DELETE FROM`
      outside the migration runner and fails on a hit
- [ ] A `403` names both the owning role and the route forward

---

### Phase 2 — Diagram core ⚠️ highest risk

This is the phase that determines whether the rest of the product can reason.
Budget disproportionate time.

**Build:** catalog entities; diagrams; semantic nodes and edges with the closed
vocabulary (contract §3.3); **separate layout store**; revisions; leases with
heartbeat; atomic whole-diagram save with stale-base rejection; canonical YAML
serializer; the import-boundary test; notes; graph query surface (what depends
on X, across diagrams).

**Notes for the implementer:**

- **The save is atomic and whole-diagram.** The client sends the full semantic
  state plus its base revision. If `base_revision != current_revision`, reject
  `409 stale_base_revision` with the diff — never merge. One revision per human
  save is what makes history readable.
- **Layout writes take no lease and produce no revision.** Moving a box is not a
  semantic change; treating it as one floods the revision log and the git
  projection with noise.
- **`layout_mode` flips to `manual` on the first coordinate write and does not
  flip back** without an explicit destructive confirm.
- **Vocabulary is validated at write time** against the closed set. An unknown
  kind or relation is `validation_failed`, listing the valid values — that error
  text is read by the agent in Phase 6 and must teach.
- Entity resolution: on a name collision, **write a Note, do not merge**
  (contract §3.4).
- The serializer must be **byte-deterministic**. Test it by serializing, mutating
  nothing, serializing again, and asserting equality; then by building the same
  graph in a different insertion order and asserting the same bytes.

**Manual test:**
```bash
# create a diagram, add nodes/edges via API, then:
curl /api/v1/diagrams/{id}/yaml        # inspect by eye — is it readable?
grep -Ei '"x"|"y"|width|height' <(curl /api/v1/diagrams/{id}/yaml)   # EXPECT nothing
# two sessions:
curl -X POST /api/v1/diagrams/{id}/lease   # A: 200
curl -X POST /api/v1/diagrams/{id}/lease   # B: 409 lease_held, names holder
# stale save:
curl -X POST /api/v1/diagrams/{id}/revisions -d '{"base_revision":1,...}'  # after rev 2
```
Then break things deliberately: let a lease expire mid-edit and confirm the save
is rejected with a usable diff; write an invalid relation and read the error as
if you were the model.

**Exit criteria:**
- [ ] Semantic and layout stores separate; import-boundary test passes and fails
      when deliberately violated
- [ ] Serialized YAML contains no coordinates, by construction
- [ ] Serializer byte-deterministic across insertion orders
- [ ] Save is atomic; stale base rejected with a diff, never merged
- [ ] Lease acquired, heartbeated, expired; expiry blocks save
- [ ] Layout write produces no revision and needs no lease
- [ ] Closed vocabulary enforced; error names valid values
- [ ] Cross-diagram query answers "what depends on entity X"
- [ ] Name collision produces a Note, not a merge

---

### Phase 3 — Ingest (draw-to-product)

**Build:** multipart upload (images, PDF); PDF page rasterization; **per-page**
VLM extraction into the closed vocabulary with per-element confidence; a
**distinct merge step** reconciling cross-page references; ingest state machine;
review payload; accept → diagram creation or update as a Proposal.

**Notes for the implementer:**

- **One VLM call per page, never one call for the document.** Then a separate
  merge. The "continues on next page" arrows are precisely where extraction goes
  wrong, and merge is a second thing a human reviews rather than an
  implementation detail.
- **Extraction output is schema-constrained**, not asked-for-JSON-in-prose. Use
  Ollama's `format` field with the node/edge schema. In LumixAgents Phase 4 a
  model asked for JSON in prose left the content field empty and put everything
  in its reasoning channel, failing roughly half of all calls; constraining the
  format removed the failure mode rather than parsing around it.
- **Low confidence is a first-class value, not a guess.** An element the model
  could not read is stored with low confidence and surfaced for review. A
  verifier with only pass/fail picks wrong. Never invent a label to fill a box.
- Extraction never writes to a diagram directly. It produces a reviewable
  payload; accept applies it.
- Store the raw per-page model output in `ingest_pages.extracted` forever. When
  extraction is wrong you will want the input, and re-running costs tokens and
  is not reproducible.

**Manual test:** photograph a real hand-drawn HLD from a notebook — badly,
at an angle, with a page fold. Then a 3-page PDF with arrows crossing pages.
```bash
curl -X POST /api/v1/projects/{pid}/ingests -F 'files=@page1.jpg' -F 'files=@page2.jpg'
curl /api/v1/ingests/{id}          # per-page extraction with confidences
curl -X POST /api/v1/ingests/{id}/accept
curl /api/v1/diagrams/{did}/yaml   # read it: does it describe your drawing?
```

**Exit criteria:**
- [ ] Per-page extraction; merge is a separate reviewable step
- [ ] Every extracted element carries provenance `vlm_extracted` and a confidence
- [ ] Unreadable elements are marked low-confidence, never invented
- [ ] Extraction confined to the closed vocabulary
- [ ] Malformed model output fails the page, not the ingest; retry is per-page
- [ ] Raw per-page output retained
- [ ] A real notebook photograph produces a diagram you would correct rather than
      discard

---

### Phase 4 — Plan model

**Build:** milestones, work items with human refs, subtasks, dependencies with
cycle rejection, entity refs, checklists, worklogs, deviations; dependency and
date violation warnings; per-assignee and per-project queries.

**Notes for the implementer:**

- **Cycle rejection at write time**, on `blocks` only. Depth-limited recursive
  CTE. A cycle that lands in the DB will be found later by an agent that
  traverses it forever.
- Warnings are **computed, never stored**. A stored warning is stale the moment
  a date moves.
- No auto-scheduling. Moving a blocker's date produces a warning, never a
  cascade. A cascade that is right 90% of the time silently destroys hand-set
  dates.
- Worklogs are append-only in practice: correct by adding, not editing. That is
  what makes the log evidence rather than a current-state field.
- **Every status change writes `*.status_changed`** with `before`/`after` — on
  work items, milestones, subtasks, and deviation resolution. This is the phase
  where the retrospective data is actually generated, and a transition recorded
  as a generic update is a transition the v0.1 export cannot see. Same for
  `checklist.checked`, including the un-check: a box ticked and then cleared is a
  signal, and recording only the tick hides it.
- **Durations are never stored.** No `time_in_status`, no `blocked_since`. Given
  complete transitions, a window function answers it; a stored field is a second
  source of truth that will disagree with the log.
- Deletion is `deleted_at` plus a `*.deleted` audit entry. Dropping a work item
  must leave its full history queryable.

**Manual test:**
```bash
# build a real milestone for ZettaPlanner itself, with real dependencies
curl -X PUT /api/v1/work-items/{a}/dependencies -d '{"blocks":["{b}"]}'
curl -X PUT /api/v1/work-items/{b}/dependencies -d '{"blocks":["{a}"]}'  # EXPECT 400
curl /api/v1/projects/{pid}/warnings
# as a developer, PATCH status on your own item (200) and on another's (403)
```

**Exit criteria:**
- [ ] Full plan CRUD with field ownership enforced per field
- [ ] Dependency cycles rejected with the offending path named
- [ ] Warnings computed live for blocker-date and milestone-date violations
- [ ] Developer can write dev-owned fields on assigned items only
- [ ] Entity refs are set explicitly and never derived
- [ ] Every status change and checklist tick recorded as a transition action
- [ ] Soft delete only; a deleted item's history stays queryable
- [ ] `audit_log` reconstructs the full history of one work item — and a single
      SQL query answers "how long did this item spend in each status", with no
      stored duration field anywhere

---

### Phase 5 — Context assembly

Build this **before** the agent. An agent built first will be debugged against a
moving context, and you will not know which one is wrong.

**Build:** skeleton assembly; graph expansion via recursive CTEs; chunking and
embedding of spill only; pgvector search behind `VectorStore`; the digest with
its harness/model field split; a `context show` command that prints exactly what
a turn would receive.

**Notes for the implementer:**

- **`zettaplanner context show --project X --message "..."` is the most valuable
  debugging tool in this build.** It prints the assembled context with a token
  count per layer. Every "why did the agent say that" question starts here. Make
  it good.
- **Only spill is chunked**: worklogs, messages, imported documents. Structured
  objects — work items, notes, proposals, entities — are retrieved by query.
  Chunking a note destroys the structure that made it findable.
- Embed on write, in the same transaction, with the model recorded per row. A
  model mismatch at query time is an error, never a silent mixed-space search.
- **Harness fields are recomputed from the DB and cannot drift.** Model fields
  are refreshed on demand and marked stale when the graph moves. Never let a
  model write a harness field — in LumixAgents Phase 4, qwen3.5 produced
  `dead_ends` describing role refusals that never happened in that run,
  reconstructed from its own prompt.
- Rejected proposals with rationales go in the skeleton (contract §5.2). This is
  the field whose loss makes the agent re-propose what was already refused.
- Budget the assembly against the target model's `num_ctx` and **name what was
  dropped** in the assembled output. Silent truncation is the failure mode.

**Manual test:**
```bash
zettaplanner context show --project X --message "why is auth blocked?"
zettaplanner context show --project X --item ZP-14 --tokens
```
Read it as if you were the model. Is anything essential missing? Is anything
present that would mislead? Is a coordinate visible anywhere?

**Exit criteria:**
- [ ] Skeleton includes open items, deviations, notes, recent decisions, and
      rejected proposals with rationales
- [ ] Expansion answers dependency and architecture questions by traversal, with
      no vector search involved
- [ ] Spill only is chunked; embedding model recorded; mismatch errors
- [ ] Digest harness fields recomputed and provably drift-free; model fields
      marked stale on graph change
- [ ] Assembly stays under budget and names what it dropped
- [ ] No layout data reachable from `contextbuild` (import test)

---

### Phase 6 — Planner agent

**Build:** the agent loop; tool inventory; turn lease with per-project
serialization and a visible queue; SSE streaming; hard limits; error taxonomy
with a **single retry** on transient classes; message persistence.

**Tool inventory (the enforcement mechanism, not prompt instruction):**

```
read-only:   get_plan, get_item, get_diagram, get_entity, search_worklogs,
             list_notes, list_deviations, get_digest
write:       update_dev_field, add_worklog, add_subtask, check_item
propose:     propose_plan_change, propose_diagram_change, add_note
control:     handoff_to_human, finish
```

**Notes for the implementer:**

- **The agent has no direct-write tool for manager-owned fields.** Not a
  tool that refuses — a tool that does not exist. `update_dev_field` validates
  against the ownership table and the requesting user's assignment, so even it
  cannot be used to reach a manager-owned field.
- **Unknown-tool and denial errors are rewritten to teach.** Name the boundary,
  name the route: *"`update_plan` is not available. Changes to manager-owned
  fields are raised as proposals — call `propose_plan_change`."* Lead with the
  action. A second refusal in the same turn appends the count and an instruction
  to stop rephrasing.
- **Retry exactly the transient classes:** `tool_parse`, `server`, `timeout`.
  Never retry `boundary`, `validation_failed`, `max_turns`, or a real tool error.
  This is the gap LumixAgents Phase 6 identified and did not close: two of five
  golden-task attempts died on the model returning nothing usable, and neither
  was retried. Close it here.
- **A turn that declines in prose and produces nothing is not a success.** If a
  turn ends with no write, no proposal, and no `handoff_to_human`, it terminates
  as `no_output` and says so. A run that anticipates a wall and stops looks
  identical to a completed one otherwise.
- Hard limits, whichever trips first: `MaxTurns 40`, `MaxTokens 300_000`,
  `MaxWallClock 15m`. Tune later with evidence.
- Every tool call, every model call, every retry is persisted. `zettaplanner
  trace <turn_id>` replays it.

**Manual test:**
```bash
# via API, as a developer:
POST /threads/{id}/messages "mark ZP-14 in progress and log that the auth
                             library version is wrong"
# EXPECT: direct write to status + worklog; audit rows; no proposal

POST /threads/{id}/messages "push the milestone date out by two weeks"
# EXPECT: a proposal, not a write. Read its rationale.

POST /threads/{id}/messages "add a Redis cache between the API and the DB"
# EXPECT: a diagram proposal in the closed vocabulary

# two users post simultaneously → second is queued, visibly
zettaplanner trace <turn_id>
```

**Exit criteria:**
- [ ] Manager-owned changes always produce proposals; no path to a direct write
- [ ] Dev-owned writes on the requester's own items land directly, with
      `agent_committed` provenance and an audit row naming both agent and user
- [ ] Turns serialize per project; a second request queues visibly
- [ ] Transient classes retried once; boundary and validation never retried
- [ ] A turn producing nothing reports `no_output`, not success
- [ ] Denials name the boundary and the route; repeats escalate
- [ ] All three hard limits enforced independently
- [ ] `trace` replays every turn and tool call
- [ ] Streaming survives a client reconnect with `Last-Event-ID`

---

### Phase 7 — Proposals, approval, re-evaluate

**Build:** proposal creation from agent turns and from re-evaluate; conflict
detection against current state; atomic application as one revision; approval
authority per §5.1; **mandatory rejection rationale**; rejected proposals fed
into context; re-evaluate flow for hand-edited diagrams.

**Notes for the implementer:**

- **Atomic or nothing.** If any change conflicts, the proposal becomes
  `superseded` and reports the conflict. Never partially apply. Never auto-merge.
- **Rejection rationale is required** at the API level. A rejection without one
  is `validation_failed`. Without the rationale the agent re-proposes what was
  refused, and the user concludes the tool does not listen.
- **Re-evaluate returns a proposal, never a write** — including when the user
  triggers it themselves. A stray click must not rewrite a hand-corrected plan.
- Re-evaluate feeds the model the **provenance**: which elements a human edited
  after extraction. That is the highest-signal input the vision model will get.
- A developer may approve only a proposal whose every change is within their own
  dev-owned fields. Mixed proposals need a manager.

**Manual test:**
```bash
# hand-edit a diagram, then:
POST /api/v1/diagrams/{id}/reevaluate
GET  /api/v1/proposals/{pid}          # read the changes and rationale
POST /api/v1/proposals/{pid}/reject   # without rationale → 400
POST /api/v1/proposals/{pid}/reject -d '{"rationale":"the queue is deliberate"}'
# then ask the agent the same thing again — it must not re-propose it blindly
# modify the target, then approve an older proposal → superseded, with conflict
```

**Exit criteria:**
- [ ] Proposals apply atomically as one revision or not at all
- [ ] Stale proposals report `superseded` with the conflict named
- [ ] Rejection without rationale rejected; rationale reaches assembled context
- [ ] Re-evaluate produces a proposal and receives edit provenance
- [ ] Developer approval limited to wholly dev-owned proposals
- [ ] Approving a proposal writes one audit entry per change plus one for the
      decision

---

### Phase 8 — Export, git projection, dogfood readiness

**Build:** `spec.md` export from a work item or milestone, bundling intent,
acceptance criteria, constraints, and the canonical YAML slice of referenced
entities; optional per-project git projection; dashboard query endpoints;
project-level audit query.

**Notes for the implementer:**

- **Export is manual, always.** Contract §10 defers Lumix integration to v0.1;
  do not build a trigger.
- The spec bundles the **serialized entity slice**, not a picture and not a
  paraphrase — Lumix's manager decomposes prose, and prose plus a precise
  architecture slice is what makes that decomposition land.
- **Git is one-way and regenerable.** DB commits, then a background worker
  serializes and commits. Worker failure loses nothing: regenerate the whole
  tree from the DB. If the projection ever becomes an input, that is a
  correctness bug, not a feature.
- One file per node and per edge; stable slug-derived paths; canonical bytes
  (contract §6.2). Human and agent commits use distinct git identities.

**Manual test:**
```bash
POST /api/v1/work-items/{id}/spec-export > spec.md
# read it as Lumix's manager would. Is it decomposable without guessing?
zettaplanner git enable --project X && zettaplanner git resync --project X
git -C data/git/X log --oneline && git -C data/git/X show HEAD
# delete the git dir entirely, resync, confirm identical tree
```

**Exit criteria:**
- [ ] `spec.md` contains intent, criteria, constraints, and the entity YAML slice
- [ ] A spec exported for a real item is decomposable by a reader with no other
      context
- [ ] Git projection regenerable from scratch to a byte-identical tree
- [ ] Git failure never blocks or corrupts a DB write
- [ ] Audit query reconstructs who changed what, including every agent write
- [ ] **Export readiness rehearsal:** using SQL only, produce (a) every status
      transition on a project with its duration, (b) all worklogs and deviations
      with actor and timestamp, (c) agent turns with tokens and error class.
      Nothing here ships a CSV — the point is to find the gap now, while the
      product is still cheap to change, rather than in v0.1 when the missing
      months cannot be recovered

---

### Phase 9 — Dogfood

**No new features.** Use ZettaPlanner to plan ZettaPlanner's own v0.1, and to
plan the FDN-Origin scoping work, for **at least two weeks** with real
stakeholders.

**Exit criteria:**
- [ ] Two real projects planned end to end by more than one person
- [ ] At least one hand-drawn HLD ingested, corrected, and used in a real
      planning conversation
- [ ] You have opinions about what is wrong — **write them down as `Dn` entries
      and fix before v0.1.** This one is the operator's, and it is why the build
      stops here.

**Do not proceed to v0.1 until you have actually used it.** v0.1 will automate
whatever exists here, including its flaws.

---

## 4. What to measure from Phase 6

A small task set — 8–12 real planning requests with known-good outcomes — run
against every configured model. Track: proposal well-formedness rate, direct
write vs proposal correctness, tokens per turn, turn count, retry rate by error
class, context assembly size, `no_output` rate.

Without this you cannot distinguish *I improved the harness* from *I changed the
harness and got lucky*.

---

## 5. Open questions

| # | Question | Decide by |
|---|---|---|
| Q1 | Does the planner need a separate cheap model for digest refresh? | Phase 5, measure both |
| Q2 | Optimal chunk size and overlap for worklog spill | Phase 5, measure |
| Q3 | Does per-page VLM extraction beat whole-document on multi-page PDFs, and by how much? | Phase 3, measure — the assumption is stated, not proven |
| Q4 | Is one retry enough for `tool_parse`, or does it need two with backoff? | Phase 6, from error-class data |
| Q5 | Should item-scoped threads have their own context skeleton, or share the project one? | Phase 6 |
| Q6 | Lease TTL and heartbeat interval that survive a laptop sleeping | Phase 2, then revisit in dogfood |
| Q7 | What shape the v0.1 audit export takes — one flat CSV or several (transitions, worklogs, turns, raw events) | v0.1, from the Phase 8 rehearsal and real dogfood data. Deliberately not decided now |