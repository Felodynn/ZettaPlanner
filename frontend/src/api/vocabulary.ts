/**
 * GENERATED FILE — DO NOT EDIT.
 *
 * The closed vocabularies (D6, §3.3, §3.5a), read out of the same OpenAPI
 * document the types come from. Use these for every dropdown and every
 * exhaustive switch: a hand-kept option list drifts, and the drift is silent
 * until somebody's work item has a status no query finds again.
 *
 * Each array is `satisfies`-checked against the generated type, so a
 * vocabulary change in the backend breaks this build rather than the product.
 */

import type { components } from './schema'

type S = components['schemas']

/** A deployment-wide role, before any per-project override. */
export type Role = NonNullable<S["UserSummary"]["role"]>
export const ROLE_VALUES = ["admin", "manager", "developer"] as const satisfies readonly Role[]

/** Project lifecycle. */
export type ProjectStatus = NonNullable<S["ProjectSummary"]["status"]>
export const PROJECT_STATUS_VALUES = ["active", "archived"] as const satisfies readonly ProjectStatus[]

/** Shared by work items and subtasks (§3.5a). */
export type WorkItemStatus = NonNullable<S["WorkItemDetail"]["status"]>
export const WORK_ITEM_STATUS_VALUES = ["todo", "in_progress", "blocked", "review", "done", "dropped"] as const satisfies readonly WorkItemStatus[]

/** Deliberately coarser than an item status. */
export type MilestoneStatus = NonNullable<S["MilestoneSummary"]["status"]>
export const MILESTONE_STATUS_VALUES = ["planned", "active", "done", "dropped"] as const satisfies readonly MilestoneStatus[]

/** Unset is not `normal` — it means untriaged. */
export type Priority = NonNullable<S["WorkItemDetail"]["priority"]>
export const PRIORITY_VALUES = ["low", "normal", "high", "urgent"] as const satisfies readonly Priority[]

/** Deviation severity. */
export type Severity = NonNullable<S["DeviationSummary"]["severity"]>
export const SEVERITY_VALUES = ["low", "medium", "high"] as const satisfies readonly Severity[]

/** Worklog kind. */
export type WorklogKind = NonNullable<S["WorklogSummary"]["kind"]>
export const WORKLOG_KIND_VALUES = ["progress", "deviation", "note"] as const satisfies readonly WorklogKind[]

/** Between work items; `blocks` cycles are rejected. */
export type DependencyKind = NonNullable<S["DependencySummary"]["kind"]>
export const DEPENDENCY_KIND_VALUES = ["blocks", "relates"] as const satisfies readonly DependencyKind[]

/** The closed node vocabulary (D6, §3.3). */
export type NodeKind = NonNullable<S["NodeView"]["kind"]>
export const NODE_KIND_VALUES = ["system", "service", "component", "datastore", "queue", "external_system", "actor", "api", "job", "boundary", "note"] as const satisfies readonly NodeKind[]

/** The closed edge vocabulary (D6, §3.3). */
export type Relation = NonNullable<S["EdgeView"]["relation"]>
export const RELATION_VALUES = ["calls", "depends_on", "reads_from", "writes_to", "publishes_to", "subscribes_to", "contains", "deploys_to", "data_flow"] as const satisfies readonly Relation[]

/** Sequence diagrams are deferred to v0.1. */
export type DiagramLevel = NonNullable<S["DiagramSummary"]["level"]>
export const DIAGRAM_LEVEL_VALUES = ["context", "container", "component"] as const satisfies readonly DiagramLevel[]

/** The first manual drag flips auto to manual. */
export type LayoutMode = NonNullable<S["DiagramSummary"]["layout_mode"]>
export const LAYOUT_MODE_VALUES = ["auto", "manual"] as const satisfies readonly LayoutMode[]

/** Where an object came from (§3.7). */
export type Provenance = NonNullable<S["NodeView"]["provenance"]>
export const PROVENANCE_VALUES = ["vlm_extracted", "human_created", "human_edited", "agent_proposed", "agent_committed", "system"] as const satisfies readonly Provenance[]

