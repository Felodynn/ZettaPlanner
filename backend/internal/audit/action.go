package audit

import (
	"sort"
	"strings"
)

// Action is the typed audit action taxonomy.
//
// It is a Go enum, not a string literal at each call site, and Write rejects
// any value not registered here. A handler that writes a raw string is a
// handler whose events cannot be found by the v0.1 export — and the export is
// the one thing that cannot be backfilled (D13).
//
// The distinction that matters is between transitions and generic updates.
// `work_item.status_changed` is not `work_item.updated`: bottleneck analysis is
// almost entirely transition analysis, and making the transition first-class
// means the export is a SELECT rather than a parser (§6.5 constraint 2).
type Action string

const (
	// ── identity and access ──────────────────────────────────────────────
	ActionUserCreated       Action = "user.created"
	ActionUserUpdated       Action = "user.updated"
	ActionUserDeleted       Action = "user.deleted"
	ActionUserRoleChanged   Action = "user.role_changed" // transition
	ActionSessionStarted    Action = "session.started"   // transition
	ActionSessionEnded      Action = "session.ended"     // transition
	ActionSessionRejected   Action = "session.rejected"  // transition
	ActionActorCreated      Action = "actor.created"
	ActionProjectCreated    Action = "project.created"
	ActionProjectUpdated    Action = "project.updated"
	ActionProjectDeleted    Action = "project.deleted"
	ActionMemberAdded       Action = "project_member.added"
	ActionMemberRoleChanged Action = "project_member.role_changed" // transition
	ActionMemberRemoved     Action = "project_member.removed"

	// ── architecture graph ───────────────────────────────────────────────
	ActionEntityCreated  Action = "catalog_entity.created"
	ActionEntityUpdated  Action = "catalog_entity.updated"
	ActionEntityDeleted  Action = "catalog_entity.deleted"
	ActionDiagramCreated Action = "diagram.created"
	ActionDiagramUpdated Action = "diagram.updated"
	ActionDiagramDeleted Action = "diagram.deleted"
	ActionDiagramRevised Action = "diagram.revised" // transition
	ActionLayoutModeSet  Action = "diagram.layout_mode_changed"
	ActionLeaseAcquired  Action = "diagram_lease.acquired"
	ActionLeaseReleased  Action = "diagram_lease.released"
	ActionLeaseExpired   Action = "diagram_lease.expired" // transition
	ActionNodeCreated    Action = "diagram_node.created"
	ActionNodeUpdated    Action = "diagram_node.updated"
	ActionNodeDeleted    Action = "diagram_node.deleted"
	ActionEdgeCreated    Action = "diagram_edge.created"
	ActionEdgeUpdated    Action = "diagram_edge.updated"
	ActionEdgeDeleted    Action = "diagram_edge.deleted"

	// ── ingest ───────────────────────────────────────────────────────────
	ActionIngestCreated   Action = "ingest.created"
	ActionIngestExtracted Action = "ingest.extracted" // transition
	ActionIngestMerged    Action = "ingest.merged"    // transition
	ActionIngestAccepted  Action = "ingest.accepted"  // transition
	ActionIngestFailed    Action = "ingest.failed"    // transition

	// ── plan ─────────────────────────────────────────────────────────────
	ActionMilestoneCreated       Action = "milestone.created"
	ActionMilestoneUpdated       Action = "milestone.updated"
	ActionMilestoneDeleted       Action = "milestone.deleted"
	ActionMilestoneStatusChanged Action = "milestone.status_changed" // transition

	ActionWorkItemCreated       Action = "work_item.created"
	ActionWorkItemUpdated       Action = "work_item.updated"
	ActionWorkItemDeleted       Action = "work_item.deleted"
	ActionWorkItemStatusChanged Action = "work_item.status_changed" // transition
	ActionWorkItemAssigned      Action = "work_item.assigned"       // transition
	ActionWorkItemMoved         Action = "work_item.milestone_changed"

	ActionSubTaskCreated       Action = "sub_task.created"
	ActionSubTaskUpdated       Action = "sub_task.updated"
	ActionSubTaskDeleted       Action = "sub_task.deleted"
	ActionSubTaskStatusChanged Action = "sub_task.status_changed" // transition

	ActionDependenciesSet Action = "item_dependencies.set"
	ActionEntityRefsSet   Action = "item_entity_refs.set"

	ActionChecklistCreated Action = "checklist.created"
	ActionChecklistUpdated Action = "checklist.updated"
	ActionChecklistDeleted Action = "checklist.deleted"
	// ActionChecklistChecked records both directions. A box ticked and then
	// cleared is a signal; before/after carry which way it went, and recording
	// only the tick hides the clear.
	ActionChecklistChecked Action = "checklist.checked" // transition

	ActionWorklogAdded Action = "worklog.added"

	ActionDeviationCreated  Action = "deviation.created"
	ActionDeviationUpdated  Action = "deviation.updated"
	ActionDeviationDeleted  Action = "deviation.deleted"
	ActionDeviationResolved Action = "deviation.resolved" // transition

	// ── conversation and agent ───────────────────────────────────────────
	ActionThreadCreated Action = "thread.created"
	ActionMessagePosted Action = "message.posted"
	ActionTurnQueued    Action = "turn.queued"    // transition
	ActionTurnStarted   Action = "turn.started"   // transition
	ActionTurnEnded     Action = "turn.ended"     // transition
	ActionTurnAbandoned Action = "turn.abandoned" // transition
	ActionTurnCancelled Action = "turn.cancelled" // transition
	ActionToolCalled    Action = "tool.called"
	ActionToolDenied    Action = "tool.denied"

	// ── governance ───────────────────────────────────────────────────────
	ActionProposalCreated    Action = "proposal.created"
	ActionProposalDecided    Action = "proposal.decided"    // transition
	ActionProposalSuperseded Action = "proposal.superseded" // transition
	ActionProposalApplied    Action = "proposal.applied"
	ActionNoteCreated        Action = "note.created"
	ActionNoteUpdated        Action = "note.updated"
	ActionNoteStatusChanged  Action = "note.status_changed" // transition

	// ── export ───────────────────────────────────────────────────────────
	ActionSpecExported    Action = "spec.exported"
	ActionGitProjected    Action = "git.projected"
	ActionDigestRefreshed Action = "digest.refreshed"
)

