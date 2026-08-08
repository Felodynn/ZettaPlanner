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
    auth/ project/ diagram/ ingest/ plan/ contextsvc/ thread/ proposal/ export/
                           (thread/ owns conversations, the turn queue, and the
                            read side of governance)
  agent/                   loop, tools, turn lease, limits, retry, trace
  contextbuild/            skeleton, expansion, spill, digest — pure, no I/O
  vector/                  the similarity boundary (§6.4); no SQL
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

Phase 5 went further with `contextbuild` and gave it **no I/O at all** (D46):
no database handle, no HTTP client, no clock it did not receive. A coordinate
cannot reach a prompt from a package that has no query to reach it with, and
every rule it enforces is then testable with no infrastructure.

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

The database tests run against a real Postgres and skip themselves without one.
The database is the product; a mock of it would test nothing.

```bash
sudo -u postgres psql -c "CREATE DATABASE zettaplanner_test OWNER $USER;"
sudo -u postgres psql -d template1 -c "CREATE EXTENSION vector;"   # once, so
                                    # every future createdb inherits pgvector
make test-db
```
Deliberately set `num_ctx` above the model's maximum and confirm boot fails with
an actionable message. Change the embedding model to one with a different
dimension and confirm boot fails rather than silently corrupting `chunks`.

**Exit criteria:**
- [x] All configured models validate; `/api/ps` allocated context matches config
      — for `self_hosted`. `cloud` proves a floor by probe instead (D20)
- [x] Embedding dimension asserted against the `chunks` column at boot
- [x] Bad config fails at startup with a message naming the fix — covered by
      `TestLoadModelsRejects` for ten distinct mistakes
- [x] Migrations idempotent and safe under two concurrent processes
- [x] `audit_log` rejects `UPDATE` and `DELETE` — and `TRUNCATE`, because a
      cleanup job is the shape this failure actually arrives in
- [x] Export indexes present in the first migration
- [x] `request_id` is `NOT NULL` and populated by middleware, and generated
      server-side rather than accepted from the caller
- [x] Audit actions are typed constants; transitions distinct from `*.updated`
- [x] A comment in the schema states retention: never pruned, never rotated
- [x] OpenAPI document emits and validates

**Decisions**

| # | Decision | Rationale |
|---|---|---|
| D20 | **Startup window validation is tiered by `tier: self_hosted \| cloud`.** `self_hosted` keeps the D4 check unchanged: `/api/ps` allocated `context_length` must equal `num_ctx`. `cloud` substitutes a **context probe** — send filler, read `prompt_eval_count` back, treat it as a floor; grow the filler and a count that does not move is a clamp | Measured, not assumed: a completed chat against `glm-5.2:cloud` on Ollama 0.30.11 leaves `/api/ps` returning `{"models":[]}`, because the weights are not on this machine. The check D4 exists for is unavailable for the two models v0 actually runs on. Deleting it would reinstate exactly the failure D4 was written about; the probe keeps the guarantee with the only mechanism the cloud tier offers. `tier` is control of the runtime, not distance — a remote Ollama we administer is `self_hosted`, and its `/api/ps` was confirmed authoritative |
| D21 | **The probe proves a floor and names what it did not prove.** Default floor 16,384 tokens at boot; `models check --full-probe` pays the full `num_ctx` in input tokens. Successful probes cache for 24h keyed on `(endpoint, model, num_ctx, target)` | Proving a 262,144-token window costs 262,144 input tokens per boot per cloud model. The floor rules out every common silent clamp (2048/4096/8192/16384), which is the failure class that actually happened. Reporting "at least N proven, the rest unverified" rather than "ok" is the same discipline as naming what context assembly dropped: a validation that overstates its coverage is the truncation failure one level up |
| D22 | **Fallback across `roles.prefer` is resolved at turn boundaries only, never mid-turn.** `ErrRateLimit` ends the turn; the next turn resolves to the next model and records it on `agent_turns.model` | A turn spanning two models spans two context windows and two tool-calling behaviours. `trace` becomes unreadable and §4's per-model measurements stop meaning anything — you can no longer distinguish *I improved the harness* from *the model changed underneath me*. Context assembly is budgeted against the chosen model, which requires choosing before assembling |
| D24 | **Migration 0001 asserts pgvector, it does not create it.** `CREATE EXTENSION` requires superuser; the migration raises a named error instead, telling the operator the package to install and the statement to run | A migration runner that needs superuser is a deployment where the application role has superuser, which is a much larger problem than one manual setup step. The assertion also fails at `migrate up` rather than at the first `CREATE TABLE … VECTOR(1024)`, so the message is about pgvector rather than about an unknown type. Verified: the down migration leaves the extension in place, so `down` then `up` round-trips without privileges |
| D23 | **The OpenAPI error schema is the Go type, not a custom marshaller.** `huma.NewError` is overridden alongside `huma.NewErrorWithContext`, and the router's own plain-text 404/405 are rewritten into the envelope by middleware | huma derives the error schema by reflecting over whatever `NewError` returns. Overriding only the per-request hook produced a server that emitted the contract's envelope while the document advertised RFC 9457 — and the frontend's client is generated from that document. That is D2's generated lie one level down. Same reasoning for the router's prose 404: "one shape, always" (§7.3) is worth nothing if the shape changes exactly when something has gone wrong |

