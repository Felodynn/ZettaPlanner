/**
 * GENERATED FILE — DO NOT EDIT.
 *
 * Emitted from the backend's OpenAPI document by `npm run api:generate`.
 * CONTRACT §7: that document is authoritative for request and response shapes.
 * If a shape here looks wrong, the fix is in the Go handler — a local
 * interface that patches over it is the copy nobody is looking at.
 *
 * Paths described: 58
 */

export interface paths {
    "/api/v1/admin/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Every account
         * @description Admin only.
         */
        get: operations["list-users"];
        put?: never;
        /**
         * Create an account
         * @description Admin only. The account's actor row is created in the same operation, so an audit entry can never reference a principal that does not exist.
         */
        post: operations["create-user"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/admin/users/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One account
         * @description Admin only, except that anyone may read their own.
         */
        get: operations["get-user"];
        put?: never;
        post?: never;
        /**
         * Remove an account
         * @description Admin only. Soft delete: the row is marked deleted and its sessions revoked, so every audit entry naming this account stays joinable (CONTRACT §6.5).
         */
        delete: operations["delete-user"];
        options?: never;
        head?: never;
        /**
         * Change an account
         * @description Admin only. Changing the role, the password, or deactivating the account revokes every live session it holds: a session must not outlast the authority it was issued under.
         */
        patch: operations["update-user"];
        trace?: never;
    };
    "/api/v1/auth/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in
         * @description Verifies a password and issues a session cookie. Any session the request already carried is revoked, so a session identifier never survives a login.
         */
        post: operations["login"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign out
         * @description Revokes the current session and clears the cookie. Revoked, not deleted: when a session ended stays answerable.
         */
        post: operations["logout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The signed-in user and their effective role on every visible project */
        get: operations["me"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/checklist/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Delete a checklist item
         * @description Soft.
         */
        delete: operations["delete-checklist-item"];
        options?: never;
        head?: never;
        /**
         * Tick, clear, or retitle a checklist item
         * @description Both directions are recorded as checklist.checked with before and after. A box ticked and then cleared is a signal, and recording only the tick hides it.
         */
        patch: operations["update-checklist-item"];
        trace?: never;
    };
    "/api/v1/deviations/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Delete a deviation
         * @description Soft, and manager-owned. The worklog that describes it is never deleted.
         */
        delete: operations["delete-deviation"];
        options?: never;
        head?: never;
        /**
         * Change or resolve a deviation
         * @description Split ownership: the reporter owns summary and severity, a manager owns the resolution. Setting a resolution resolves it; clearing one reopens it, and both are recorded as deviation.resolved.
         */
        patch: operations["update-deviation"];
        trace?: never;
    };
    "/api/v1/diagrams/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A diagram's semantic state, presentation state and current revision
         * @description The two layers are returned side by side and never merged. `layout` on a node is presentation and is not part of what any model is shown (D5).
         */
        get: operations["get-diagram"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/layout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Write presentation state
         * @description Takes no lease and produces no revision: moving a box is not a semantic change. The first write carrying a coordinate flips the diagram to `manual` permanently (§3.2). Any project member may move a node — a coordinate is not an owned field.
         */
        put: operations["save-diagram-layout"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/layout/reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Discard every stored coordinate and return to automatic layout
         * @description Destructive and irreversible: the hand-arranged positions are not recoverable from anything else. Requires confirm=true, which is why this is not a field on the layout endpoint (§3.2).
         */
        post: operations["reset-diagram-layout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/lease": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Take the edit session
         * @description Leases, not locks or CRDTs (D9). Returns 409 lease_held naming the current holder and when their session lapses. A lapsed lease is taken over automatically, so a crashed editor cannot block a diagram indefinitely.
         */
        post: operations["acquire-diagram-lease"];
        /** Give up the edit session */
        delete: operations["release-diagram-lease"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/lease/heartbeat": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Extend the edit session
         * @description Fails once the session has lapsed, so the client finds out with the work still on screen rather than at save time.
         */
        post: operations["heartbeat-diagram-lease"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/reevaluate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Ask the vision model to look at this diagram again
         * @description **Returns a Proposal, never a write** (§5.4) — including for a manager who could have written it directly. A stray click must not rewrite a diagram somebody corrected by hand.
         *
         *     The model is shown the source pages this diagram was ingested from, one call per page, alongside the current graph with **per-element provenance**: what a human created or edited is presented as a decision rather than a candidate, and any correction that would overwrite one is dropped and reported in the rationale. A diagram with no ingest behind it is re-read from its graph alone.
         *
         *     Synchronous, and slow on a multi-page document.
         */
        post: operations["reevaluate-diagram"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/revisions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A diagram's revision history
         * @description One revision per human save. Layout writes produce none.
         */
        get: operations["list-diagram-revisions"];
        put?: never;
        /**
         * Save the whole diagram atomically
         * @description Send the complete semantic state and the revision you started from. If the base has moved, the save is rejected with 409 stale_base_revision carrying the revisions that landed in between — never merged. Nodes are identified by entity slug; unknown slugs create catalog entities, and a plausible name collision records a Note rather than merging anything (§3.4).
         */
        post: operations["save-diagram-revision"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/diagrams/{id}/yaml": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The canonical semantic serialization
         * @description Byte-deterministic, and structurally incapable of containing coordinates: the serializer's only input type has no coordinate field, and it cannot import the layout store (D5, §6.2). The same bytes feed the model context and the optional git projection.
         */
        get: operations["get-diagram-yaml"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/entities/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An entity, where it appears, and what it is connected to
         * @description `dependents` answers "what depends on this" across every diagram in the project, by traversing typed edges. Structural relations (contains, deploys_to) are excluded: they say where something sits, not what needs it.
         */
        get: operations["get-entity"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/ingests/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An ingest's status, per-page extraction and merged payload
         * @description `pages[].extracted` is exactly what the model returned for that page, kept forever. `merged` is the separate reconciliation step: what the pages agreed on, what they disagreed about, and which arrows ran off a page and found nothing.
         *
         *     Every element carries a confidence, and `needs_review` says why one is flagged. An element the model could not read has `legible: false` and cannot be accepted without a name.
         */
        get: operations["get-ingest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/ingests/{id}/accept": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Apply a reviewed ingest to a diagram
         * @description Creates a diagram, or adds to one. **Additive**: nothing is removed. A drawing is evidence that these things exist, never evidence that everything else does not, and a whole-state save from a photograph would delete every node the drawing happened not to include.
         *
         *     An element the model could not read cannot reach the canvas under an invented name. Every one of them needs a `resolutions` entry giving a name or setting `drop`; the refusal names each element and quotes whatever the model could make out.
         *
         *     Arrows that ran off a page and found nothing are recorded as Notes rather than drawn or discarded. Accepted elements carry provenance `vlm_extracted` and their confidence; anything you renamed carries `human_edited` and no confidence, because you read it.
         */
        post: operations["accept-ingest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/ingests/{id}/pages/{idx}/image": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The page image the model was shown
         * @description The normalized page: rotation applied, downscaled. Reviewing an extraction against the original photograph rather than against this one means comparing to something the model never saw.
         */
        get: operations["get-ingest-page-image"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/ingests/{id}/pages/{idx}/retry": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Re-read one page
         * @description Retry is per page because failure is per page. Re-running the whole ingest to fix the one page that was out of focus costs a model call for every page and discards extractions somebody has already reviewed. The merge re-runs afterwards.
         */
        post: operations["retry-ingest-page"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/milestones/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Delete a milestone
         * @description Soft. The work items under it survive and lose the pointer: deleting a grouping must not delete the work.
         */
        delete: operations["delete-milestone"];
        options?: never;
        head?: never;
        /**
         * Change a milestone
         * @description Every field in the body is resolved to its owning role before anything is written. A status move is recorded as milestone.status_changed, not as a generic update.
         */
        patch: operations["update-milestone"];
        trace?: never;
    };
    "/api/v1/milestones/{id}/spec-export": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Export a milestone as spec.md
         * @description Bundles intent, acceptance criteria, the constraints that are actually recorded — blockers in both directions, dates against the milestone's, open deviations, and anything unresolved or never human-checked in the architecture it touches — and the canonical YAML slice of the referenced entities (§6.2).
         *
         *     **Manual only, and one-way.** There is no trigger and nothing reads the document back in; the Lumix handoff is deferred to v0.1 (§10). The call is a POST because it writes a `spec.exported` audit entry: who took this outside and when is a question a retrospective asks, and it cannot be answered later if nobody recorded it.
         *
         *     The milestone form carries every work item under it, and its constraints name only the dependencies that leave the milestone — one inside it is already visible in the item list, and repeating it buries the ones that are somebody else's.
         */
        post: operations["export-milestone-spec"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/notes/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Resolve or dismiss a note
         * @description Manager-owned: deciding that two things are or are not the same is an architectural judgement.
         */
        patch: operations["update-note"];
        trace?: never;
    };
    "/api/v1/projects": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Projects the caller can see
         * @description An admin sees every project; anyone else sees the ones they are a member of.
         */
        get: operations["list-projects"];
        put?: never;
        /**
         * Create a project
         * @description Admin only. The creator is added as a manager of the new project in the same operation.
         */
        post: operations["create-project"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One project
         * @description A caller who is not a member gets not_found rather than forbidden: distinguishing them would let anyone enumerate which projects exist.
         */
        get: operations["get-project"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Change a project
         * @description Every field in the body is resolved to its owning role before anything is written (CONTRACT §4.2). Project settings are manager-owned, so a developer gets field_not_owned naming the route forward.
         */
        patch: operations["update-project"];
        trace?: never;
    };
    "/api/v1/projects/{pid}/audit": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Who changed what, including every agent write
         * @description Readable by any member of the project, not only by managers — the same call §3.6 makes about the conversation. A record the people it describes cannot read is not a control.
         *
         *     State transitions are their own actions (`work_item.status_changed`, not a generic update), so "how long did this sit in blocked" is a query over this rather than a diff of two JSONB blobs.
         */
        get: operations["get-project-audit"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/dashboard": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A project's rollup: load, dates, blockers, warnings
         * @description One call rather than six, because a view assembled from six calls renders six moments and the one that matters — is anything overdue — arrives last. Nothing here is stored or cached; every figure is an aggregate over live rows.
         */
        get: operations["get-dashboard"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/deviations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's deviations */
        get: operations["list-deviations"];
        put?: never;
        /**
         * Record a divergence between the plan and reality
         * @description Anyone on the project may raise one. Supplying `body` files the worklog describing it and links the two.
         */
        post: operations["raise-deviation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/diagrams": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's diagrams */
        get: operations["list-diagrams"];
        put?: never;
        /**
         * Create a diagram
         * @description Manager-owned. Created in `auto` layout mode: no coordinates are stored until somebody drags a node.
         */
        post: operations["create-diagram"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/digest": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The project digest: computed facts and the model's hypothesis, kept apart
         * @description The harness half is recomputed from the database on every read and cannot drift. The model half is a hypothesis and is marked stale the moment anything is recorded in the project after it was written. Never render the two the same way.
         */
        get: operations["get-digest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/entities": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The project's architecture catalog
         * @description Entities are project-level identities. One entity may appear in several diagrams as the same thing.
         */
        get: operations["list-entities"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/field-ownership": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The field ownership table (§4.2), as this caller sees it
         * @description Read from the same table the write path enforces, so it cannot describe a rule that is not applied.
         *
         *     **This is a hint for rendering, never a control.** `scope` values other than `project` depend on the specific row — whether the caller is that item's assignee, that worklog's author, that deviation's reporter — which only the frontend knows per row. Every write is checked again on the server (D7). Use it to disable controls and to explain why; never to decide whether a write is allowed.
         */
        get: operations["get-field-ownership"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/ingests": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's ingests, newest first */
        get: operations["list-ingests"];
        put?: never;
        /**
         * Upload a drawing to be read into the architecture graph
         * @description Send photographs as several `files` parts, or one PDF. One ingest is one document: a PDF and loose photographs in the same upload would be merged across drawings that were never on the same desk, so it is refused.
         *
         *     Extraction runs in the background — one model call per page, never one for the document — and the response returns immediately with the ingest in `uploaded`. Poll `GET /api/v1/ingests/{id}` for progress. Nothing here writes to a diagram; accept does.
         *
         *     Any project member may upload: a photograph binds nobody to anything. Accepting it is the write, and that needs diagram authority.
         */
        post: operations["create-ingest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/members": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Project members and their effective roles */
        get: operations["list-project-members"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/members/{uid}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Add a member or change their project role
         * @description Admin only. The project role overrides the user's global role for this project, including downward.
         */
        put: operations["put-project-member"];
        post?: never;
        /**
         * Remove a member
         * @description Admin only. The membership row is removed; the add and the removal both stay in the audit log.
         */
        delete: operations["remove-project-member"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/milestones": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's milestones */
        get: operations["list-milestones"];
        put?: never;
        /**
         * Create a milestone
         * @description Manager-owned (CONTRACT §4.2). Seq defaults to one past the last milestone.
         */
        post: operations["create-milestone"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/notes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Clarifications: overlaps, conflicts, missing pieces
         * @description Notes are how the backend and the agent say "these two may be the same thing" without merging them. They live in Postgres and in assembled context, and are never chunked into the vector store.
         */
        get: operations["list-notes"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/proposals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A project's proposals
         * @description The review queue. A pending proposal has changed nothing. Conflicts are computed per proposal on the detail route rather than here, because computing them for a whole list means reading every target the project has.
         */
        get: operations["list-proposals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/threads": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A project's conversations
         * @description Every thread is readable by every project member, including managers reading developers' turns. Scope controls context assembly and UI placement, never visibility (§3.6).
         */
        get: operations["list-threads"];
        put?: never;
        /**
         * Open the thread for a scope, creating it on first use
         * @description Idempotent: there is exactly one thread per scope, and asking twice returns the same one.
         */
        post: operations["ensure-thread"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/turns": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's agent turns, newest first */
        get: operations["list-turns"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/warnings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Dependency and date violations
         * @description Computed on every call and stored nowhere: a stored warning is stale the moment a date moves, and reads identically whether it is true now or was true in April. Nothing is rescheduled — a cascade that is right 90% of the time destroys the dates somebody set by hand.
         */
        get: operations["list-warnings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/work-items": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A project's work items
         * @description Filterable by assignee, status and milestone. `milestone=none` selects the items in no milestone.
         */
        get: operations["list-work-items"];
        put?: never;
        /**
         * Create a work item
         * @description Manager-owned. The ref is issued by the server from a monotonic counter and is never reissued.
         */
        post: operations["create-work-item"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/projects/{pid}/worklogs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A project's worklogs, newest first */
        get: operations["list-worklogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/proposals/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One proposal with its ordered changes and its current conflicts
         * @description `conflicts` is computed now, not stored. A non-empty list means approving this would supersede it rather than apply it: the state it was raised against has moved, and reconciling the two is the judgement you were being asked to make.
         */
        get: operations["get-proposal"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/proposals/{id}/approve": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve a proposal and apply it as one revision
         * @description Atomic: every change lands or none does. If any change's recorded premise has moved, the proposal becomes `superseded`, the conflict is named, and **nothing is applied** — never partially, never auto-merged.
         *
         *     Managers and admins may approve anything. A developer may approve only a proposal whose every change is one they could have written directly.
         */
        post: operations["approve-proposal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/proposals/{id}/reject": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Refuse a proposal, with a rationale
         * @description The rationale is required. It is fed into assembled context (§5.2), which is what stops the planner proposing the same thing again — and what stops the person concluding the tool does not listen.
         *
         *     Anybody on the project may refuse. Refusing changes nothing, and requiring a manager to be present to record a reason is how the reason stops being recorded.
         */
        post: operations["reject-proposal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/sub-tasks/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Delete a subtask
         * @description Soft.
         */
        delete: operations["delete-sub-task"];
        options?: never;
        head?: never;
        /** Change a subtask */
        patch: operations["update-sub-task"];
        trace?: never;
    };
    "/api/v1/threads/{id}/messages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A thread's transcript
         * @description Chat is transcript, never memory (D8): nothing here is replayed to a model. A turn receives assembled state and the current prompt.
         */
        get: operations["list-messages"];
        put?: never;
        /**
         * Post a message and enqueue an agent turn
         * @description The message always lands. The turn queues behind whatever else is running on the project (§5.3) and the response carries its position — a second user's prompt is queued and shown as queued, never run concurrently.
         */
        post: operations["post-message"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/turns/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** One turn, with its outcome and its cost */
        get: operations["get-turn"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/turns/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Stop a queued or running turn
         * @description Anybody on the project may cancel. A turn holds the project's only slot, and requiring the person who started it to be present is how a queue stays blocked all afternoon. Cancellation is a real outcome, not a flavour of failure.
         */
        post: operations["cancel-turn"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One work item with its subtasks, checklist, dependencies and entity refs
         * @description A deleted item is still returned, carrying deleted_at. Deletion is soft and the ref is never reissued, so every worklog and audit entry naming it still resolves.
         */
        get: operations["get-work-item"];
        put?: never;
        post?: never;
        /**
         * Delete a work item
         * @description Soft. Its subtasks, checklist, worklogs and dependencies stay, and its ref is never reissued.
         */
        delete: operations["delete-work-item"];
        options?: never;
        head?: never;
        /**
         * Change a work item
         * @description Field ownership is enforced per field (CONTRACT §4.2). A developer may write status and progress_pct on items assigned to them; everything else is manager-owned and a developer's route to it is a proposal. A manager may not write status either — it is the developer's report of reality.
         */
        patch: operations["update-work-item"];
        trace?: never;
    };
    "/api/v1/work-items/{id}/checklist": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Add a checklist item */
        post: operations["add-checklist-item"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}/dependencies": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Replace an item's dependencies
         * @description Manager-owned. Cycles in `blocks` are rejected at write time with the offending path named. An omitted key leaves that kind alone; an empty array clears it.
         */
        put: operations["set-dependencies"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}/entity-refs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Replace an item's references to the architecture
         * @description Set explicitly and never derived (§3.5). They exist so context assembly can pull the relevant architecture slice when the item is discussed.
         */
        put: operations["set-entity-refs"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}/spec-export": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Export a work item as spec.md
         * @description Bundles intent, acceptance criteria, the constraints that are actually recorded — blockers in both directions, dates against the milestone's, open deviations, and anything unresolved or never human-checked in the architecture it touches — and the canonical YAML slice of the referenced entities (§6.2).
         *
         *     **Manual only, and one-way.** There is no trigger and nothing reads the document back in; the Lumix handoff is deferred to v0.1 (§10). The call is a POST because it writes a `spec.exported` audit entry: who took this outside and when is a question a retrospective asks, and it cannot be answered later if nobody recorded it.
         */
        post: operations["export-work-item-spec"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}/sub-tasks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Add a subtask
         * @description Dev-owned, scoped to the assignee of the parent item (§4.2: within own item).
         */
        post: operations["create-sub-task"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/work-items/{id}/worklogs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Append a worklog to an item
         * @description Anyone on the project may write one. There is no edit and no delete: correct by adding another.
         */
        post: operations["add-worklog"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/health": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Liveness and dependency status
         * @description Reports the schema version and the state of each dependency. Model validation is not re-run here; it happens at startup and fails the process.
         */
        get: operations["health"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        AcceptIngestInputBody: {
            /** @description Add to this diagram. Omit to create one. */
            diagram_id?: string;
            /**
             * @description Required when creating a diagram. A sketch almost never says which altitude it is drawn at, and guessing it wrong mislabels the diagram permanently.
             * @enum {string}
             */
            level?: "context" | "container" | "component";
            /** @description The new diagram's name. Defaults to the title read off the page. */
            name?: string;
            resolutions?: components["schemas"]["Item4"][] | null;
            /** @description Stable identity. Derived from the name when omitted. */
            slug?: string;
            /** @description Revision summary. Appears in the diagram's history. */
            summary?: string;
        };
        AcceptResponse: {
            diagram_id: string;
            diagram_slug: string;
            /** @description Elements you discarded during review. */
            dropped: string[] | null;
            /** Format: int64 */
            edges_added: number;
            /** Format: int64 */
            nodes_added: number;
            /** @description Clarifications this accept recorded: unplaceable arrows, page disagreements, elements accepted below the confidence threshold. */
            notes: components["schemas"]["NoteView"][] | null;
            /** Format: int64 */
            revision: number;
        };
        "Add-checklist-itemRequest": {
            /** Format: int64 */
            seq?: number;
            text: string;
        };
        "Add-worklogRequest": {
            body: string;
            /** @enum {string} */
            kind?: "progress" | "deviation" | "note";
        };
        AssigneeLoad: {
            /** Format: int64 */
            blocked: number;
            /** Format: int64 */
            in_progress: number;
            name?: string;
            /** Format: int64 */
            open: number;
            /** Format: int64 */
            overdue: number;
            /** @description Absent on the unassigned row */
            user_id?: string;
        };
        AuditEntryView: {
            action: string;
            actor_id: string;
            /** @enum {string} */
            actor_kind: "human" | "agent" | "system";
            /** @description Which model an agent was running. The same agent on a different model is a different reader of the same prompt */
            actor_model?: string;
            actor_name: string;
            after?: unknown;
            /** Format: date-time */
            at: string;
            before?: unknown;
            id: string;
            /** @description True when this records a state change with a meaningful before and after, rather than an edit */
            is_transition: boolean;
            /** @description The instructing user when an agent acted */
            on_behalf_of?: string;
            on_behalf_of_name?: string;
            /** @description Groups one logical operation. Fourteen entries sharing one is one proposal applied, not an afternoon of edits */
            request_id: string;
            subject_id?: string;
            subject_kind: string;
        };
        BlockedItemView: {
            blocked_by: string[] | null;
            id: string;
            ref: string;
            title: string;
        };
        ChecklistItemSummary: {
            checked: boolean;
            /** @description Kept after an un-tick: who ticked it and when survives somebody clearing the box */
            checked_at?: string;
            id: string;
            /** Format: int64 */
            seq: number;
            text: string;
        };
        ChecklistPatchBody: {
            checked?: boolean;
            text?: string;
        };
        Conflict: {
            detail: string;
            kind: string;
            name: string;
            pages: number[] | null;
        };
        "Create-diagramRequest": {
            /** @enum {string} */
            level: "context" | "container" | "component";
            name: string;
            /** @description Optional. Derived from the name when omitted, and never changed afterwards. */
            slug?: string;
        };
        "Create-milestoneRequest": {
            due_date?: string;
            intent?: string;
            name: string;
            /** Format: int64 */
            seq?: number;
            start_date?: string;
            /** @enum {string} */
            status?: "planned" | "active" | "done" | "dropped";
        };
        "Create-projectRequest": {
            description?: string;
            /** @description Upper-case letters and digits; prefixes every work item ref */
            key: string;
            name: string;
        };
        "Create-sub-taskRequest": {
            assignee_id?: string;
            /** Format: int64 */
            seq?: number;
            /** @enum {string} */
            status?: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title: string;
        };
        "Create-userRequest": {
            display_name: string;
            /** Format: email */
            email: string;
            is_active?: boolean;
            /** @description At least 12 characters. Length, not character classes. */
            password: string;
            /** @enum {string} */
            role: "admin" | "manager" | "developer";
        };
        "Create-work-itemRequest": {
            acceptance_criteria?: string;
            assignee_id?: string;
            due_date?: string;
            intent?: string;
            milestone_id?: string;
            /** @enum {string} */
            priority?: "low" | "normal" | "high" | "urgent";
            start_date?: string;
            /** @enum {string} */
            status?: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title: string;
        };
        DashboardBody: {
            /** @description Open work per person. The entry with no user_id is the unassigned pile */
            assignees: components["schemas"]["AssigneeLoad"][] | null;
            blocked: components["schemas"]["BlockedItemView"][] | null;
            /** Format: int64 */
            horizon_days: number;
            milestones: components["schemas"]["MilestoneLoad"][] | null;
            totals: components["schemas"]["DashboardTotals"];
            /** @description Open items already overdue, plus those due within horizon_days */
            upcoming: components["schemas"]["DatedItemView"][] | null;
            /** @description The same computation as GET /warnings, from the same query */
            warnings: components["schemas"]["WarningSummary"][] | null;
        };
        DashboardTotals: {
            by_status: {
                [key: string]: number;
            };
            /** Format: int64 */
            open_deviations: number;
            open_deviations_by_severity: {
                [key: string]: number;
            };
            /** Format: int64 */
            open_items: number;
            /** Format: int64 */
            pending_proposals: number;
            /**
             * Format: int64
             * @description Open items with no priority. An unset priority is not normal (§3.5a) — it means nobody has looked
             */
            untriaged_open: number;
            /** Format: int64 */
            work_items: number;
        };
        DatedItemView: {
            assignee?: string;
            due_date: string;
            id: string;
            overdue: boolean;
            priority?: string;
            ref: string;
            status: string;
            title: string;
        };
        DependenciesOutputBody: {
            blocks: components["schemas"]["DependencySummary"][] | null;
            relates: components["schemas"]["DependencySummary"][] | null;
        };
        DependencySummary: {
            item_id: string;
            /** @enum {string} */
            kind: "blocks" | "relates";
            ref: string;
            title: string;
        };
        DeviationListOutputBody: {
            deviations: components["schemas"]["DeviationSummary"][] | null;
        };
        DeviationPatchBody: {
            /** @description Manager-owned. Setting it resolves the deviation; clearing it reopens */
            resolution?: string;
            /**
             * @description Reporter-owned
             * @enum {string}
             */
            severity?: "low" | "medium" | "high";
            /** @description Reporter-owned */
            summary?: string;
        };
        DeviationSummary: {
            /** Format: date-time */
            created_at: string;
            id: string;
            item_ref?: string;
            resolution?: string;
            resolved: boolean;
            resolved_at?: string;
            /** @enum {string} */
            severity: "low" | "medium" | "high";
            summary: string;
        };
        DiagramSummary: {
            id: string;
            /**
             * @description Flips to manual on the first coordinate write and does not flip back without an explicit destructive confirm.
             * @enum {string}
             */
            layout_mode: "auto" | "manual";
            /** @enum {string} */
            level: "context" | "container" | "component";
            name: string;
            /** Format: int64 */
            revision: number;
            /** @description Stable identity. Assigned at creation and never follows the name, so a rename is not a delete plus an add. */
            slug: string;
        };
        DiagramView: {
            edges: components["schemas"]["EdgeView"][] | null;
            id: string;
            /**
             * @description Flips to manual on the first coordinate write and does not flip back without an explicit destructive confirm.
             * @enum {string}
             */
            layout_mode: "auto" | "manual";
            lease?: components["schemas"]["LeaseView"];
            /** @enum {string} */
            level: "context" | "container" | "component";
            name: string;
            nodes: components["schemas"]["NodeView"][] | null;
            notes: components["schemas"]["NoteView"][] | null;
            /** Format: int64 */
            revision: number;
            /** @description Stable identity. Assigned at creation and never follows the name, so a rename is not a delete plus an add. */
            slug: string;
        };
        DigestBody: {
            /** @description Recomputed from the database on every read. Cannot drift */
            harness: components["schemas"]["DigestHarness"];
            /** @description Written by a model. A hypothesis, not a record — never present these as fact */
            model: components["schemas"]["DigestModel"];
            /** @description True when anything has been recorded in this project since the model fields were written */
            model_stale: boolean;
            /** Format: date-time */
            updated_at: string;
        };
        DigestHarness: {
            active_milestone?: string;
            blocked_refs: string[] | null;
            by_status: {
                [key: string]: number;
            };
            /** Format: int64 */
            diagrams: number;
            /** Format: int64 */
            entities: number;
            /**
             * Format: int64
             * @description How much spill is retrievable. Zero means layer 3 will find nothing
             */
            indexed_chunks: number;
            /** Format: date-time */
            last_activity_at?: string;
            /** Format: int64 */
            open_deviations: number;
            open_deviations_by_severity: {
                [key: string]: number;
            };
            /** Format: int64 */
            open_items: number;
            /** Format: int64 */
            open_notes: number;
            recent_decisions: string[] | null;
            recently_rejected: string[] | null;
            /** Format: int64 */
            unassigned_open: number;
            /** Format: int64 */
            work_items: number;
        };
        DigestModel: {
            current_hypothesis?: string;
            dead_ends?: string[] | null;
            open_questions?: string[] | null;
        };
        EdgeView: {
            attrs: {
                [key: string]: unknown;
            };
            /** Format: float */
            confidence?: number;
            /** @description Source node's entity slug */
            from: string;
            id: string;
            label?: string;
            provenance: string;
            /** @enum {string} */
            relation: "calls" | "depends_on" | "reads_from" | "writes_to" | "publishes_to" | "subscribes_to" | "contains" | "deploys_to" | "data_flow";
            to: string;
            /** @description Presentation only. */
            waypoints?: components["schemas"]["Point"][] | null;
        };
        "Ensure-threadRequest": {
            /** @enum {string} */
            scope_kind?: "project" | "work_item" | "diagram";
            /** @description A work item ref (ZP-14) or a diagram slug. Omit for the project thread */
            scope_ref?: string;
        };
        EntityDetailView: {
            attrs: {
                [key: string]: unknown;
            };
            /** @description What depends on this entity, across every diagram. */
            dependents: components["schemas"]["RelatedView"][] | null;
            depends_on: components["schemas"]["RelatedView"][] | null;
            id: string;
            kind: string;
            name: string;
            notes: components["schemas"]["NoteView"][] | null;
            placements: components["schemas"]["PlacementSummary"][] | null;
            slug: string;
            summary?: string;
        };
        EntityRefsOutputBody: {
            implements: components["schemas"]["EntityRefSummary"][] | null;
        };
        EntityRefSummary: {
            entity_id: string;
            name: string;
            /** @enum {string} */
            relation: "implements";
            slug: string;
        };
        EntitySummary: {
            attrs: {
                [key: string]: unknown;
            };
            id: string;
            kind: string;
            name: string;
            slug: string;
            summary?: string;
        };
        Error: {
            error: components["schemas"]["ErrorBody"];
        };
        ErrorBody: {
            /**
             * @description Machine-readable error code
             * @enum {string}
             */
            code: "unauthenticated" | "forbidden" | "field_not_owned" | "not_found" | "conflict" | "lease_held" | "stale_base_revision" | "validation_failed" | "turn_in_progress" | "model_unavailable" | "context_overflow" | "internal";
            /** @description Structured detail specific to the code */
            details?: {
                [key: string]: unknown;
            };
            /** @description Human-readable explanation. For a denial this is prompt text: it leads with the route forward and names the owning role. */
            message: string;
            /** @description Correlates this failure with the audit log and the server logs */
            request_id: string;
        };
        ExtractedEdge: {
            /** Format: float */
            confidence: number;
            continues_off_page?: boolean;
            from: string;
            label?: string;
            relation: string;
            to: string;
        };
        ExtractedNode: {
            /** Format: float */
            confidence: number;
            kind: string;
            legible: boolean;
            name: string;
            parent?: string;
            summary?: string;
            verbatim?: string;
        };
        FieldOwnershipBody: {
            /** @enum {string} */
            effective_role: "admin" | "manager" | "developer";
            objects: components["schemas"]["ObjectOwnershipView"][] | null;
        };
        FieldRuleView: {
            field: string;
            needs_relationship: boolean;
            /** @enum {string} */
            owner: "admin" | "manager" | "developer";
            /**
             * @description What relationship the caller needs beyond the role. 'project' means membership is enough
             * @enum {string}
             */
            scope: "project" | "assigned" | "author" | "reporter";
            writable_by_role: boolean;
        };
        "Get-project-auditResponse": {
            entries: components["schemas"]["AuditEntryView"][] | null;
        };
        HealthBody: {
            checks: components["schemas"]["HealthCheck"][] | null;
            /**
             * Format: int64
             * @description Highest applied migration; 0 means the database has not been migrated
             */
            schema_version: number;
            /**
             * @description ok when every check passed, degraded otherwise
             * @enum {string}
             */
            status: "ok" | "degraded";
            /** Format: int64 */
            uptime_seconds: number;
            version: string;
        };
        HealthCheck: {
            detail?: string;
            name: string;
            /** @enum {string} */
            status: "ok" | "failing" | "skipped";
        };
        IngestPageView: {
            /** Format: int64 */
            attempts: number;
            /** Format: int64 */
            duration_ms?: number;
            error_class?: string;
            error_detail?: string;
            extracted?: components["schemas"]["PageExtraction"];
            /** Format: date-time */
            extracted_at?: string;
            /**
             * Format: int64
             * @description 0-based and dense. A failed page keeps its slot.
             */
            idx: number;
            model?: string;
            raw?: string;
            /** @enum {string} */
            status: "pending" | "extracting" | "extracted" | "failed";
            /** Format: int64 */
            tokens_in?: number;
            /** Format: int64 */
            tokens_out?: number;
        };
        IngestSummary: {
            /** Format: date-time */
            created_at: string;
            /** @description Set once the ingest is accepted. */
            diagram_id?: string;
            error_class?: string;
            error_detail?: string;
            id: string;
            /** Format: int64 */
            page_count: number;
            source_filename?: string;
            /** @enum {string} */
            source_kind: "image" | "pdf";
            /** @enum {string} */
            status: "uploaded" | "extracting" | "merging" | "review" | "accepted" | "failed";
            /** Format: date-time */
            updated_at: string;
        };
        IngestView: {
            counts?: components["schemas"]["ReviewCounts"];
            /** Format: date-time */
            created_at: string;
            /** @description Set once the ingest is accepted. */
            diagram_id?: string;
            error_class?: string;
            error_detail?: string;
            id: string;
            merged?: components["schemas"]["Merged"];
            /** Format: int64 */
            page_count: number;
            pages: components["schemas"]["IngestPageView"][] | null;
            source_filename?: string;
            /** @enum {string} */
            source_kind: "image" | "pdf";
            /** @enum {string} */
            status: "uploaded" | "extracting" | "merging" | "review" | "accepted" | "failed";
            /** Format: date-time */
            updated_at: string;
        };
        Item: {
            attrs?: {
                [key: string]: unknown;
            };
            /** Format: float */
            confidence?: number;
            /** @enum {string} */
            kind: "system" | "service" | "component" | "datastore" | "queue" | "external_system" | "actor" | "api" | "job" | "boundary" | "note";
            /** @description Renames the entity on this diagram only. */
            label_override?: string;
            name: string;
            /** @description Containment parent's slug. Only a boundary or a system may contain. */
            parent?: string;
            /** @enum {string} */
            provenance?: "vlm_extracted" | "human_created" | "human_edited" | "agent_proposed" | "agent_committed" | "system";
            /** @description Entity slug. This is the node's identity; an unknown one creates a catalog entity. */
            slug: string;
            summary?: string;
        };
        Item1: {
            attrs?: {
                [key: string]: unknown;
            };
            /** Format: float */
            confidence?: number;
            from: string;
            label?: string;
            provenance?: string;
            /** @enum {string} */
            relation: "calls" | "depends_on" | "reads_from" | "writes_to" | "publishes_to" | "subscribes_to" | "contains" | "deploys_to" | "data_flow";
            to: string;
        };
        Item2: {
            collapsed?: boolean;
            /** Format: double */
            h?: number;
            node_id: string;
            /** Format: double */
            w?: number;
            /** Format: double */
            x?: number;
            /** Format: double */
            y?: number;
        };
        Item3: {
            edge_id: string;
            waypoints: components["schemas"]["Point"][] | null;
        };
        Item4: {
            /** @description Discard this element and every arrow touching it. */
            drop?: boolean;
            /** @enum {string} */
            kind?: "system" | "service" | "component" | "datastore" | "queue" | "external_system" | "actor" | "api" | "job" | "boundary" | "note";
            /** @description What it actually says. Supplying one marks the element human_edited and drops the model's confidence, because you read it. */
            name?: string;
            /** @description The merged element's slug. Unreadable boxes get a positional placeholder such as unreadable-p2-1. */
            slug: string;
        };
        LeaseView: {
            /** Format: int64 */
            base_revision: number;
            /** Format: date-time */
            expires_at: string;
            /**
             * Format: int64
             * @description How often the client should heartbeat. Published so the frontend does not hard-code a number that later disagrees with the server.
             */
            heartbeat_seconds: number;
            held_by_you: boolean;
            /** @enum {string} */
            holder_kind: "human" | "agent" | "system";
            holder_name: string;
        };
        "List-diagram-revisionsResponse": {
            revisions: components["schemas"]["RevisionView"][] | null;
        };
        "List-diagramsResponse": {
            diagrams: components["schemas"]["DiagramSummary"][] | null;
        };
        "List-entitiesResponse": {
            entities: components["schemas"]["EntitySummary"][] | null;
        };
        "List-ingestsResponse": {
            ingests: components["schemas"]["IngestSummary"][] | null;
        };
        "List-notesResponse": {
            notes: components["schemas"]["NoteView"][] | null;
        };
        LoginInputBody: {
            /**
             * Format: email
             * @description The account's email address
             */
            email: string;
            /** @description The account's password */
            password: string;
        };
        MeBody: {
            projects: components["schemas"]["ProjectRoleSummary"][] | null;
            user: components["schemas"]["UserSummary"];
        };
        MemberListOutputBody: {
            members: components["schemas"]["MemberSummary"][] | null;
        };
        MemberSummary: {
            display_name: string;
            /** @enum {string} */
            effective_role: "admin" | "manager" | "developer";
            email: string;
            /** @enum {string} */
            global_role: "admin" | "manager" | "developer";
            is_active: boolean;
            /**
             * @description The per-project override
             * @enum {string}
             */
            project_role: "admin" | "manager" | "developer";
            user_id: string;
        };
        Merged: {
            conflicts?: components["schemas"]["Conflict"][] | null;
            edges: components["schemas"]["MergedEdge"][] | null;
            level?: string;
            nodes: components["schemas"]["MergedNode"][] | null;
            notes?: string[] | null;
            pages_failed?: number[] | null;
            pages_read: number[] | null;
            title?: string;
        };
        MergedEdge: {
            /** Format: float */
            confidence: number;
            cross_page?: boolean;
            from: string;
            label?: string;
            needs_review: boolean;
            pages: number[] | null;
            relation: string;
            review_reason?: string;
            to: string;
            unresolved?: boolean;
        };
        MergedNode: {
            /** Format: float */
            confidence: number;
            kind: string;
            legible: boolean;
            name: string;
            needs_review: boolean;
            pages: number[] | null;
            parent?: string;
            review_reason?: string;
            slug: string;
            summary?: string;
            verbatim?: string;
        };
        MessageListOutputBody: {
            messages: components["schemas"]["MessageSummary"][] | null;
        };
        MessageSummary: {
            actor_name?: string;
            content: string;
            /** Format: date-time */
            created_at: string;
            id: string;
            /** @enum {string} */
            role: "user" | "assistant" | "tool" | "system";
            /** Format: int64 */
            seq: number;
            tool_calls?: components["schemas"]["ToolCallDetail"][] | null;
            turn_id?: string;
        };
        MilestoneListOutputBody: {
            milestones: components["schemas"]["MilestoneSummary"][] | null;
        };
        MilestoneLoad: {
            /** Format: int64 */
            blocked_count: number;
            /** Format: int64 */
            done_count: number;
            due_date?: string;
            id: string;
            /** Format: int64 */
            item_count: number;
            name: string;
            /** Format: int64 */
            open_count: number;
            /** Format: int64 */
            overdue_count: number;
            /** Format: int64 */
            seq: number;
            start_date?: string;
            /** @enum {string} */
            status: "planned" | "active" | "done" | "dropped";
        };
        MilestonePatchBody: {
            /** @description YYYY-MM-DD, or "" to clear */
            due_date?: string;
            intent?: string;
            name?: string;
            /** Format: int64 */
            seq?: number;
            /** @description YYYY-MM-DD, or "" to clear */
            start_date?: string;
            /** @enum {string} */
            status?: "planned" | "active" | "done" | "dropped";
        };
        MilestoneSummary: {
            /** @description YYYY-MM-DD; absent when unset */
            due_date?: string;
            id: string;
            intent?: string;
            /** Format: int64 */
            item_count: number;
            name: string;
            /** Format: int64 */
            open_count: number;
            provenance: string;
            /** Format: int64 */
            seq: number;
            /** @description YYYY-MM-DD; absent when unset */
            start_date?: string;
            /** @enum {string} */
            status: "planned" | "active" | "done" | "dropped";
        };
        NodeView: {
            attrs: {
                [key: string]: unknown;
            };
            /**
             * Format: float
             * @description Set for vlm_extracted elements. Low confidence is a first-class value, never a guess.
             */
            confidence?: number;
            entity_id: string;
            id: string;
            /** @enum {string} */
            kind: "system" | "service" | "component" | "datastore" | "queue" | "external_system" | "actor" | "api" | "job" | "boundary" | "note";
            /** @description Presentation only. Never sent to a model. */
            layout?: components["schemas"]["PlacementView"];
            name: string;
            /** @description Containment parent's slug. Only a boundary or a system may contain. */
            parent?: string;
            /** @enum {string} */
            provenance: "vlm_extracted" | "human_created" | "human_edited" | "agent_proposed" | "agent_committed" | "system";
            slug: string;
            summary?: string;
        };
        NoteView: {
            body: string;
            /** Format: date-time */
            created_at: string;
            id: string;
            /** @enum {string} */
            kind: "overlap" | "conflict" | "missing" | "clarification";
            /** @enum {string} */
            status: "open" | "resolved" | "dismissed";
            subject_id?: string;
            subject_kind: string;
            subject_slug?: string;
        };
        ObjectOwnershipView: {
            all_fields?: components["schemas"]["FieldRuleView"];
            fields: components["schemas"]["FieldRuleView"][] | null;
            kind: string;
        };
        PageExtraction: {
            edges: components["schemas"]["ExtractedEdge"][] | null;
            level?: string;
            nodes: components["schemas"]["ExtractedNode"][] | null;
            notes?: string[] | null;
            title?: string;
        };
        PlacementSummary: {
            diagram_id: string;
            diagram_name: string;
            diagram_slug: string;
            level: string;
            node_id: string;
        };
        PlacementView: {
            collapsed: boolean;
            /** Format: double */
            h?: number;
            /** @description The node arrived without coordinates and was placed near its neighbours. The UI must make it visually distinct; the flag clears on the first human move. */
            unplaced: boolean;
            /** Format: double */
            w?: number;
            /** Format: double */
            x?: number;
            /** Format: double */
            y?: number;
        };
        Point: {
            /** Format: double */
            x: number;
            /** Format: double */
            y: number;
        };
        "Post-messageRequest": {
            content: string;
        };
        PostMessageOutputBody: {
            message: components["schemas"]["MessageSummary"];
            turn: components["schemas"]["TurnSummary"];
        };
        ProjectListOutputBody: {
            projects: components["schemas"]["ProjectSummary"][] | null;
        };
        ProjectPatchBody: {
            description?: string;
            git_enabled?: boolean;
            name?: string;
            settings?: {
                [key: string]: unknown;
            };
            /** @enum {string} */
            status?: "active" | "archived";
        };
        ProjectRoleSummary: {
            /** @enum {string} */
            effective_role: "admin" | "manager" | "developer";
            /**
             * @description The per-project override, when one is set
             * @enum {string}
             */
            override_role?: "admin" | "manager" | "developer";
            project_id: string;
            project_key: string;
            project_name: string;
        };
        ProjectSummary: {
            description?: string;
            /** @description Whether the one-way git projection is on. Off by default (D10) */
            git_enabled: boolean;
            id: string;
            /** @description The prefix of every work item ref in this project, such as ZP in ZP-14 */
            key: string;
            name: string;
            settings: {
                [key: string]: unknown;
            };
            /** @enum {string} */
            status: "active" | "archived";
        };
        ProposalChange: {
            after?: {
                [key: string]: unknown;
            };
            /** @description The state when this was raised. Phase 7 supersedes the proposal if it has moved */
            before?: {
                [key: string]: unknown;
            };
            /** @enum {string} */
            op: "create" | "update" | "delete";
            /** Format: int64 */
            seq: number;
            target: string;
            target_id?: string;
        };
        ProposalConflict: {
            /** @description What it holds now */
            actual?: string;
            /** @description What a human calls it: ZP-14, redis-cache, the milestone name */
            element: string;
            /** @description What it held when the proposal was raised */
            expected?: string;
            field?: string;
            /** @description Set instead of field when the whole target is the problem */
            reason?: string;
            /**
             * Format: int64
             * @description Which change in the proposal
             */
            seq: number;
            target: string;
        };
        ProposalListOutputBody: {
            proposals: components["schemas"]["ProposalSummary"][] | null;
        };
        ProposalSummary: {
            approval_blocked_because?: string;
            can_approve?: boolean;
            changes: components["schemas"]["ProposalChange"][] | null;
            conflicts?: components["schemas"]["ProposalConflict"][] | null;
            /** Format: date-time */
            created_at: string;
            decided_by?: string;
            decision_rationale?: string;
            id: string;
            /** @description The instructing user when an agent raised it */
            on_behalf_of?: string;
            raised_by?: string;
            rationale: string;
            /** @enum {string} */
            status: "pending" | "approved" | "rejected" | "superseded";
            /** @enum {string} */
            target_kind: "diagram" | "plan" | "mixed";
            title: string;
            turn_id?: string;
        };
        "Put-project-memberRequest": {
            /** @enum {string} */
            role: "admin" | "manager" | "developer";
        };
        "Raise-deviationRequest": {
            /** @description Files a worklog alongside the deviation and links the two */
            body?: string;
            /** @enum {string} */
            severity?: "low" | "medium" | "high";
            summary: string;
            work_item_id?: string;
        };
        ReevaluateOutputBody: {
            message: string;
            model?: string;
            /**
             * Format: int64
             * @description Source pages re-read, one model call each. Zero means the diagram has no ingest behind it
             */
            pages_read: number;
            /** @description Absent when the model found nothing to change, which is a real answer */
            proposal?: components["schemas"]["ProposalSummary"];
            /** @description What was deliberately left out of this run */
            skipped?: string;
        };
        RejectInputBody: {
            /** @description Why you are refusing. Required — it becomes context the planner reads */
            rationale: string;
        };
        RelatedView: {
            /**
             * Format: int64
             * @description Hops from the starting entity. 1 is a direct relation.
             */
            depth: number;
            /** @description Which diagram that first hop was drawn in. */
            diagram_slug: string;
            entity_id: string;
            kind: string;
            label?: string;
            name: string;
            /** @description The relation on the first hop. */
            relation: string;
            slug: string;
            summary?: string;
        };
        "Reset-diagram-layoutRequest": {
            /** @description Must be true. Every hand-set position on this diagram is discarded. */
            confirm: boolean;
        };
        ReviewCounts: {
            /** Format: int64 */
            conflicts: number;
            /** Format: int64 */
            edges: number;
            /** Format: int64 */
            illegible: number;
            /** Format: int64 */
            low_confidence: number;
            /** Format: int64 */
            needs_review: number;
            /** Format: int64 */
            nodes: number;
            /** Format: int64 */
            unresolved: number;
        };
        RevisionChanges: {
            edges_added?: string[] | null;
            edges_removed?: string[] | null;
            edges_updated?: string[] | null;
            nodes_added?: string[] | null;
            nodes_removed?: string[] | null;
            nodes_updated?: string[] | null;
        };
        RevisionView: {
            actor_id: string;
            changes: components["schemas"]["RevisionChanges"];
            /** Format: date-time */
            created_at: string;
            /** Format: int64 */
            seq: number;
            summary?: string;
        };
        "Save-diagram-layoutRequest": {
            edges?: components["schemas"]["Item3"][] | null;
            nodes?: components["schemas"]["Item2"][] | null;
        };
        "Save-diagram-layoutResponse": {
            /** @description True when this write was the one that made the diagram manual. Automatic layout stops from here. */
            flipped_to_manual: boolean;
            /** @enum {string} */
            layout_mode: "auto" | "manual";
        };
        SaveInputBody: {
            /**
             * Format: int64
             * @description The revision this edit started from. A mismatch is rejected, never merged.
             */
            base_revision: number;
            edges: components["schemas"]["Item1"][] | null;
            nodes: components["schemas"]["Item"][] | null;
            /** @description What this save changed, in a line. Appears in the revision log and the git commit. */
            summary?: string;
        };
        SaveResponse: {
            changes: components["schemas"]["RevisionChanges"];
            /** @description Clarifications this save produced — most often a possible duplicate entity, recorded rather than merged. */
            notes_raised: components["schemas"]["NoteView"][] | null;
            /** Format: int64 */
            revision: number;
        };
        "Set-dependenciesRequest": {
            /** @description Items this one must finish before */
            blocks?: string[];
            /** @description Worth reading together; no ordering, so cycles are fine */
            relates?: string[];
        };
        "Set-entity-refsRequest": {
            implements: string[] | null;
        };
        SubTaskPatchBody: {
            assignee_id?: string;
            /** Format: int64 */
            seq?: number;
            /** @enum {string} */
            status?: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title?: string;
        };
        SubTaskSummary: {
            assignee_id?: string;
            id: string;
            /** Format: int64 */
            seq: number;
            /** @enum {string} */
            status: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title: string;
        };
        ThreadListOutputBody: {
            threads: components["schemas"]["ThreadSummary"][] | null;
        };
        ThreadSummary: {
            id: string;
            /** Format: date-time */
            last_message_at?: string;
            /** Format: int64 */
            message_count: number;
            scope_id?: string;
            /** @enum {string} */
            scope_kind: "project" | "work_item" | "diagram";
            /** @description The item ref or diagram name this thread is about */
            scope_label?: string;
        };
        ToolCallDetail: {
            name: string;
            /** @description ok, denied, error or unknown */
            outcome?: string;
        };
        TurnListOutputBody: {
            turns: components["schemas"]["TurnSummary"][] | null;
        };
        TurnSummary: {
            /** Format: date-time */
            ended_at?: string;
            error_class?: string;
            error_detail?: string;
            /** @description Why it ended. no_output means it produced no write, no proposal and no handoff — a non-success, not an answer */
            finish_reason?: string;
            id: string;
            model?: string;
            /** Format: int64 */
            queue_position: number;
            /** Format: date-time */
            queued_at: string;
            requested_by?: string;
            /** Format: int64 */
            retries: number;
            /** Format: date-time */
            started_at?: string;
            /** @enum {string} */
            status: "queued" | "running" | "complete" | "failed" | "abandoned" | "cancelled";
            /** Format: int64 */
            steps: number;
            thread_id: string;
            /** Format: int64 */
            tokens_in: number;
            /** Format: int64 */
            tokens_out: number;
            /** Format: int64 */
            wall_ms: number;
        };
        "Update-noteRequest": {
            /** @enum {string} */
            status: "open" | "resolved" | "dismissed";
        };
        "Update-userRequest": {
            display_name?: string;
            is_active?: boolean;
            password?: string;
            /** @enum {string} */
            role?: "admin" | "manager" | "developer";
        };
        UserListOutputBody: {
            users: components["schemas"]["UserSummary"][] | null;
        };
        UserSummary: {
            display_name: string;
            email: string;
            id: string;
            is_active: boolean;
            /**
             * @description Deployment-wide role, before any per-project override
             * @enum {string}
             */
            role: "admin" | "manager" | "developer";
        };
        WarningListOutputBody: {
            warnings: components["schemas"]["WarningSummary"][] | null;
        };
        WarningSummary: {
            /**
             * Format: int64
             * @description How far the violation runs, always positive
             */
            days: number;
            item_date?: string;
            item_id: string;
            item_ref: string;
            item_title: string;
            /** @enum {string} */
            kind: "blocker_date" | "milestone_date";
            message: string;
            related_date?: string;
            related_id: string;
            /** @enum {string} */
            related_kind: "work_item" | "milestone";
            related_name: string;
            related_ref?: string;
        };
        WorkItemDetail: {
            acceptance_criteria?: string;
            assignee_id?: string;
            assignee_name?: string;
            blocked_by: components["schemas"]["DependencySummary"][] | null;
            /** @description Items this one must finish before */
            blocks: components["schemas"]["DependencySummary"][] | null;
            checklist: components["schemas"]["ChecklistItemSummary"][] | null;
            /** @description Set when the item has been deleted. Deletion is soft, and a deleted item stays readable so its history keeps resolving */
            deleted_at?: string;
            due_date?: string;
            id: string;
            /** @description Set explicitly, never derived (§3.5) */
            implements: components["schemas"]["EntityRefSummary"][] | null;
            intent?: string;
            milestone_id?: string;
            milestone_name?: string;
            /**
             * @description Absent means nobody has triaged it, which is not the same as normal
             * @enum {string}
             */
            priority?: "low" | "normal" | "high" | "urgent";
            /** Format: int64 */
            progress_pct: number;
            provenance: string;
            /** @description The human-facing handle, such as ZP-14. Issued once and never reissued */
            ref: string;
            relates: components["schemas"]["DependencySummary"][] | null;
            start_date?: string;
            /** @enum {string} */
            status: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            sub_tasks: components["schemas"]["SubTaskSummary"][] | null;
            title: string;
        };
        WorkItemListOutputBody: {
            work_items: components["schemas"]["WorkItemSummary"][] | null;
        };
        WorkItemPatchBody: {
            acceptance_criteria?: string;
            /** @description An id, or "" to unassign */
            assignee_id?: string;
            /** @description YYYY-MM-DD, or "" to clear */
            due_date?: string;
            intent?: string;
            /** @description An id, or "" to detach */
            milestone_id?: string;
            /** @enum {string} */
            priority?: "low" | "normal" | "high" | "urgent";
            /** Format: int64 */
            progress_pct?: number;
            /** @description YYYY-MM-DD, or "" to clear */
            start_date?: string;
            /** @enum {string} */
            status?: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title?: string;
        };
        WorkItemSummary: {
            acceptance_criteria?: string;
            assignee_id?: string;
            /** @description Set when the item has been deleted. Deletion is soft, and a deleted item stays readable so its history keeps resolving */
            deleted_at?: string;
            due_date?: string;
            id: string;
            intent?: string;
            milestone_id?: string;
            /**
             * @description Absent means nobody has triaged it, which is not the same as normal
             * @enum {string}
             */
            priority?: "low" | "normal" | "high" | "urgent";
            /** Format: int64 */
            progress_pct: number;
            provenance: string;
            /** @description The human-facing handle, such as ZP-14. Issued once and never reissued */
            ref: string;
            start_date?: string;
            /** @enum {string} */
            status: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            title: string;
        };
        WorklogListOutputBody: {
            worklogs: components["schemas"]["WorklogSummary"][] | null;
        };
        WorklogSummary: {
            actor_name?: string;
            body: string;
            /** Format: date-time */
            created_at: string;
            id: string;
            item_ref?: string;
            /** @enum {string} */
            kind: "progress" | "deviation" | "note";
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    "list-users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-user": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-userRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-user": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-user": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-user": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Update-userRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    login: {
        parameters: {
            query?: never;
            header?: {
                "User-Agent"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LoginInputBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MeBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    logout: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    me: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MeBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-checklist-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-checklist-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ChecklistPatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ChecklistItemSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-deviation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-deviation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeviationPatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviationSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-diagram": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DiagramView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "save-diagram-layout": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Save-diagram-layoutRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Save-diagram-layoutResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "reset-diagram-layout": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reset-diagram-layoutRequest"];
            };
        };
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "acquire-diagram-lease": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LeaseView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "release-diagram-lease": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "heartbeat-diagram-lease": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LeaseView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "reevaluate-diagram": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReevaluateOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-diagram-revisions": {
        parameters: {
            query?: {
                limit?: number;
            };
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["List-diagram-revisionsResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "save-diagram-revision": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SaveInputBody"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SaveResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-diagram-yaml": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-entity": {
        parameters: {
            query?: {
                /** @description How many hops to traverse. */
                depth?: number;
            };
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["EntityDetailView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-ingest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["IngestView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "accept-ingest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AcceptIngestInputBody"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AcceptResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-ingest-page-image": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
                idx: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Cache-Control"?: string;
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "retry-ingest-page": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
                idx: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Accepted */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["IngestView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-milestone": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-milestone": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["MilestonePatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MilestoneSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "export-milestone-spec": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Disposition"?: string;
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-note": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Update-noteRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NoteView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-projects": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-project": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-projectRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-project": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-project": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ProjectPatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-project-audit": {
        parameters: {
            query?: {
                action?: string;
                /** @description Actor id. Note this is an actor, not a user: an agent has its own */
                actor?: string;
                limit?: number;
                /** @description RFC3339 */
                since?: string;
                /** @description Subject kind, such as work_item or diagram */
                subject?: string;
                /** @description One thing's whole history */
                subject_id?: string;
                /** @description RFC3339 */
                until?: string;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Get-project-auditResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-dashboard": {
        parameters: {
            query?: {
                /** @description How far ahead to look for due dates. Overdue items are always included */
                horizon_days?: number;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DashboardBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-deviations": {
        parameters: {
            query?: {
                item?: string;
                /** @description Restrict to unresolved deviations */
                open?: boolean;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviationListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "raise-deviation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Raise-deviationRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviationSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-diagrams": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["List-diagramsResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-diagram": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-diagramRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DiagramSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-digest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DigestBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-entities": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["List-entitiesResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-field-ownership": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["FieldOwnershipBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-ingests": {
        parameters: {
            query?: {
                limit?: number;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["List-ingestsResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-ingest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "multipart/form-data": {
                    files: string[];
                };
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["IngestView"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-project-members": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MemberListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "put-project-member": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
                uid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Put-project-memberRequest"];
            };
        };
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "remove-project-member": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
                uid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-milestones": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MilestoneListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-milestone": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-milestoneRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MilestoneSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-notes": {
        parameters: {
            query?: {
                status?: "open" | "resolved" | "dismissed";
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["List-notesResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-proposals": {
        parameters: {
            query?: {
                status?: "pending" | "approved" | "rejected" | "superseded";
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProposalListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-threads": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ThreadListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "ensure-thread": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Ensure-threadRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ThreadSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-turns": {
        parameters: {
            query?: {
                limit?: number;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TurnListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-warnings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WarningListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-work-items": {
        parameters: {
            query?: {
                /** @description A user id */
                assignee?: string;
                /** @description A milestone id, or the literal none */
                milestone?: string;
                status?: "todo" | "in_progress" | "blocked" | "review" | "done" | "dropped";
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkItemListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-work-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-work-itemRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkItemDetail"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-worklogs": {
        parameters: {
            query?: {
                /** @description A work item id */
                item?: string;
                kind?: "progress" | "deviation" | "note";
                limit?: number;
                /** @description RFC 3339 instant; entries at or after it */
                since?: string;
            };
            header?: never;
            path: {
                pid: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorklogListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-proposal": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProposalSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "approve-proposal": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProposalSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "reject-proposal": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RejectInputBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProposalSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-sub-task": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-sub-task": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SubTaskPatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SubTaskSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "list-messages": {
        parameters: {
            query?: {
                /** @description Return the messages immediately preceding this sequence number */
                before?: number;
                limit?: number;
                /** @description Restrict to one turn */
                turn?: string;
            };
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessageListOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "post-message": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Post-messageRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PostMessageOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-turn": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TurnSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "cancel-turn": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TurnSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "get-work-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkItemDetail"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "delete-work-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "update-work-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WorkItemPatchBody"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkItemDetail"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "add-checklist-item": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Add-checklist-itemRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ChecklistItemSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "set-dependencies": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Set-dependenciesRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DependenciesOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "set-entity-refs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Set-entity-refsRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["EntityRefsOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "export-work-item-spec": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Disposition"?: string;
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "create-sub-task": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Create-sub-taskRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SubTaskSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    "add-worklog": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Add-worklogRequest"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorklogSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
    health: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["HealthBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
        };
    };
}