// transitions is the set of actions that record a state change with a
// meaningful before and after. The v0.1 export reads these directly; everything
// else it treats as an edit.
var transitions = map[Action]bool{
	ActionUserRoleChanged:        true,
	ActionSessionStarted:         true,
	ActionSessionEnded:           true,
	ActionSessionRejected:        true,
	ActionMemberRoleChanged:      true,
	ActionDiagramRevised:         true,
	ActionLeaseExpired:           true,
	ActionIngestExtracted:        true,
	ActionIngestMerged:           true,
	ActionIngestAccepted:         true,
	ActionIngestFailed:           true,
	ActionMilestoneStatusChanged: true,
	ActionWorkItemStatusChanged:  true,
	ActionWorkItemAssigned:       true,
	ActionSubTaskStatusChanged:   true,
	ActionChecklistChecked:       true,
	ActionDeviationResolved:      true,
	ActionTurnQueued:             true,
	ActionTurnStarted:            true,
	ActionTurnEnded:              true,
	ActionTurnAbandoned:          true,
	ActionTurnCancelled:          true,
	ActionProposalDecided:        true,
	ActionProposalSuperseded:     true,
	ActionNoteStatusChanged:      true,
}

// known is every registered action. Write refuses anything outside it, which is
// what makes the enum an enforcement mechanism rather than a suggestion.
var known = func() map[Action]bool {
	all := []Action{
		ActionUserCreated, ActionUserUpdated, ActionUserDeleted, ActionUserRoleChanged,
		ActionSessionStarted, ActionSessionEnded, ActionSessionRejected,
		ActionActorCreated,
		ActionProjectCreated, ActionProjectUpdated, ActionProjectDeleted,
		ActionMemberAdded, ActionMemberRoleChanged, ActionMemberRemoved,

		ActionEntityCreated, ActionEntityUpdated, ActionEntityDeleted,
		ActionDiagramCreated, ActionDiagramUpdated, ActionDiagramDeleted,
		ActionDiagramRevised, ActionLayoutModeSet,
		ActionLeaseAcquired, ActionLeaseReleased, ActionLeaseExpired,
		ActionNodeCreated, ActionNodeUpdated, ActionNodeDeleted,
		ActionEdgeCreated, ActionEdgeUpdated, ActionEdgeDeleted,

		ActionIngestCreated, ActionIngestExtracted, ActionIngestMerged,
		ActionIngestAccepted, ActionIngestFailed,

		ActionMilestoneCreated, ActionMilestoneUpdated, ActionMilestoneDeleted,
		ActionMilestoneStatusChanged,
		ActionWorkItemCreated, ActionWorkItemUpdated, ActionWorkItemDeleted,
		ActionWorkItemStatusChanged, ActionWorkItemAssigned, ActionWorkItemMoved,
		ActionSubTaskCreated, ActionSubTaskUpdated, ActionSubTaskDeleted,
		ActionSubTaskStatusChanged,
		ActionDependenciesSet, ActionEntityRefsSet,
		ActionChecklistCreated, ActionChecklistUpdated, ActionChecklistDeleted,
		ActionChecklistChecked,
		ActionWorklogAdded,
		ActionDeviationCreated, ActionDeviationUpdated, ActionDeviationDeleted,
		ActionDeviationResolved,

		ActionThreadCreated, ActionMessagePosted,
		ActionTurnQueued, ActionTurnStarted, ActionTurnEnded,
		ActionTurnAbandoned, ActionTurnCancelled,
		ActionToolCalled, ActionToolDenied,

		ActionProposalCreated, ActionProposalDecided, ActionProposalSuperseded,
		ActionProposalApplied,
		ActionNoteCreated, ActionNoteUpdated, ActionNoteStatusChanged,

		ActionSpecExported, ActionGitProjected, ActionDigestRefreshed,
	}
	m := make(map[Action]bool, len(all))
	for _, a := range all {
		m[a] = true
	}
	return m
}()

// Valid reports whether a is a registered action.
func (a Action) Valid() bool { return known[a] }

// IsTransition reports whether a records a state change rather than an edit.
func (a Action) IsTransition() bool { return transitions[a] }

// SubjectKind is the part of the action before the dot, and is what the
// audit_log.subject_kind column is populated from. Deriving it removes the
// possibility of an action and a subject kind that disagree.
func (a Action) SubjectKind() string {
	kind, _, _ := strings.Cut(string(a), ".")
	return kind
}

func (a Action) String() string { return string(a) }

// Actions returns every registered action, sorted. Used by the OpenAPI emitter
// and by tests that assert the taxonomy has not silently shrunk.
func Actions() []Action {
	out := make([]Action, 0, len(known))
	for a := range known {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Transitions returns every registered transition action, sorted.
func Transitions() []Action {
	out := make([]Action, 0, len(transitions))
	for a := range transitions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