**Measurements**

- `glm-5.2:cloud` — `/api/show` reports a 1,000,000-token ceiling and
  `[thinking, completion, tools]`. **No `vision`.** The planner/vision split is
  therefore forced by capability, not chosen.
- `gemma4:31b-cloud` — 262,144 ceiling, `[completion, thinking, tools, vision]`.
- Probe ratio: 4,096 unique filler words evaluated as **20,891 prompt tokens**
  on `glm-5.2:cloud`, so roughly five tokens per word. The probe sizes its
  filler from that and then corrects against the real count.
- Cold-loading `qwen3-256k:latest` at 131,072 ctx through the Cloudflare-fronted
  remote returned **HTTP 524** once — the origin's load time exceeds the proxy's
  100s ceiling. Warm, it validates in ~10s. **Open: the planner fallback cannot
  be validated from cold through that proxy.** Options are a longer `keep_alive`
  on the remote, a preload on that host, or bypassing the proxy. Decide before
  Phase 6, when the fallback first matters.

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
- [x] Login, logout, session rotation, `me` with effective per-project role
- [x] Field ownership enforced from the table; adding a field to the table
      changes behaviour with no handler change
- [x] Every mutating request writes exactly one audit row, in the same tx
- [x] Per-project role override beats the global role — **including downward**
- [x] No hard-delete path exists in any repository; a test greps for `DELETE FROM`
      outside the migration runner and fails on a hit
- [x] A `403` names both the owning role and the route forward

**Decisions**

| # | Decision | Rationale |
|---|---|---|
| D25 | **Field ownership is two columns, not two tiers.** A manager cannot write a dev-owned field — not `status`, not `progress_pct` — and the denial's route is *reassign the item to yourself first, or ask its assignee* | §4.2 is a two-column table and §4.1 gives the manager "all **manager-owned** fields". Read as a ladder it would collapse into "manager can do everything", and the point of `status` being dev-owned is that it is the developer's report of reality: a manager overwriting it destroys the signal instead of correcting it. The consequence is real and deliberate — closing a departed developer's item means reassigning it first, which is manager-owned and leaves an audit trail. **Flagged for confirmation in dogfood (Phase 9)** |
| D26 | **The per-project role override wins in both directions**, except that `admin` is never lowered by a membership row | An override that could only raise is a permission you cannot take away, and "add the manager to this project as a developer" is the normal way to say *you are hands-on here*. Admin is deployment-level authority and a project membership is not the right place to revoke it |
| D27 | **The written-field set is derived by reflection over the patch struct**, not by a hand-written switch | This is what makes the exit criterion true rather than aspirational: no handler names a field, so adding one to the ownership table plus the request struct is the entire change. `UnmappedFields` turns the inverse — a settable field with no rule — into a test failure rather than an unchecked write |
| D28 | **The no-hard-delete test requires an annotation, not silence.** `DELETE FROM` is permitted only with a `zp:hard-delete-ok` marker *and* only on a table the contract does not list as soft-deleted | A blanket ban gets silenced the first time somebody genuinely needs a removal, and a silenced test enforces nothing. `project_members` is the one exception in v0: membership is current state, both the add and the removal are audited, and a soft-deleted membership row would need filtering out of every permission check for the rest of the product's life |
| D29 | **A failed login is audited only when the account exists.** An unknown address is logged, not written to `audit_log` | The log's retention is never (§6.5 constraint 5). An unauthenticated caller able to append a row per request is an unauthenticated caller able to grow a table that is never pruned. Attempts against real accounts are bounded by the user count and are the ones worth having. **Open: rate limiting is not built, so even bounded attempts are unbounded in volume — decide before the deployment is reachable from anything but a LAN** |
| D30 | **Each test package gets its own scratch database**, created on demand from `template1` | Go runs packages' tests concurrently, and two packages resetting one database produce failures that read exactly like product bugs. Installing pgvector into `template1` once is what lets the unprivileged application role create them |

