package diagram

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Provenance is where an object came from (CONTRACT §3.7).
type Provenance string

const (
	ProvVLMExtracted   Provenance = "vlm_extracted"
	ProvHumanCreated   Provenance = "human_created"
	ProvHumanEdited    Provenance = "human_edited"
	ProvAgentProposed  Provenance = "agent_proposed"
	ProvAgentCommitted Provenance = "agent_committed"
	ProvSystem         Provenance = "system"
)

// Valid reports whether p is a known provenance.
func (p Provenance) Valid() bool {
	switch p {
	case ProvVLMExtracted, ProvHumanCreated, ProvHumanEdited,
		ProvAgentProposed, ProvAgentCommitted, ProvSystem:
		return true
	}
	return false
}

// Entity is a project-level named architectural thing — the identity that makes
// "what depends on the token store" answerable across the whole project rather
// than per-picture (§3.4).
type Entity struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Kind      NodeKind
	Name      string
	Slug      string
	Summary   string
	Attrs     map[string]any

	Provenance Provenance
	ActorID    uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// Node is a *placement* of an entity in one diagram. One entity may appear in a
// context diagram and a component diagram as the same identity.
type Node struct {
	ID        uuid.UUID
	DiagramID uuid.UUID
	EntityID  uuid.UUID

	// LabelOverride renames the entity in this diagram only. The entity's own
	// name is what the serializer emits as `name`; the override is a
	// presentation-adjacent nicety that still lives in the semantic layer
	// because a model reading "Auth (legacy)" learns something a coordinate
	// never conveys.
	LabelOverride string
	Attrs         map[string]any

	// ParentNodeID is containment: only a boundary or a system may be a parent.
	ParentNodeID *uuid.UUID

	Provenance Provenance
	// Confidence is set for vlm_extracted elements. Low confidence is a
	// first-class value, not a guess: an element the model could not read is
	// stored with low confidence and surfaced for review, never given an
	// invented label.
	Confidence     *float32
	ActorID        uuid.UUID
	OriginRevision int
	DeletedAt      *time.Time

	// Entity is populated when a node is read as part of a graph. It is what
	// gives the node its kind, name and slug.
	Entity *Entity

	// ParentSlug is the containment parent's slug, resolved when the graph is
	// loaded. Carried on the node so that serializing one node in isolation —
	// which the git projection does, one file per node — still names its
	// parent by a stable identity rather than a UUID.
	ParentSlug string
}

// Label is what the node is called in this diagram.
func (n *Node) Label() string {
	if n.LabelOverride != "" {
		return n.LabelOverride
	}
	if n.Entity != nil {
		return n.Entity.Name
	}
	return ""
}

// Slug is the node's stable identity in serialized output and in git paths.
// Derived from the entity, never from a display label — a rename must not look
// like a delete plus an add (§6.2).
func (n *Node) Slug() string {
	if n.Entity != nil {
		return n.Entity.Slug
	}
	return ""
}

// Kind is the entity's kind.
func (n *Node) Kind() NodeKind {
	if n.Entity != nil {
		return n.Entity.Kind
	}
	return ""
}

// Edge is a directed relation between two placements in one diagram.
type Edge struct {
	ID         uuid.UUID
	DiagramID  uuid.UUID
	FromNodeID uuid.UUID
	ToNodeID   uuid.UUID
	Relation   Relation
	Label      string
	Attrs      map[string]any

	Provenance     Provenance
	Confidence     *float32
	ActorID        uuid.UUID
	OriginRevision int
	DeletedAt      *time.Time
}

// Diagram is the semantic container.
type Diagram struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	Slug      string
	Level     Level

	// LayoutMode is `auto` until the first manual node drag, then `manual`
	// permanently. It lives here rather than in the layout package because it
	// governs *whether* coordinates are stored at all, which is a fact about
	// the diagram rather than a coordinate.
	LayoutMode      string
	CurrentRevision int

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Layout modes.
const (
	LayoutAuto   = "auto"
	LayoutManual = "manual"
)

