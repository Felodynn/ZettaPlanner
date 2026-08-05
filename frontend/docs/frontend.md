# ZettaPlanner — Frontend Build Plan

**Status:** Draft v1
**Owner:** Solo technical founder (Felodynn)
**Implementation partner:** Claude Code CLI (sequential, phase-gated), with
Chrome integration for visual QA
**Required reading:** `CONTRACT.md` in full, plus this document's §1–§3. Read one
phase at a time.

---

## 0. How to use this document

1. **The backend is built first and is running.** Every phase here is developed
   against the real API, never a mock. If an endpoint's behaviour surprises you,
   the contract or the backend is wrong — report it, do not work around it.
2. Build **one phase at a time**. Do not start phase N+1 until phase N's exit
   criteria pass.
3. **The TypeScript API client is generated from the backend's OpenAPI document
   and is never hand-edited.** A hand-written type is a lie waiting to be
   believed.
4. `CONTRACT.md` is authoritative. Where this document and the contract
   disagree, the contract wins.
5. Every phase has a **manual test procedure** to be run in a real browser.
   Screenshot-based QA supplements it; it does not replace it.
6. Append `Dn` decision entries per phase, including the honest ones.

---

## 1. Shape and stack

| Concern | Choice | Why |
|---|---|---|
| Framework | React 18 + TypeScript, strict | Contract D2. Strict mode non-negotiable |
| Build | Vite; output embedded in the Go binary | One deployment artifact |
| Routing | React Router | Deep-linkable items, diagrams, proposals |
| Server state | TanStack Query | Cache invalidation driven by SSE events |
| Client state | Zustand, only for the diagram editor's working copy | Everything else is server state. Resist a global store |
| Canvas | `@xyflow/react` (React Flow) | Agreed. The one place real complexity lives |
| Auto-layout | dagre | Computed layout, no coordinates in `auto` mode |
| Styling | Tailwind + a small primitive set (Radix or shadcn/ui) | Not a design system project |
| Forms | react-hook-form + zod, schemas derived from generated types | Client validation must mirror server validation, not invent it |
| API client | generated from OpenAPI | §0.3 |

```
web/src/
  api/            generated client (do not edit) + thin query hooks
  events/         SSE connection, reconnect, event→invalidation map
  auth/
  layout/         app shell, project switcher, nav
  features/
    project/
    diagram/      viewer, editor, proposal-diff overlay
    ingest/       upload, per-page review, merge review
    plan/         milestones, items, board, item detail
    chat/         the single global thread
    proposals/    queue, diff review
    dashboard/    my tasks, project overview
    audit/
  components/     shared primitives
  lib/
```

### 1.1 Three rules that shape everything

**1. The canvas is one component, used three times.** Ingest review, direct
editing, and proposal-diff review are the same graph renderer in three modes.
Building three canvases means three sets of bugs and three divergent
interpretations of the node vocabulary. Extract the shared renderer in Phase 3
and consume it in Phases 4 and 6.

**2. Coordinates never leave the layout endpoint.** The semantic save
(`POST /diagrams/{id}/revisions`) carries nodes, edges, and relations. Layout
goes to `PUT /diagrams/{id}/layout`, separately. If a coordinate is ever
included in a revision payload, that is a contract violation on the client side
(contract D5).

**3. SSE drives invalidation, not local optimism about other people's writes.**
Your own edits may be optimistic. Another actor's changes arrive as events and
invalidate queries. Never assume the server agrees with your cache.

---

## 2. Phased build plan

Every phase: **implement → manual test in a browser → exit criteria → commit →
next.**

---

### Phase F0 — Scaffold, client, auth

**Build:** Vite + React + TS strict; Tailwind and primitives; OpenAPI client
generation wired into the build; router; login page; session handling with
401 → login redirect; app shell with project switcher; error boundary; a single
toast/error surface that renders the contract's error shape (§7.3) including
`request_id`.

**Notes:**

- **Render the backend's message verbatim** on a `403 field_not_owned`. It names
  the owning role and the route forward, and it was written to be read. Do not
  replace it with "Permission denied."
- Client generation is a build step, committed output. A stale client that
  compiles is worse than a missing one.

**Manual test:** log in as admin, manager, developer. Confirm the shell shows
role-appropriate navigation. Force a 403 by hand and read the toast.