**Notes**

- The session cookie holds a token; the database holds its SHA-256. There is no
  path that reads a token back, only one that compares — so a database dump
  does not hand over live sessions.
- A wrong password and an unknown address return the identical message, and an
  unknown address is verified against a fixed hash anyway. Two different
  answers, or two different response times, is an account enumeration endpoint.
- A non-member reading a project gets `not_found`, never `forbidden`.
  Distinguishing them lets anyone enumerate which projects exist.

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
- [x] Semantic and layout stores separate; import-boundary test passes and fails
      when deliberately violated
- [x] Serialized YAML contains no coordinates, by construction
- [x] Serializer byte-deterministic across insertion orders
- [x] Save is atomic; stale base rejected with a diff, never merged
- [x] Lease acquired, heartbeated, expired; expiry blocks save
- [x] Layout write produces no revision and needs no lease
- [x] Closed vocabulary enforced; error names valid values **with their meanings**
- [x] Cross-diagram query answers "what depends on entity X"
- [x] Name collision produces a Note, not a merge

**Decisions**

| # | Decision | Rationale |
|---|---|---|
| D31 | **Notes get a row in the §4.2 ownership table.** `status` and `body` are manager-owned; anyone may raise one | Notes have a lifecycle and §4.2 predates it. Putting the check in a handler instead would be an authority decision living outside the table, which is the exact thing the table exists to prevent. Resolving "these two may be the same datastore" is an architectural judgement; noticing it is not, and a developer who spots an overlap should not have to ask permission to say so |
| D32 | **A coordinate is not an owned field.** Any project member may write layout; no lease, no revision, no audit row — except the `layout_mode` flip, which is audited | The ownership table covers the semantic layer. Requiring a manager to tidy a canvas would mean the people who read a diagram most cannot make it readable. The flip *is* recorded because it is a permanent state change with a consequence: automatic layout stops, and turning it back on destroys the arrangement — which is why the reset is a separate endpoint requiring `confirm=true` rather than a field on the layout write |
| D33 | **`diagrams` gains a stored `slug`** (migration 0003, contract §6.1 amended) | §6.2's document opens with `diagram: auth-context` and the git projection derives paths from it. Deriving that from the display name at serialization time makes a rename look like a delete plus an add — precisely what "stable identity in paths" exists to prevent. Same reasoning as the entity slug: assigned once, never follows the name |
| D34 | **The closed-vocabulary `enum` stays in the OpenAPI document, and the schema-validation error is rewritten to teach** | The enum is what gives the frontend's generated client a union type instead of a string, so removing it costs real safety. But huma validates against it first and answers "validation failed" with the valid values buried in a nested array — fine for a browser, useless for a model. Phase 2 requires that error to teach because Phase 6 reads it. So the enum is kept and the message is replaced by the domain's teaching text: the same words whichever door the caller came in |
| D35 | **One contract error code maps to exactly one HTTP status.** huma's 422 for a body-schema violation is normalised to the 400 our own `validation_failed` uses | A code that arrives as 400 sometimes and 422 other times is a special case the generated client has to carry, for no gain |
| D36 | **Ingest accept is additive and lease-free** (`diagramsvc.ApplyInTx`), and it is a *different entry point* from the human whole-diagram save, not a flag on it | Save is a human at a canvas sending complete state: what it omits, it deletes. A drawing is evidence that these five services exist and says nothing whatsoever about the eleven already in the diagram, so treating its silence as a deletion lets one badly-lit page destroy an afternoon's architecture. Additive also removes the need for a base revision — there is nothing to be stale against when nothing is removed — and for a lease. It still writes exactly one revision and one audit row, and it still goes through the same `CheckWrite`: an ingest is not a back door around §4.2. Phase 7 reuses the same entry point for an approved proposal |
| D37 | **The merge step is deterministic code, not a second model call** | The judgements a merge faces — is the box on page 3 the same box as page 1, where does the arrow that ran off the sheet actually go — are exactly the ones §3.4 says must never be automatic. A model asked to make them returns a confident answer over data that is already uncertain, and the second failure compounds the first with nothing left to inspect. Being pure code also makes it testable with no database and no model, which is why its rules are exercised against hand-written page disagreements. Two boxes merge only when their names normalise equal; a kind disagreement is recorded as a `Conflict` and flagged, never settled |
| D38 | **Ollama Cloud accepts `format` and ignores it.** Startup validation probes for it and reports `format_enforced` per model; the ingest prompt restates the required shape, the parser finds the JSON object inside whatever prose arrives, and a wrong answer is retried once with the teaching error | Measured against Ollama 0.30.11 with a two-property schema: locally-served `gemma4:12b` returns a conforming object; `gemma4:31b-cloud` and `glm-5.2:cloud` both return markdown prose. **This is the third instance of one pattern** — `/v1/chat/completions` discarding `num_ctx` (D4), `/api/ps` reporting nothing for cloud (D20), `format` discarded — and it is worth stating as a rule: a guarantee that holds for weights we run does not hold for weights we rent, and nothing in the response says which you got. It found a real bug the same day: the first cloud extraction returned three arrows, zero boxes, and a key called `boxes`, and the decoder reported success. A warning rather than a failure, because the harness copes — but `models check` says which behaviour a deployment has, because "copes" and "guaranteed" are different properties |
| D39 | **`ingest_pages.raw_output` stores the model's answer byte for byte, on success and on failure.** `extracted` is not a substitute | `extracted` is JSONB reached through a Go struct: keys reordered, unknown fields dropped, an undecodable array turned into `null`. Each of those destroys the answer to "what did the model actually say", which is the only question worth asking when an extraction is wrong — and it was the reason D38 took an extra hour to find, because the stored "raw" output was our own re-marshal showing `nodes: null` when the model had never used that key. Stored on failure too: a malformed answer is the one you most want to read |
| D40 | **An extraction with no nodes is a failure, not an empty page.** `error_class: no_nodes`, retried once with a correction naming the key | Every arrow starts and ends at a box, so a page with arrows has boxes. Reporting success over a payload containing nothing makes an empty result and a correct one indistinguishable, which is how a user ends up accepting a diagram with seven arrows and no services in it. Paired with the hard gate on illegible elements, this is the phase's general rule: **the harness never lets "the model said nothing useful" look like "there was nothing to say"** |
| D41 | **`VocabularyError` and `Provenance` moved to the root `domain` package, aliased in `domain/diagram`** | §3.7 already says provenance applies to "every node, edge, work item, and plan field", and the plan's statuses are closed sets needing the same teaching error. The alternatives were a plan package importing the diagram package for an error shape, or a second error type the HTTP layer had to learn about — and a second copy of the six provenance strings that would eventually disagree about one of them. Aliases keep every existing reference and the `errors.As` in `errmap.go` working unchanged, because an alias is the same type |
| D42 | **A dev-owned rule scoped to the project is a floor, not a ceiling** — for lifecycle checks only. Contract §4.2 amended | Found by building Phase 4 against Phase 1's table: `OwnerDeveloper` denied every non-developer, so a **manager could not file a worklog, raise a deviation, or open a note on their own project**. Phases 2 and 3 never hit it because notes were created by the system path. A manager who cannot record what went wrong stops recording it, and that is precisely the evidence the retrospective is built from. The exemption is deliberately narrow — it applies where the field name is empty, which is only ever create or delete — and a generated test fails the moment a dev-owned *field* is given project scope, because that field would silently inherit a reading nobody intended. Dev-owned fields stay a ceiling: a manager still cannot write `status` |
| D43 | **Work item refs come from `projects.ref_seq`, a monotonic counter read with `UPDATE ... RETURNING`** (migration 0005, contract §6.1 amended) | 0001 already refuses to make the ref index partial so that `ZP-14` is never reissued after `ZP-14` is dropped. `max(ref)+1` would satisfy that only by parsing an integer out of text, and would race between two concurrent creates. The counter is exact, and the row lock the UPDATE takes serialises the creates for free — no advisory lock, no retry loop |
| D44 | **The plan's four status vocabularies are closed in the schema** (`milestones.status`, `sub_tasks.status`, `work_items.priority`; migration 0005). Work items and subtasks **share** one set; milestones get a coarser one; an unset priority is not `normal` | 0001 left these as bare TEXT because the contract's schema block does, and every other status column in the database carries its set. A typo'd status is not a validation failure, it is a work item no query ever finds again. Sharing the set between items and subtasks is one thing to teach the planner rather than two, and makes the time-in-status query identical at both levels. Milestones are coarser on purpose: a milestone is not `blocked` or `review` — those are facts about the items inside it, and offering them invites a status that contradicts its own contents |
| D45 | **A patch that changes only a transition writes only the transition entry** | The obvious implementation writes `*.updated` alongside every `*.status_changed`, and its before and after are then identical. That is worse than no row: it records an edit that never happened, and the export cannot tell it from one that did. So each fact is written only when it moved — up to four entries from one PATCH (status, assignee, milestone, everything else), grouped by the shared `request_id` |
| D46 | **`internal/contextbuild` does no I/O whatsoever.** The gathering lives in `internal/service/contextsvc`; the assembler is given rows | §1.1's structural rule says the packages producing model context must not import anything that reads `diagram_layout`, and the test states that as "must not import internal/repo". Building the assembler *under* that rule rather than beside it turns out to be stronger and simpler: it has no database handle, so there is no query from it to reach a coordinate with, and every rule it enforces — the budget ladder, the never-dropped sections, the rendering — is testable with no infrastructure at all. The cost is a service that is mostly plumbing, which is the correct place for plumbing |
| D47 | **Spill is embedded inside the transaction that wrote the source row. A configured-and-failing embedder fails the write** | §6.3 says embed on write, in the same transaction. The reason is that the alternative — commit now, index shortly — produces a worklog that exists and is invisible to retrieval, and *nothing downstream can distinguish that state from "there was nothing relevant"*. A backfill job would be a second source of truth about what has been indexed. The uncomfortable half is stated rather than hidden: a worklog write now depends on the embedding model answering. **Not configured** and **configured and broken** are deliberately different — with no embedder the write succeeds and the assembled context says layer 3 is unavailable; with a broken one the write fails and says why. Worth revisiting in dogfood: if a local Ollama hiccup blocks note-taking often enough to change behaviour, the tradeoff is wrong |
| D48 | **Digest model-field staleness is derived from the project's `audit_log` count, not set by a flag** | `model_fields_stale` as a flag is correct until the fourteenth write path forgets to set it, and the failure is invisible — a stale hypothesis presented as current. The log is append-only and nothing is ever deleted from it (§6.5), so its count for a project only rises: recording the mark when the model wrote makes "has the graph moved since" a comparison. Stale means dated, not wrong, so the fields are kept — the `dead_ends` are the most valuable thing in them. And a project where no model has written yet is **not** stale: a staleness badge over an empty panel teaches people to ignore the badge |
| D49 | **A similarity search across two embedding models is an error, never a filter** | Filtering to the rows that happen to match would answer a question about ten thousand chunks using the eleven in the right space, and report success — and the answers would be plausible, which is what makes it the worst available behaviour. `chunks.model` is per row precisely so a half-migrated corpus is detectable; the refusal names both models and says to re-embed |
| D50 | **Each write tool declares the object it writes, and the "no path to a manager-owned field" test is generated from the ownership table** | The first version of that test was a hand-written list of manager-owned names checked against every write tool's schema, and it failed immediately on `add_subtask` accepting `title` — because `title` is manager-owned on a WorkItem and dev-owned on a SubTask. The hand-written list was not wrong about §4.2, it was wrong about *which object*. Making the tool name its object turns the check into a question the table can answer, including for `update_dev_field`, whose boundary lives in an enum rather than in a property name. A field moved between columns tomorrow is covered tomorrow |
| D51 | **The SSE hub keeps a bounded in-memory ring per project and emits `resync` when a client's gap exceeds it** | §7.2 says `Last-Event-ID` is honoured "where it can be" and `resync` otherwise, which is a design, not a hedge. These events are notifications about facts that are already durable in Postgres; persisting a second copy would create a second thing that can disagree with the first. A restart drops the ring and every client resyncs, which is the correct answer after a restart. Two details earned their own thought: a client that sends `Last-Event-ID: 0` has seen nothing and wants everything, which is **not** the same as a client that sends no header and has missed nothing — collapsing them loses a reconnecting client's whole gap. And a subscriber whose buffer is full is dropped rather than blocked, because holding a turn's progress hostage to a wedged browser tab is not a trade anybody wants |
| D52 | **A turn that did not succeed posts the harness's verdict into the thread alongside the model's summary** | Found by the test, then seen for real: a `no_output` turn ends with a confident summary — "I looked at the plan and everything seems fine" — and posting only that is indistinguishable from a turn that did the work. So both go in: what the model said it did, and what actually happened. The verdict names the finish reason and, for `no_output`, says plainly that nothing was written, proposed or handed back and that `zettaplanner trace` shows why |
| D53 | **`turn_events` is append-only by trigger, and separate from `audit_log`** | The audit log is the permanent record of what changed (§6.5) and may never be pruned. The trace is diagnostic: what the *model* did, including the calls that changed nothing and the ones that failed. Mixing them would either pollute the export with retries or lose the retries entirely. Append-only for the same reason as the audit log, because "what actually happened" is not a question a rewritable table can answer — and separate so a later version can prune it by age without touching anything the retrospective reads |
| Q3 | **Answered: per-page wins, and not narrowly** | Same schema, same rules, same model, same three pages. Per-page: 7 nodes, 7 edges, correct kinds, both dangling arrows flagged. Whole-document, three trials: **0 nodes every time**, 7 edges every time, and an invented `unknown_destination` endpoint. The failure is not "worse cross-page linking" as assumed — the model stops emitting the first array altogether once the input is multi-image, so there is nothing for the edges to attach to. Whole-document is cheaper in input tokens (1,605 against 3,840, since the images share one prompt) and the output is unusable. Latency on the real cloud model was 7–10s per page. The one thing per-page genuinely loses is that whole-document *did* join `auth_service → token_store` across the page break; per-page could not, and correctly recorded it as unresolved rather than guessing |
| Q6 | **Lease TTL 5 minutes, heartbeat 60 seconds** — answered, and due to be revisited in dogfood | Long enough to survive a screen lock, a network blip, or a lid closed while somebody answers the door; short enough that a browser killed mid-edit does not hold a diagram for an afternoon. A sleeping laptop loses its lease, which is the correct outcome — the alternative is a diagram nobody can touch because of a machine that is not coming back. The TTL is published in the acquire response so the frontend does not hard-code a number that later disagrees with the server |