// Note is the agent's and the harness's channel for clarifications: overlaps,
// conflicts, likely duplicates, missing pieces (§3.4).
//
// Notes live in Postgres and in the assembled context. They are never chunked
// into the vector store — they are structured facts, retrieved by traversal,
// and chunking one destroys the structure that made it findable.
type Note struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	SubjectKind string
	SubjectID   *uuid.UUID
	Kind        NoteKind
	Body        string
	Status      NoteStatus
	Provenance  Provenance
	ActorID     uuid.UUID
	CreatedAt   time.Time
	ResolvedAt  *time.Time
	DeletedAt   *time.Time

	// SubjectSlug is populated when a note is read for serialization, so the
	// YAML can name the subject rather than print a UUID at a human.
	SubjectSlug string
}

// NoteKind is the closed set of note kinds.
type NoteKind string

const (
	NoteOverlap       NoteKind = "overlap"
	NoteConflict      NoteKind = "conflict"
	NoteMissing       NoteKind = "missing"
	NoteClarification NoteKind = "clarification"
)

// Valid reports whether k is a known note kind.
func (k NoteKind) Valid() bool {
	switch k {
	case NoteOverlap, NoteConflict, NoteMissing, NoteClarification:
		return true
	}
	return false
}

// NoteStatus is a note's lifecycle state.
type NoteStatus string

const (
	NoteOpen      NoteStatus = "open"
	NoteResolved  NoteStatus = "resolved"
	NoteDismissed NoteStatus = "dismissed"
)

// Valid reports whether s is a known note status.
func (s NoteStatus) Valid() bool {
	switch s {
	case NoteOpen, NoteResolved, NoteDismissed:
		return true
	}
	return false
}

// Graph is a whole diagram's semantic state: exactly what the serializer
// receives and exactly what a model is shown.
//
// It has no layout field and no way to acquire one. That is the D5 guarantee
// expressed as a type rather than as a filtering step — a coordinate cannot be
// omitted from the prompt by accident because it was never in the value.
type Graph struct {
	Diagram *Diagram
	Nodes   []*Node
	Edges   []*Edge
	Notes   []*Note
}

// NodeByID indexes the graph's nodes.
func (g *Graph) NodeByID(id uuid.UUID) *Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// ── invariants ───────────────────────────────────────────────────────────────