/** Agents are principals (D11). */
export type ActorKind = NonNullable<S["AuditEntryView"]["actor_kind"]>
export const ACTOR_KIND_VALUES = ["human", "agent", "system"] as const satisfies readonly ActorKind[]

/** What a note is about. */
export type NoteKind = NonNullable<S["NoteView"]["kind"]>
export const NOTE_KIND_VALUES = ["overlap", "conflict", "missing", "clarification"] as const satisfies readonly NoteKind[]

/** Note lifecycle. */
export type NoteStatus = NonNullable<S["NoteView"]["status"]>
export const NOTE_STATUS_VALUES = ["open", "resolved", "dismissed"] as const satisfies readonly NoteStatus[]

/** Proposal lifecycle (§5.1). */
export type ProposalStatus = NonNullable<S["ProposalSummary"]["status"]>
export const PROPOSAL_STATUS_VALUES = ["pending", "approved", "rejected", "superseded"] as const satisfies readonly ProposalStatus[]

/** What a proposal touches. */
export type ProposalTarget = NonNullable<S["ProposalSummary"]["target_kind"]>
export const PROPOSAL_TARGET_VALUES = ["diagram", "plan", "mixed"] as const satisfies readonly ProposalTarget[]

/** One typed change within a proposal. */
export type ChangeOp = NonNullable<S["ProposalChange"]["op"]>
export const CHANGE_OP_VALUES = ["create", "update", "delete"] as const satisfies readonly ChangeOp[]

/** Agent turn lifecycle (§5.3). */
export type TurnStatus = NonNullable<S["TurnSummary"]["status"]>
export const TURN_STATUS_VALUES = ["queued", "running", "complete", "failed", "abandoned", "cancelled"] as const satisfies readonly TurnStatus[]

/** Who a message is from. */
export type MessageRole = NonNullable<S["MessageSummary"]["role"]>
export const MESSAGE_ROLE_VALUES = ["user", "assistant", "tool", "system"] as const satisfies readonly MessageRole[]

/** Scope drives context assembly, never visibility. */
export type ThreadScope = NonNullable<S["ThreadSummary"]["scope_kind"]>
export const THREAD_SCOPE_VALUES = ["project", "work_item", "diagram"] as const satisfies readonly ThreadScope[]

/** Ingest lifecycle. */
export type IngestStatus = NonNullable<S["IngestView"]["status"]>
export const INGEST_STATUS_VALUES = ["uploaded", "extracting", "merging", "review", "accepted", "failed"] as const satisfies readonly IngestStatus[]

/** Failure is per page, and so is retry. */
export type IngestPageStatus = NonNullable<S["IngestPageView"]["status"]>
export const INGEST_PAGE_STATUS_VALUES = ["pending", "extracting", "extracted", "failed"] as const satisfies readonly IngestPageStatus[]

/** What was uploaded. */
export type IngestSource = NonNullable<S["IngestView"]["source_kind"]>
export const INGEST_SOURCE_VALUES = ["image", "pdf"] as const satisfies readonly IngestSource[]

/** Computed on read and stored nowhere (§3.5). */
export type WarningKind = NonNullable<S["WarningSummary"]["kind"]>
export const WARNING_KIND_VALUES = ["blocker_date", "milestone_date"] as const satisfies readonly WarningKind[]

/** What a caller needs beyond the role. */
export type OwnershipScope = NonNullable<S["FieldRuleView"]["scope"]>
export const OWNERSHIP_SCOPE_VALUES = ["project", "assigned", "author", "reporter"] as const satisfies readonly OwnershipScope[]

/** The closed error set (§7.3). */
export type ErrorCode = NonNullable<S["ErrorBody"]["code"]>
export const ERROR_CODE_VALUES = ["unauthenticated", "forbidden", "field_not_owned", "not_found", "conflict", "lease_held", "stale_base_revision", "validation_failed", "turn_in_progress", "model_unavailable", "context_overflow", "internal"] as const satisfies readonly ErrorCode[]