**Notes**

- The traversal that answers "what depends on X" runs over **catalog entities,
  not diagram nodes**. The same service drawn in a context diagram and a
  component diagram is one identity, and its dependents are the union. Doing it
  per-diagram would answer a narrower question that nobody asked.
- `contains` and `deploys_to` are excluded from that traversal. They say where
  something sits, not what needs it; including them turns "what depends on the
  token store" into "everything in the same trust boundary".
- A save that changes nothing produces **no revision and no audit row**. An
  empty revision is noise in the log and an empty commit in the git projection.
- The heartbeat failure deliberately carries **no diff**. The lease records the
  revision it was taken at, not the one the client last saw, so counting saves
  from it counts the holder's own — it reported "2 saves landed while your
  session was gone" when the holder had made both. The diff belongs on the save
  path, where the request carries a base revision that means something.

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
- [x] Per-page extraction; merge is a separate reviewable step
- [x] Every extracted element carries provenance `vlm_extracted` and a confidence
- [x] Unreadable elements are marked low-confidence, never invented
- [x] Extraction confined to the closed vocabulary
- [x] Malformed model output fails the page, not the ingest; retry is per-page
- [x] Raw per-page output retained
- [ ] A real notebook photograph produces a diagram you would correct rather than
      discard — **outstanding: needs an actual photograph.** Verified instead
      against a generated 3-page PDF with an arrow crossing pages and an arrow
      into a question mark, on the real `gemma4:31b-cloud`: 7 entities, correct
      kinds including `external_system` inferred from "third party, not ours",
      `auth-service` recognised as one identity across all three pages, and both
      dangling arrows flagged rather than guessed or dropped. Held open
      deliberately — a clean render is not a notebook, and the criterion says
      notebook. Closed in dogfood (Phase 9), which requires the same thing

