# Notes for the frontend build

**Status:** written at the end of backend Phase 8, before any frontend exists
**Audience:** whoever writes `FRONTEND.md` and the code under it
**Authority:** none. `contract.md` is the contract and the generated OpenAPI
document is authoritative for shapes. This file is the backend telling the
frontend what it has learned — the traits that are cheap to honour on day one
and expensive to retrofit in week six.

Nothing here is a requirement the contract does not already imply. It is the
implications, written down, because the contract states rules and this states
what they do to a UI.

---

## 1. The client is generated. All of it.

`zettaplanner openapi > openapi.json` needs no database and no model server, by
design, so generating the client is a build step rather than an operation. D2 is
blunt about why: a generated JS client is a generated lie, and a hand-written
TypeScript one drifts. **Never hand-write a request type.** When a shape looks
wrong, the fix is in the Go handler, not in a local interface that patches over
it — the moment those two disagree, the one that is wrong is the one nobody is
looking at.

The document carries the closed vocabularies as `enum`s (`status`, `priority`,
`severity`, node kinds, relations), so the generated types already refuse a value
the backend would refuse. Use them as the source for dropdowns. A hard-coded
option list is how a vocabulary quietly gains a seventh status.

---

## 2. Authentication is a cookie, and it is invisible to your code

`HttpOnly`, `SameSite=Lax`. There is no token to read, store, or attach: every
call is `credentials: 'include'` and that is the whole of it. A login page that
tries to keep the session in memory or in `localStorage` is building a second,
weaker copy of something the browser already does correctly.

Three consequences:

- **A 401 mid-session is real, not a glitch.** Changing a role or a password, or
  deactivating an account, revokes every live session it holds (§7.1) — a
  session must not outlast the authority it was issued under. Treat 401 as "you
  have been logged out, here is why", not as a retry.
- **`SameSite=Lax` is fine across localhost ports.** Ports are not part of a
  site, so a dev server on `:5173` and the API on `:8080` are same-site and the
  cookie is sent. What blocks you is CORS, not the cookie.
- **CORS is dev-only and off by default.** Set `ZP_DEV_ORIGIN=http://localhost:5173`
  and the backend allows exactly that one origin with credentials. In a
  deployment the SPA is served by the same binary (D2), so there is no CORS at
  all — do not build anything that depends on it existing.

---

## 3. Every error has one shape. Render the message, not your paraphrase.

```json
{ "error": { "code": "field_not_owned", "message": "...", "details": { ... }, "request_id": "01J..." } }
```

Codes are closed (§7.3). Two things follow, and the second one is the one that
gets skipped:

**Show the `request_id`.** It is in the body and in the `X-Request-Id` header,
and it is what turns "it broke" into a row in the audit log. Put it somewhere
copyable on every error surface.

**A denial's wording is a prompt layer** (§7.3, LumixAgents D34). Every 403 names
the owning role *and* the available route, in a sentence written to produce an
action:

> `start_date is manager-owned. Your effective role on this project is developer.
> Raise a proposal, or ask a manager.`

A UI that replaces this with "Permission denied" throws away the half that tells
somebody what to do next — and the measured result of leading with the action
rather than the reason is that people take it. Render `message` verbatim and
attach the route in `details` to a button.

---

## 4. Field ownership: ask, do not duplicate

`GET /api/v1/projects/{pid}/field-ownership` publishes §4.2's table, read from
the same function the write path enforces. Use it to disable controls and to
explain why they are disabled.

**It is a hint and never a control.** `scope` is the reason: `project` means the
role is enough, but `assigned`, `author` and `reporter` depend on the specific
row, and only the view knows whether this item is the caller's. Combine
`writable_by_role` with what you know about the row; expect the backend to
re-check everything anyway (D7).

The rule to hold on to: **a disabled button is a courtesy, not a permission.**
Design every write path to handle a 403 gracefully, including the ones you
believe you have greyed out — because the same endpoint is called by an agent
that never saw your UI.

The nastiest case to get right is the split object. A work item's `status` and
`progress_pct` belong to its assignee; everything else about it belongs to a
manager. So the item detail view is *two* editable regions with different
owners, and which one is yours changes per item. Deciding that once, centrally,
is much cheaper than discovering it per screen.

---

## 5. Real-time is one SSE stream per project, and reconnect is your problem

`GET /api/v1/projects/{pid}/events` (§7.2). One connection per project, not per
view. `Last-Event-ID` is honoured where the backend can replay; where it cannot
it emits `resync` and **you refetch** — treat that event as authoritative and do
not try to patch state forward across a gap.

The events are a nudge to refetch, not a replacement for the data. `plan.changed`
carries a subject kind and id, not the new object. Resist building a store that
applies event payloads as state: it becomes a second, subtly different copy of
the backend's model, and it is wrong exactly when two people are editing.

`turn.*` and `message.delta` are the exception — those *are* the data, because a
streaming answer has nowhere else to come from.

---

## 6. The canvas: leases, not locks

Editing a diagram means acquiring a lease and heartbeating it (§7.1, D9). Three
things a UI has to do that are easy to leave out:

- **Heartbeat on a timer, and stop when the tab is hidden or the editor is
  closed.** A lease held by a dead tab blocks a diagram for its whole TTL.
- **`lease.lost` means stop.** Block the save, show what changed, offer the
  diff. Do not silently re-acquire — the whole point is that the person finds
  out before they overwrite somebody.
- **Save is whole-state and carries `base_revision`.** A 409 with
  `stale_base_revision` is not an error to retry; it is "somebody else saved,
  and your base is gone".