**Exit criteria:**
- [ ] TS strict, no `any` in `src/api` or feature code
- [ ] Client generated from the live OpenAPI document; regeneration is one command
- [ ] Login, logout, session expiry redirect
- [ ] Error surface renders code, message, and `request_id`
- [ ] Backend's denial text shown verbatim

---

### Phase F1 — Project shell and the single thread

The chat is the product's spine. Build it early, live with it.

**Build:** project overview; the **single global project thread**; message list
with virtualization; composer; SSE connection with reconnect and
`Last-Event-ID`; streaming assistant messages; turn queue state; actor
attribution on every message.

**Notes:**

- **Every message shows who sent it** — human name or agent name. In a globally
  visible thread where managers read developers' turns, an unattributed message
  is unusable.
- **The queue must be visible.** When another user's turn is running, the
  composer shows who is running and the position, and the send button reflects
  it. Silence here reads as a broken app.
- Streaming appends `message.delta`; `message.complete` replaces with the
  canonical server message. Never treat accumulated deltas as the record.
- On reconnect, replay from `Last-Event-ID`; on `resync`, refetch the tail.
- **Never render the digest's model-written fields as fact.** If shown at all,
  they are labelled as the agent's current hypothesis (contract §6.3).

**Manual test:** open the same project in two browsers as two users. Post from
both. Confirm the second queues visibly, both see the same thread in the same
order, and killing the network mid-stream recovers without duplicated text.

**Exit criteria:**
- [ ] One thread per project, globally visible, correctly ordered
- [ ] Streaming smooth; deltas never persist as the record
- [ ] Reconnect resumes without duplicates or gaps
- [ ] Queue state visible with the running user named
- [ ] Every message attributed to a named actor
- [ ] Long threads scroll without jank at 500+ messages

---

### Phase F2 — Plan views

**Build:** milestone list; work item table with filters (assignee, status,
milestone); item detail with subtasks, checklist, worklogs, deviations,
dependencies, entity refs; "My tasks" dashboard; warnings surface.

**Notes:**

- **Field ownership must be visible before it is enforced.** A field the current
  user does not own is rendered read-only with a hint naming the owning role and
  offering "ask the planner to propose this." Letting a developer type into a
  due-date field and then rejecting the save is the worst of both.
- Worklogs are append-only in the UI: add, never edit.
- Warnings are computed server-side. Render them; do not recompute or cache.
- The item detail page is the most-visited screen in the product. Its density
  matters more than its polish.

**Manual test:** as a developer, work a real item end to end — status, subtasks,
checklist, a worklog, a deviation. Confirm every manager-owned field is visibly
read-only *before* you try to change it. As a manager, set a date that violates a
dependency and confirm the warning appears.

**Exit criteria:**
- [ ] Item detail covers every plan object in the contract
- [ ] Unowned fields read-only with the owning role named and a route offered
- [ ] My-tasks dashboard usable as a daily driver
- [ ] Warnings surfaced on both item and project views
- [ ] Filters deep-linkable

---

### Phase F3 — Diagram viewer ⚠️ highest risk starts here

**Build:** the shared graph renderer; node components for all 11 kinds; edge
rendering for all 9 relations; dagre auto-layout; boundary containment; pan,
zoom, fit, minimap; selection with a detail panel; confidence and provenance
badges; a read-only YAML view.

**Notes:**

- **The renderer is the component you will reuse three times** (§1.1). Its props
  are: semantic graph, optional layout, mode (`view | edit | diff`), and change
  handlers. Get this interface right before adding editing.
- **Node kinds must be visually distinct at a glance** — shape and iconography,
  not colour alone. A diagram where `service` and `component` look alike is a
  diagram nobody trusts.
- Low-confidence elements are visually marked in every mode, not only in ingest
  review. A hand-corrected diagram still contains extracted elements.
- `auto` mode stores nothing. dagre runs on render; the same graph must produce
  the same picture.
- Boundary nodes are React Flow parent nodes with children constrained inside.
  Get containment right here, in view mode, before editing makes it harder.

**Manual test:** view a diagram ingested in backend Phase 3, on a laptop screen.
Can you tell a `queue` from a `datastore` without reading labels? Do boundaries
contain their children? Does a 40-node graph lay out readably?