**Notes on what was built:**

- **The merge is deterministic code, not a second model call** (D37). Pure, no
  database and no clock, so its rules are testable against a hand-written
  five-page disagreement rather than against a photograph having a bad day.
- Extraction runs in the **background**, not in the request. Four pages of a PDF
  is minutes of model time, and a request holding a connection open that long
  dies to the first proxy timeout leaving no record of how far it got. The
  ingest row is the record; Phase 5 replaces polling with `ingest.progress`.
  The goroutine deliberately does not use the request context — that is
  cancelled the instant the 201 is written — but does carry its request id, so
  `ingest.created` and `ingest.merged` group in the audit log.
- A killed process leaves ingests claiming to be in flight. A **boot sweep**
  marks them failed with `error_class: interrupted`; the pages already read are
  kept and the rest are retried by hand. Not restarted automatically: that
  spends model budget on work nobody is waiting for any more.
- **EXIF orientation is applied before anything else.** A phone stores a
  portrait photograph landscape with the rotation only in the metadata, Go's
  decoders ignore the flag, and a sideways page is not a page a model reads
  badly — it is a page it reads as a different drawing. Hand-rolled for the one
  tag; a full EXIF parser is mostly metadata that has no business near a prompt.
- Uploading needs only membership; **accepting needs diagram authority**. A
  photograph binds nobody to anything, and applying it to the architecture is
  the write. Same split as a worklog.

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
- [x] Full plan CRUD with field ownership enforced per field
- [x] Dependency cycles rejected with the offending path named
- [x] Warnings computed live for blocker-date and milestone-date violations
- [x] Developer can write dev-owned fields on assigned items only
- [x] Entity refs are set explicitly and never derived
- [x] Every status change and checklist tick recorded as a transition action
- [x] Soft delete only; a deleted item's history stays queryable
- [x] `audit_log` reconstructs the full history of one work item — and a single
      SQL query answers "how long did this item spend in each status", with no
      stored duration field anywhere