// ValidationError is a graph-level invariant failure. Like VocabularyError its
// message is written to be read by a model as well as a human.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) *ValidationError {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// Validate checks the invariants that hold for any semantic state, whoever
// produced it — a human save, an accepted proposal, or an ingest.
//
// Checked here rather than in the service so that all three paths get the same
// answer, and so the rules are testable with no database at all.
func (g *Graph) Validate() error {
	if g.Diagram == nil {
		return invalid("the graph has no diagram")
	}
	if err := ValidateLevel(g.Diagram.Level); err != nil {
		return err
	}

	byID := make(map[uuid.UUID]*Node, len(g.Nodes))
	seenEntity := make(map[uuid.UUID]string, len(g.Nodes))

	for _, n := range g.Nodes {
		if n.Entity == nil {
			return invalid("node %s has no entity; every node is a placement of a catalog entity", n.ID)
		}
		if err := ValidateNodeKind(n.Entity.Kind); err != nil {
			return err
		}
		if !n.Provenance.Valid() {
			return invalid("node %s has provenance %q, which is not one of vlm_extracted, human_created, human_edited, agent_proposed, agent_committed, system",
				n.Slug(), n.Provenance)
		}
		if n.Confidence != nil && (*n.Confidence < 0 || *n.Confidence > 1) {
			return invalid("node %s has confidence %v; it must be between 0 and 1", n.Slug(), *n.Confidence)
		}
		// One placement per entity per diagram. Two placements would make
		// "what depends on X" count the same edge twice.
		if prev, dup := seenEntity[n.EntityID]; dup {
			return invalid("entity %s is placed twice in this diagram (as %s and %s); an entity has one placement per diagram",
				n.Entity.Slug, prev, n.Slug())
		}
		seenEntity[n.EntityID] = n.Slug()
		byID[n.ID] = n
	}

	for _, n := range g.Nodes {
		if n.ParentNodeID == nil {
			continue
		}
		parent, ok := byID[*n.ParentNodeID]
		if !ok {
			return invalid("node %s names a containment parent that is not in this diagram", n.Slug())
		}
		if !parent.Kind().CanContain() {
			return invalid("node %s is contained by %s, which is a %s; only a boundary or a system may contain other nodes",
				n.Slug(), parent.Slug(), parent.Kind())
		}
		if parent.ID == n.ID {
			return invalid("node %s contains itself", n.Slug())
		}
	}
	if cycle := containmentCycle(byID); cycle != "" {
		return invalid("containment is circular: %s", cycle)
	}

	seenEdge := map[string]bool{}
	for _, e := range g.Edges {
		if err := ValidateRelation(e.Relation); err != nil {
			return err
		}
		from, ok := byID[e.FromNodeID]
		if !ok {
			return invalid("an edge starts at a node that is not in this diagram")
		}
		to, ok := byID[e.ToNodeID]
		if !ok {
			return invalid("edge from %s ends at a node that is not in this diagram", from.Slug())
		}
		if from.ID == to.ID {
			return invalid("node %s has an edge to itself", from.Slug())
		}
		// `note` nodes participate in no relations (§3.3).
		if from.Kind() == KindNote || to.Kind() == KindNote {
			return invalid("edge %s -> %s is not allowed: a note node is free text pinned to the diagram and carries no relations",
				from.Slug(), to.Slug())
		}
		if e.Relation == RelContains && !from.Kind().CanContain() {
			return invalid("edge %s contains %s is not allowed: %s is a %s, and only a boundary or a system may contain",
				from.Slug(), to.Slug(), from.Slug(), from.Kind())
		}
		if !e.Provenance.Valid() {
			return invalid("edge %s -> %s has provenance %q, which is not a known origin", from.Slug(), to.Slug(), e.Provenance)
		}
		key := from.Slug() + "\x00" + to.Slug() + "\x00" + string(e.Relation)
		if seenEdge[key] {
			return invalid("edge %s %s %s appears twice; an edge is identified by (from, to, relation)",
				from.Slug(), e.Relation, to.Slug())
		}
		seenEdge[key] = true
	}
	return nil
}

// containmentCycle returns a readable path if containment loops, else "".
func containmentCycle(byID map[uuid.UUID]*Node) string {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[uuid.UUID]int{}

	var walk func(n *Node, path []string) string
	walk = func(n *Node, path []string) string {
		switch state[n.ID] {
		case done:
			return ""
		case inStack:
			return strings.Join(append(path, n.Slug()), " -> ")
		}
		state[n.ID] = inStack
		path = append(path, n.Slug())
		if n.ParentNodeID != nil {
			if parent, ok := byID[*n.ParentNodeID]; ok {
				if cycle := walk(parent, path); cycle != "" {
					return cycle
				}
			}
		}
		state[n.ID] = done
		return ""
	}

	for _, n := range byID {
		if cycle := walk(n, nil); cycle != "" {
			return cycle
		}
	}
	return ""
}

// ── slugs ────────────────────────────────────────────────────────────────────

// Slugify derives a stable identifier from a display name.
//
// Stable identity in paths matters: the git projection writes
// nodes/<entity_slug>.yaml, and a path derived from a display label turns a
// rename into a delete plus an add (§6.2). A slug is assigned once, at entity
// creation, and does not follow the name afterwards.
func Slugify(name string) string {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// NormaliseName reduces a name to the form entity resolution compares.
//
// Used to find plausible duplicates, never to merge them: on a collision the
// backend records a Note and leaves both in place (§3.4). A merge that is wrong
// 10% of the time is a permanent source of confusion with no permanent fix.
func NormaliseName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