**Exit criteria:**
- [ ] All 11 node kinds and 9 relations render distinctly
- [ ] dagre layout deterministic and readable at 40+ nodes
- [ ] Boundary containment correct
- [ ] Confidence and provenance visible in every mode
- [ ] Renderer accepts `view | edit | diff` and is used only through that interface
- [ ] 60fps pan/zoom at 100 nodes

---

### Phase F4 — Diagram editor

The single most complex screen. Budget accordingly.

**Build:** edit mode with lease acquisition and heartbeat; palette constrained
to the closed vocabulary; node create/rename/delete; edge draw with relation
picker; boundary reparenting; drag with `manual` mode flip; undo/redo on the
local working copy; atomic save; stale-base conflict UI; lease-lost handling;
unplaced-node indication.

**Notes:**

- **Acquire the lease on entering edit mode, not on first change.** A user who
  makes twenty edits and *then* discovers someone else holds the lease has lost
  twenty edits.
- **Heartbeat while the tab is visible.** On `visibilitychange` to hidden,
  continue briefly then release. A laptop lid closing must not hold a lease for
  an hour.
- **`lease.lost` blocks saving immediately** and offers: view the diff, or copy
  the working copy out. Never silently keep accepting edits that cannot land.
- **The palette is the schema** (contract D6). No free-form node type, no
  free-form relation. If the vocabulary feels short, that is a `Dn` entry and a
  backend migration, not a text input.
- **The first node drag flips `layout_mode` to `manual`.** Tell the user, once,
  plainly: auto-layout will no longer rearrange this diagram. Re-running it later
  is destructive and behind a confirm.
- **Undo/redo is local to the working copy** and does not survive save. Server
  history is revisions.
- On `409 stale_base_revision`, show what changed on the server beside what
  changed locally. Never auto-merge. Offer: discard mine, or copy mine out and
  reload.
- Semantic save and layout save are **separate calls** (§1.1 rule 2). Layout may
  save on a debounce; semantic saves only on explicit Save.

**Manual test:** take a real hand-drawn ingest, correct it fully — rename boxes,
fix relations, add a missed service, group into a boundary — and save. Then
force conflicts: two tabs, one holding a lease; let a lease expire by sleeping
the machine; save from a stale base.

**Exit criteria:**
- [ ] Lease acquired on entry, heartbeated, released on exit and on hide
- [ ] `lease.lost` blocks save and offers recovery
- [ ] Palette restricted to the closed vocabulary
- [ ] Semantic and layout saves separate; **no coordinate ever in a revision payload**
- [ ] First drag flips to `manual` with a clear one-time notice
- [ ] Stale base shows both sides and never auto-merges
- [ ] Undo/redo correct across create, delete, reparent, and edge changes
- [ ] Unplaced nodes visually distinct until first moved
- [ ] Correcting a real ingested diagram is faster than redrawing it

---

### Phase F5 — Ingest review

**Build:** upload (drag-drop, multi-image, PDF); ingest progress via
`ingest.progress`; per-page review showing the source image beside the extracted
graph; confidence-sorted correction list; the **merge review** step for
cross-page references; accept.

**Notes:**

- **Source image beside extraction, always.** The reviewer's job is comparison;
  making them switch views makes them approve without checking.
- **Sort corrections by confidence ascending.** The reviewer's attention is
  finite and should be spent where the model was least sure. Do not present 40
  elements as a flat list.
- **Merge review is its own step**, not a footnote on the last page. Cross-page
  arrows are where extraction fails; that is exactly the step people skip if it
  is not made explicit.
- A page that failed extraction is retryable alone, without re-uploading.
- Accept is deliberate and shows what will be created or changed.

**Manual test:** photograph a real notebook page badly and ingest it. Then a
3-page PDF with arrows crossing pages. Time yourself from upload to a diagram
you would keep — if it is slower than redrawing from scratch, the review UI is
wrong, not the model.

**Exit criteria:**
- [ ] Multi-image and PDF upload with live progress
- [ ] Source image and extraction shown side by side
- [ ] Corrections sorted by ascending confidence
- [ ] Merge review is an explicit, unskippable step
- [ ] Per-page retry without re-upload
- [ ] Accept previews its effect
- [ ] End-to-end faster than redrawing by hand

---