**Notes on what was built:**

- **The ownership table did the work.** Phase 1 put every plan field into
  `internal/policy` before any of them could be written; Phase 4 added no
  per-field logic at all, only handlers that hand `policy.Fields(body)` to
  `CheckWrite`. Two generated tests keep it honest in both directions: no patch
  body carries a field the table has never heard of, and no rule in the table is
  unreachable from any endpoint — a dead rule reads in review as an enforced one.
- **A PATCH can produce four audit entries.** A request that moves the status,
  the assignee, the milestone and the title is four facts, and they are written
  as `status_changed`, `assigned`, `milestone_changed` and `updated`, sharing one
  `request_id`. The inverse matters as much: a status-only PATCH writes no
  `work_item.updated`, because an audit row whose before and after are identical
  puts an edit in the history that never happened.
- **Cycle detection runs after the write, inside the transaction.** Checking
  first would mean modelling the proposed graph in Go; checking after lets
  Postgres answer against the rows that exist and the rollback undo them. The
  walk is seeded from the item just written rather than from the whole project:
  the graph was acyclic before this transaction, so any cycle now runs through
  one of the new edges.
- **Refs come from a counter on the project row, not `max(ref)+1`.** The
  `UPDATE ... RETURNING` takes a row lock, which serialises two concurrent
  creates without an advisory lock or a retry loop, and a counter that only ever
  goes up cannot reissue `ZP-14` after `ZP-14` is deleted. Verified: deleting
  ZP-3 and creating another item issued ZP-4.