Layout is a **separate** write (`PUT /diagrams/{id}/layout`), needs no lease, and
produces no revision. Dragging a box is not a semantic change and must never
create one. Correspondingly, coordinates exist only in the layout store: nothing
in the semantic API has an `x` (D5), and the frontend owns the auto-layout for a
diagram whose `layout_mode` is still `auto`.

---

## 7. Provenance is a first-class visual

Every node, edge, work item and plan field carries where it came from (§3.7).
This is not metadata for a tooltip — for `vlm_extracted` it is the difference
between "the system knows this" and "a model read it off a photograph and
nobody has checked". Ingested elements carry a `confidence` too, and a low one
is a **first-class value**, not a guess to hide: the backend deliberately stores
a low-confidence reading rather than inventing a label.

Two rules:

- **Show `vlm_extracted` differently from everything else**, and make correcting
  it one click. That correction is the highest-signal input the re-evaluate flow
  will ever get.
- **A node's effective provenance is the stronger of its own and its entity's**
  (D56). The backend computes this; do not re-derive it from the node alone or
  you will show "unchecked" on something a person fixed yesterday.

The digest has the same shape of hazard: `harness` is computed and cannot drift,
`model` is a hypothesis, and `model_stale` says whether anything has happened
since it was written. **Never render the two the same way.** In LumixAgents a
model asked to summarise produced dead-ends describing events that never
happened. Present them as "what the model currently thinks", visibly separate
from the counts.

---

## 8. Governance is a queue, and rejection is a form

Proposals are the agent's route to anything it may not write directly. `GET
/proposals/{id}` returns the changes, the **current** conflicts, `can_approve`,
and `approval_blocked_because` — all four are for rendering; the last is a
sentence you can show as-is.

- **Conflicts are computed on read.** A proposal that was clean when the queue
  loaded may not be clean now. Re-fetch before showing the approve button, and
  expect a 409 anyway — it names the field, the value it was raised against and
  the value now.
- **A 409 on approve is terminal for that proposal.** The status becomes
  `superseded`; nothing was applied. The next action is "raise it again against
  the current state", not "try again".
- **Rejection requires a rationale, minimum 8 characters** (§5.2), and this is
  not a validation quirk to route around with "no". The rationale is fed back
  into the agent's context, and the measured effect is that it stops
  re-proposing what was refused. A UI that makes the box feel like an obstacle
  gets "nope" typed into it, and the loop that made this worth building does not
  close. Prompt for the *reason*, and say why it matters.

---

## 9. Deletion is soft, and history keeps resolving

Every DELETE sets `deleted_at` (§6.5). `GET /work-items/{id}` returns a deleted
item, with the field set, on purpose: a worklog or an audit entry mentioning
ZP-14 has to keep resolving. Render deleted things as deleted rather than
404-ing them, and never reissue a ref.

There is deliberately **no route to edit or delete a worklog.** A worklog is
corrected by adding another. Do not build an edit affordance and then discover
it; make "add a correction" the obvious action.

`dropped` is not deleted. It is the decision *not* to do something, kept
because it is worth keeping — a different visual from `done` and from gone.

---

## 10. Dates are calendar days

`YYYY-MM-DD`, everywhere, for `start_date` and `due_date`. They are not
instants: "due 2026-08-08" does not mean midnight in anybody's timezone, and
putting one through a `Date` and back out is how a due date moves a day for
everyone not sitting on the server. Keep them as strings end to end; use a date
picker that emits one.

Timestamps (`created_at`, `at`, `decided_at`) *are* instants, RFC3339, and
should be rendered in the viewer's zone.

---

## 11. Small things that are annoying to discover late

- **An unset priority is not `normal`** (§3.5a). An untriaged item and one
  judged ordinary are different facts, and the dashboard counts the first
  separately. Do not default the control.
- **`GET /projects/{pid}/dashboard`** answers the project-view question in one
  call — totals, milestone rollups, load per person, what is overdue or due
  soon, what is blocked and by what, plus the date warnings. It exists so the
  view does not render six moments; prefer it to six requests.
- **Warnings are computed, and nothing cascades.** Moving a blocker's date
  produces a warning and no rescheduling, deliberately. The UI must not offer
  to "fix" it by moving dates in bulk.
- **`blocked_by` on the dashboard can be empty** for an item whose status is
  `blocked`. That means somebody set the status and never recorded what by —
  it is worth surfacing as a prompt, not hiding as a null.
- **Spec export is a POST that returns `text/markdown`** with a
  `Content-Disposition` filename, and it writes an audit entry. Treat it as a
  download, and expect the generated client to hand you a blob rather than JSON.
- **Turns serialize per project** (§5.3). A second prompt queues *visibly* — the
  response carries its queue position. Show it; a send button that appears to do
  nothing is the worst version of this.
- **Item-scoped and diagram-scoped threads are globally readable** (§3.6). Scope
  controls context assembly and placement, never visibility. Do not build a
  privacy affordance the backend does not have.

---

## 12. What the backend does not do for you

Stated plainly so nobody waits for it:

- **No auto-layout.** `layout_mode` is `auto` until the first manual drag, and
  computing the automatic arrangement is the frontend's job. The backend stores
  coordinates and has opinions about none of them.
- **No auto-scheduling, no critical path.** [DEFERRED], §3.5.
- **No notifications, no email.** [DEFERRED], §10.
- **No pagination on most list endpoints.** They are project-scoped and bounded
  by `limit` where it matters (audit, worklogs, turns, revisions). If a list
  needs paging in dogfood, that is a backend change — ask for it rather than
  fetching everything and slicing.
- **No per-field validation endpoint.** Validation errors come back from the
  write, in the envelope, with `details.errors` naming the location. The
  closed-vocabulary failures carry a message that teaches the valid values;
  render it.