### Phase F6 — Proposals and diff review

**Build:** proposal queue; plan-change diff (field-level before/after); **diagram
diff rendered on the canvas** in `diff` mode; per-change accept/reject where the
backend allows; approve/reject with the mandatory rationale; conflict display for
superseded proposals; the re-evaluate trigger.

**Notes:**

- **Diagram proposals are reviewed on the canvas**, not as a list of JSON
  changes. Added, removed, and changed nodes and edges are visually distinct on
  the same renderer used everywhere else (§1.1).
- **The rejection rationale field is required and the reason is worth telling the
  user:** it is fed back to the agent so it stops re-proposing what was refused.
  A user who understands that writes a better rationale.
- Re-evaluate must be clearly labelled as producing a proposal, never a change.
  "Ask the model to review my edits" — the outcome is a review, always.
- Superseded proposals show what moved underneath.

**Manual test:** ask the agent for a plan change and a diagram change. Review
both. Reject one with a rationale, then ask again and confirm the agent's answer
accounts for it. Modify a target and confirm the stale proposal reports
superseded with the conflict shown.

**Exit criteria:**
- [ ] Queue shows pending proposals with actor, rationale, and target
- [ ] Diagram diffs render on the canvas with add/remove/change distinct
- [ ] Plan diffs show field-level before/after
- [ ] Rejection rationale required, with its purpose explained
- [ ] Superseded proposals show the conflict
- [ ] Re-evaluate visibly produces a proposal, never a write
- [ ] Approval authority matches §5.1; a developer sees why they cannot approve

---

### Phase F7 — Audit, export, polish

**Build:** audit view filterable by subject, actor, and date, with agent writes
attributed to both agent and instructing user; spec export with preview and
download; project settings; keyboard shortcuts on the item and canvas views;
empty and loading states throughout.

**Notes:**

- **Render transition actions as transitions**, not as generic updates with two
  JSONB blobs. `blocked → in_progress, 4d 6h` is the line a person reads;
  `work_item.updated` with a diff is the line they skip. The backend records
  these distinctly (contract §6.5.2) precisely so this view can.
- Deleted objects still appear in audit history. A deleted work item's trail must
  remain reachable — that is the whole reason nothing is hard-deleted.
- **No CSV export button in v0.** The download is v0.1 (contract §6.5); shipping
  a half-shaped export now sets an expectation about its columns that the real
  design will have to break.

**Manual test:** answer "who changed this due date, and why" for a real item,
using only the UI. If it takes more than three clicks, the audit view is wrong.

**Exit criteria:**
- [ ] Audit answers who/what/when/on-whose-behalf in three clicks
- [ ] Spec export previews before download
- [ ] No unstyled loading or empty states on any primary screen
- [ ] Keyboard navigation on item list and canvas

---

### Phase F8 — Dogfood

**No new features.** Use it. See `BACKEND.md` Phase 9 — the two dogfood phases
run together.

**Exit criteria:**
- [ ] Two real projects driven through the UI by more than one person
- [ ] A hand-drawn HLD ingested, corrected, and used in real planning
- [ ] Written-down opinions about what is wrong, as `Dn` entries

---

## 3. Visual QA with Chrome integration

Screenshot QA catches layout regressions. It does not catch the things that
matter here. Use it for: canvas rendering at multiple viewports, node-kind
distinguishability, diff-mode legibility, empty and loading states, long-content
overflow.

It cannot check: whether the lease heartbeat survives a sleeping laptop, whether
SSE reconnect duplicates messages, whether an unowned field was read-only
*before* the attempt, or whether correcting an ingest is faster than redrawing.
Those are hands-on, every time.

---

## 4. Open questions

| # | Question | Decide by |
|---|---|---|
| Q1 | Does the canvas need virtualization above ~200 nodes, or is React Flow enough? | F3, measure |
| Q2 | Item-scoped chat threads inline on the item page, or only in the global view? | F2, after using the global thread |
| Q3 | Is per-change accept on a diagram proposal genuinely useful, or does partial acceptance produce incoherent graphs? | F6 — if incoherent, all-or-nothing is correct |
| Q4 | Board view as well as table view for work items, or is the table enough? | F2, from dogfood |
| Q5 | Should the diagram editor auto-save layout, or require explicit save for both layers? | F4, from use |