- **Dates are calendar days, and `""` means clear.** A JSON null cannot say
  "remove this" through a Go pointer — absent and null both decode to nil — so
  the optional date and id fields carry wire strings and the empty one clears.
  A range that runs backwards is refused rather than reordered: one of the two
  dates is a typo and the system cannot tell which.

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
- [x] Skeleton includes open items, deviations, notes, recent decisions, and
      rejected proposals with rationales
- [x] Expansion answers dependency and architecture questions by traversal, with
      no vector search involved
- [x] Spill only is chunked; embedding model recorded; mismatch errors
- [x] Digest harness fields recomputed and provably drift-free; model fields
      marked stale on graph change
- [x] Assembly stays under budget and names what it dropped
- [x] No layout data reachable from `contextbuild` (import test)

**Notes on what was built:**

- **`internal/contextbuild` does no I/O at all** (D46). Not "does not import the
  repo" — no database handle, no HTTP client, no clock it did not receive. That
  is a stronger form of the D5 rule than the import test can state: a coordinate
  cannot reach a prompt from there because there is no query from there to reach
  it with. `internal/service/contextsvc` does every query and hands rows in.
- **Everything is read in one transaction.** Layers gathered across several
  snapshots can disagree — a skeleton listing an item as open beside an
  expansion showing it done — and a model handed a contradiction resolves it
  silently and confidently.
- **Reference resolution is deterministic and closed.** Expansion starts from
  `KEY-\d+` matched against the project's key and from entity slugs and display
  names that actually exist; it finds nothing else, and never asks a model which
  items a sentence is about. `context show` prints what it resolved to, because
  "the context is about the wrong item" is a failure that looks exactly like
  "the model is bad at this".
- **Staleness is derived rather than flagged** (D48), from the project's
  `audit_log` count. The log is append-only, so the count only rises and "has
  the graph moved" is a comparison — no write path has to remember anything.
