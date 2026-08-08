// Package diagram holds the semantic layer of a diagram: entities, nodes,
// edges, and the closed vocabulary they are drawn from.
//
// **There is no coordinate anywhere in this package, and there must never be.**
// Not filtered out before a prompt — absent from the types (D5). Coordinates
// are meaningless to a model and actively harmful in a prompt: the planner
// derives flow from relations, and a large diagram's geometry invites it to
// derive flow from position instead. Presentation lives in
// internal/domain/layout, which this package does not import and which the
// serializer cannot reach.
package diagram

import (
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// NodeKind is the closed set of node kinds (CONTRACT §3.3, D6).
//
// Closed, not per-project configurable. Configurable types mean prompts must be
// regenerated and versioned per project, and the query surface degrades to
// string matching. The same closed vocabulary is what makes VLM extraction
// reliable in Phase 3.
type NodeKind string

const (
	KindSystem         NodeKind = "system"
	KindService        NodeKind = "service"
	KindComponent      NodeKind = "component"
	KindDatastore      NodeKind = "datastore"
	KindQueue          NodeKind = "queue"
	KindExternalSystem NodeKind = "external_system"
	KindActor          NodeKind = "actor"
	KindAPI            NodeKind = "api"
	KindJob            NodeKind = "job"
	KindBoundary       NodeKind = "boundary"
	KindNote           NodeKind = "note"
)

// nodeKindMeaning is the one-line gloss from the contract's table. It is
// included in the validation error, because that error is read by the vision
// model in Phase 3 and by the planner in Phase 6, and a list of bare words
// teaches less than a list with meanings.
var nodeKindMeaning = map[NodeKind]string{
	KindSystem:         "a whole system under discussion",
	KindService:        "an independently deployable process",
	KindComponent:      "a module inside a service, not independently deployable",
	KindDatastore:      "a database, cache, object store, or file store",
	KindQueue:          "a message broker, topic, or stream",
	KindExternalSystem: "something outside the team's control",
	KindActor:          "a human role or class of user",
	KindAPI:            "a named interface surface, when the interface is the subject",
	KindJob:            "a scheduled or triggered batch process",
	KindBoundary:       "a grouping container: trust zone, deployment unit, team ownership",
	KindNote:           "free text pinned to the diagram; carries no relations",
}

// NodeKinds returns every kind, in contract order.
func NodeKinds() []NodeKind {
	return []NodeKind{
		KindSystem, KindService, KindComponent, KindDatastore, KindQueue,
		KindExternalSystem, KindActor, KindAPI, KindJob, KindBoundary, KindNote,
	}
}

// Valid reports whether k is in the closed set.
func (k NodeKind) Valid() bool { _, ok := nodeKindMeaning[k]; return ok }

// NodeKindMeaning returns the one-line gloss for a kind.
//
// Exported because the ingest prompt teaches the vocabulary to the vision model
// with the same words the validation error uses. Two hand-written copies of this
// table would diverge, and the copy that diverged would be the prompt.
func NodeKindMeaning(k NodeKind) string { return nodeKindMeaning[k] }

func (k NodeKind) String() string { return string(k) }

// CanContain reports whether a node of this kind may be a containment parent.
// Only `boundary` and `system` group other nodes (§3.3).
func (k NodeKind) CanContain() bool { return k == KindBoundary || k == KindSystem }

// Relation is the closed set of edge relations (CONTRACT §3.3). All are
// directed.
type Relation string

const (
	RelCalls        Relation = "calls"
	RelDependsOn    Relation = "depends_on"
	RelReadsFrom    Relation = "reads_from"
	RelWritesTo     Relation = "writes_to"
	RelPublishesTo  Relation = "publishes_to"
	RelSubscribesTo Relation = "subscribes_to"
	RelContains     Relation = "contains"
	RelDeploysTo    Relation = "deploys_to"
	RelDataFlow     Relation = "data_flow"
)

var relationMeaning = map[Relation]string{
	RelCalls:        "synchronous request/response",
	RelDependsOn:    "needs to exist or be available; non-specific",
	RelReadsFrom:    "reads data",
	RelWritesTo:     "writes data",
	RelPublishesTo:  "emits to a queue or topic",
	RelSubscribesTo: "consumes from a queue or topic",
	RelContains:     "structural containment; only from a boundary or a system",
	RelDeploysTo:    "runtime placement",
	RelDataFlow:     "generic flow, when nothing more specific is true",
}

// Relations returns every relation, in contract order.
func Relations() []Relation {
	return []Relation{
		RelCalls, RelDependsOn, RelReadsFrom, RelWritesTo, RelPublishesTo,
		RelSubscribesTo, RelContains, RelDeploysTo, RelDataFlow,
	}
}

// Valid reports whether r is in the closed set.
func (r Relation) Valid() bool { _, ok := relationMeaning[r]; return ok }

// RelationMeaning returns the one-line gloss for a relation.
func RelationMeaning(r Relation) string { return relationMeaning[r] }

func (r Relation) String() string { return string(r) }

// IsDependency reports whether this relation means the source needs the target.
// Used by the cross-diagram "what depends on X" traversal; `contains` and
// `deploys_to` are structural rather than dependency edges.
func (r Relation) IsDependency() bool {
	switch r {
	case RelCalls, RelDependsOn, RelReadsFrom, RelWritesTo, RelPublishesTo, RelSubscribesTo, RelDataFlow:
		return true
	}
	return false
}

// DependencyRelations returns the relations IsDependency accepts, for SQL.
func DependencyRelations() []string {
	var out []string
	for _, r := range Relations() {
		if r.IsDependency() {
			out = append(out, string(r))
		}
	}
	return out
}

// Level is a diagram's altitude (§3.3). Sequence diagrams are deferred to v0.1.
type Level string

const (
	LevelContext   Level = "context"
	LevelContainer Level = "container"
	LevelComponent Level = "component"
)

// Levels returns every level.
func Levels() []Level { return []Level{LevelContext, LevelContainer, LevelComponent} }

// Valid reports whether l is a known level.
func (l Level) Valid() bool {
	switch l {
	case LevelContext, LevelContainer, LevelComponent:
		return true
	}
	return false
}

func (l Level) String() string { return string(l) }

// ── teaching errors ──────────────────────────────────────────────────────────

// VocabularyError is an unknown kind, relation or level.
//
// An alias, not a type of its own: the plan's statuses and priorities are closed
// sets too, and one error shape for every closed vocabulary is one case in the
// HTTP error map rather than one per package. Existing references — including
// the errors.As in httpapi — are unaffected, because an alias is the same type.
type VocabularyError = domain.VocabularyError

// ValidateNodeKind returns nil or a teaching error.
func ValidateNodeKind(kind NodeKind) error {
	if kind.Valid() {
		return nil
	}
	return &VocabularyError{
		Field: "node kind",
		Value: string(kind),
		Valid: domain.Glossed(NodeKinds(), NodeKindMeaning),
		Hint:  "Pick the closest one rather than inventing a kind; a kind that does not exist is a schema migration, not a configuration change.",
	}
}

// ValidateRelation returns nil or a teaching error.
func ValidateRelation(rel Relation) error {
	if rel.Valid() {
		return nil
	}
	return &VocabularyError{
		Field: "edge relation",
		Value: string(rel),
		Valid: domain.Glossed(Relations(), RelationMeaning),
		Hint:  "Use data_flow when nothing more specific is true, rather than inventing a relation.",
	}
}

// ValidateLevel returns nil or a teaching error.
func ValidateLevel(level Level) error {
	if level.Valid() {
		return nil
	}
	return &VocabularyError{
		Field: "diagram level",
		Value: string(level),
		Valid: domain.Glossed(Levels(), func(l Level) string {
			switch l {
			case LevelContext:
				return "the system and what surrounds it"
			case LevelContainer:
				return "the deployable pieces inside one system"
			default:
				return "the modules inside one container"
			}
		}),
		Hint: "Sequence diagrams are deferred to v0.1; they are a different shape with a different editor.",
	}
}