- The **token count is an estimate and says so everywhere it is printed.** A
  real count needs the target model's tokenizer, which costs a round trip per
  assembly and, on a cloud model, tokens. The estimate runs deliberately high:
  an underestimate overflows at the model rather than at the budget check.
- Read as the model on a real project: the assembled context answered *"why is
  auth blocked?"* from the skeleton alone — the item, its deviation, and two
  rejected proposals that between them rule out Redis and a TTL column. Adding
  `--item ZP-1` brought the dependency chain, the subtasks and the
  architecture slice; the top retrieval hit was the exact paragraph weighing the
  sweeper against a check-on-read. Nothing present was misleading, and no
  coordinate appeared with real ones in `diagram_layout`.

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
- [x] Manager-owned changes always produce proposals; no path to a direct write
- [x] Dev-owned writes on the requester's own items land directly, with
      `agent_committed` provenance and an audit row naming both agent and user
- [x] Turns serialize per project; a second request queues visibly
- [x] Transient classes retried once; boundary and validation never retried
- [x] A turn producing nothing reports `no_output`, not success
- [x] Denials name the boundary and the route; repeats escalate
- [x] All three hard limits enforced independently
- [x] `trace` replays every turn and tool call
- [x] Streaming survives a client reconnect with `Last-Event-ID`

**Notes on what was built:**

- **The inventory check is generated from the ownership table** (D50). Each
  write tool declares the object it writes; a test then asserts that every field
  its schema accepts — including the values of `update_dev_field`'s `field`
  enum — is dev-owned for that object. A field moved from one column of §4.2 to
  the other tomorrow is covered by that test tomorrow, with nobody remembering
  to extend it.
- **A `no_output` turn posts two messages, not one** (D52). The model's summary
  and the harness's verdict. A turn that produced nothing very often ends with a
  confident summary — the manual run's *"I looked at the plan and everything
  seems fine"* — and posting that alone is exactly the failure this phase exists
  to close.
- **Serialization is the partial unique index from migration 0001**, not a
  mutex. `FOR UPDATE SKIP LOCKED` on the claim keeps the queue moving; the index
  rejects whichever runner would have been the second. Both are needed, and both
  survive the process being run twice.
- **The SSE hub is in memory, and says so when it cannot replay.** The events
  are notifications about facts that are already durable; a second copy in
  Postgres would be a second thing that can disagree. A client whose gap is
  wider than the ring gets `resync` rather than a silent resume from a hole, and
  an absent `Last-Event-ID` is deliberately distinguished from `0`.
- Measured on the real `glm-5.2:cloud`: **7 turns, 0 retries, 2–4 steps each,
  3–5 seconds per model call.** The four manual prompts all behaved as specified
  on the first attempt, including the unscripted *"delete every work item in
  this project"*, which produced a proposal rather than a deletion.

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
| ~~Q3~~ | ~~Does per-page VLM extraction beat whole-document on multi-page PDFs?~~ | **Answered in Phase 3 (see D-table).** Whole-document returned 0 nodes in 3 of 3 trials against the same schema per-page handles correctly |
| Q4 | Is one retry enough for `tool_parse`, or does it need two with backoff? | Phase 6, from error-class data |
| Q5 | Should item-scoped threads have their own context skeleton, or share the project one? | Phase 6 |
| Q6 | Lease TTL and heartbeat interval that survive a laptop sleeping | Phase 2, then revisit in dogfood |
| Q7 | What shape the v0.1 audit export takes — one flat CSV or several (transitions, worklogs, turns, raw events) | v0.1, from the Phase 8 rehearsal and real dogfood data. Deliberately not decided now |
| Q8 | **Should a subtask's own assignee be able to write it?** Today they cannot: §4.2 scopes SubTask and ChecklistItem to the assignee of the *parent item* ("within own item"), so handing somebody a subtask without reassigning the item leaves them unable to tick it. Implemented as the table says rather than widened silently | Dogfood (Phase 9), alongside D25. Both are the same shape of question — a rule that reads correctly in the table and may be wrong in a real week |
| Q9 | **Does expansion need to match slug components, not just whole slugs?** The manual test's own question — "why is auth blocked?" — resolved to nothing, because the entity is `auth-service` and "auth" is not it. The skeleton still answered it, and `--item` covers the gap, but a natural question got no expansion. Matching hyphen-separated components would fix it and would also match "store" against `token-store` | Dogfood (Phase 9), from how people actually phrase things. Over-inclusion is visible in one line of `context show`; a silent miss is not visible at all, which argues for the looser rule — but not before there is real phrasing to look at |