// Package contextbuild assembles what a turn is given (CONTRACT §6.3).
//
// Three layers, assembled deterministically, and **never a replay of chat**
// (D8): a skeleton that is always present, a graph expansion reached by
// traversal from whatever the message references, and a vector spill over
// unstructured text only.
//
// **Nothing in this package does I/O.** Not "does not import the repo" — has no
// database handle, no HTTP client, no clock it did not receive. That is a
// stronger form of the D5 rule than the import test can state: a coordinate
// cannot reach a prompt from here because there is no query from here to reach
// it with. The gathering lives in internal/service/contextsvc; this package is
// given rows and decides what a model sees.
//
// It is also the most valuable debugging tool in the build. Every "why did the
// agent say that" question starts with reading this output as if you were the
// model, so the rendering optimises for a human reading it at 11pm rather than
// for compactness.
package contextbuild

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/vector"
)

// Layer names the three layers of §6.3, used in the per-layer accounting and in
// the drop records.
type Layer string

const (
	LayerSkeleton  Layer = "skeleton"
	LayerExpansion Layer = "expansion"
	LayerSpill     Layer = "spill"
)

// ── inputs ───────────────────────────────────────────────────────────────────

// ProjectMeta is the project itself.
type ProjectMeta struct {
	ID          uuid.UUID
	Key         string
	Name        string
	Description string
	Status      string
}

// MilestoneView is a milestone with its computed counts. The counts are
// computed, never stored — see the plan service.
type MilestoneView struct {
	Milestone *plan.Milestone
	Total     int
	Open      int
}

// ItemView is a work item with the names its ids resolve to. A model handed a
// UUID cannot say anything useful about it, and neither can a person.
type ItemView struct {
	Item          *plan.WorkItem
	AssigneeName  string
	MilestoneName string
}

// Decision is a proposal that has been decided (§5.1, §5.2).
//
// Rationale is why it was proposed; DecisionRationale is why it was accepted or
// refused. **A rejection requires the second one**, and it is the single
// highest-value record in the skeleton: without it the agent re-proposes what
// was already refused and the human concludes the tool does not listen.
type Decision struct {
	ID                uuid.UUID
	Title             string
	Rationale         string
	DecisionRationale string
	Status            string
	TargetKind        string
	DecidedBy         string
	DecidedAt         time.Time
}

// Skeleton is layer 1: always present, never dependent on what was asked.
type Skeleton struct {
	Project    ProjectMeta
	Milestones []MilestoneView
	OpenItems  []ItemView
	Deviations []*plan.Deviation
	Notes      []*diagram.Note
	Decisions  []Decision
	Rejections []Decision
}

// Hop is one edge out of an entity, with the far end named and the diagram it
// was drawn in.
//
// The diagram slug is carried so a reader can go and look at the picture the
// claim came from. There is no coordinate on this type and there must never be.
type Hop struct {
	Relation    diagram.Relation
	Label       string
	OtherSlug   string
	OtherName   string
	OtherKind   diagram.NodeKind
	DiagramSlug string
	// Inbound reports whether the edge points at this entity rather than away.
	Inbound bool
}

// EntitySlice is one entity plus one hop in each direction.
type EntitySlice struct {
	Entity *diagram.Entity
	Hops   []Hop
}

// ExpandedItem is a referenced work item with everything reachable from it in
// one traversal.
type ExpandedItem struct {
	ItemView
	Blocks    []plan.Dependency
	BlockedBy []plan.Dependency
	Relates   []plan.Dependency
	SubTasks  []*plan.SubTask
	Checklist []*plan.ChecklistItem
	Refs      []plan.EntityRef
	// Reason is why this item was pulled in: named in the message, asked for
	// explicitly, or reached from one that was.
	Reason string
}

// Expansion is layer 2: reached by traversal from what the message references.
//
// **No vector search is involved and none may be.** "What blocks ZP-14" is a
// query with a right answer; asking an embedding produces a plausible list
// instead, and a plausible list is worse than none because it reads as
// authoritative.
type Expansion struct {
	Items    []ExpandedItem
	Entities []EntitySlice
}

// SpillHit is one retrieved chunk with the label a reader needs to place it.
type SpillHit struct {
	vector.Hit
	// Label is where this text came from, in words: "worklog on ZP-14 · Dev ·
	// 2026-08-06". A score attached to an anonymous paragraph is not evidence.
	Label string
}

// Spill is layer 3: similarity over unstructured text only.
type Spill struct {
	Hits []SpillHit
	// Unavailable, when set, says why there is no spill — no embedding model
	// configured, nothing indexed yet, a model mismatch. Stated rather than
	// left as an empty section, because an empty section reads as "there is
	// nothing relevant" when it may mean "nothing was looked at".
	Unavailable string
}

// Input is everything the assembler is given. It never fetches anything.
type Input struct {
	Message   string
	Skeleton  Skeleton
	Expansion Expansion
	Spill     Spill
	Budget    Budget
	// Now is supplied rather than read, so the same inputs render byte for byte
	// the same in a test and in production.
	Now time.Time
}

// ── output ───────────────────────────────────────────────────────────────────

// Section is one rendered block with its estimated cost.
type Section struct {
	Layer  Layer
	Title  string
	Body   string
	Tokens int
}

// Context is the assembled result.
type Context struct {
	Sections []Section
	Drops    []Drop
	Budget   Budget

	// Tokens is the estimated total, including the header.
	Tokens int
	// ByLayer is the estimated cost per layer, which is the number that
	// actually answers "why is the context so large".
	ByLayer map[Layer]int
}

// Assemble renders the three layers, drops what does not fit, and records every
// drop.
//
// **Silent truncation is the failure mode this exists to prevent.** A context
// that quietly lost the deviation explaining why an item is blocked produces an
// answer that is confidently wrong, and nothing in the transcript says why. So
// what was dropped is rendered into the output the model reads, not only logged.
func Assemble(in Input) *Context {
	if in.Budget.Tokens <= 0 {
		in.Budget.Tokens = DefaultBudget
	}

	c := &Context{Budget: in.Budget, ByLayer: map[Layer]int{}}
	sel := newSelector(in)

	for _, s := range sel.sections() {
		if s.Body == "" {
			continue
		}
		s.Tokens = EstimateTokens(s.Body)
		c.Sections = append(c.Sections, s)
		c.ByLayer[s.Layer] += s.Tokens
		c.Tokens += s.Tokens
	}
	c.Drops = sel.drops
	c.Tokens += EstimateTokens(c.header(in))
	return c
}

// Render produces the bytes a turn receives.
func (c *Context) Render(in Input) string {
	var b strings.Builder
	b.WriteString(c.header(in))
	for _, s := range c.Sections {
		b.WriteString("\n")
		b.WriteString(s.Body)
	}
	if omitted := renderDrops(c.Drops); omitted != "" {
		b.WriteString("\n")
		b.WriteString(omitted)
	}
	return b.String()
}

// OverBudget reports whether the assembly could not be brought under its
// ceiling even after dropping everything droppable.
//
// Reported rather than fixed. What remains at that point is the skeleton's
// non-negotiable half — the project, its deviations, and what has already been
// refused — and quietly cutting into that is how an agent re-proposes something
// somebody rejected last week.
func (c *Context) OverBudget() bool { return c.Tokens > c.Budget.Tokens }

func (c *Context) header(in Input) string {
	var b strings.Builder
	p := in.Skeleton.Project
	b.WriteString("# ZettaPlanner project context\n")
	b.WriteString("# project: " + p.Key + " — " + p.Name + "\n")
	b.WriteString("# assembled: " + in.Now.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("# budget: " + itoa(in.Budget.Tokens) + " tokens against " + in.Budget.Model +
		" (counts below are estimates, not a tokenizer)\n")
	b.WriteString("#\n")
	b.WriteString("# This is assembled state, not a transcript. Everything here was read from the\n")
	b.WriteString("# database at the moment above. Where a section says nothing was found, that is\n")
	b.WriteString("# a fact about the project, not about the assembly — except where the OMITTED\n")
	b.WriteString("# section at the end says otherwise.\n")
	return b.String()
}

// ── ordering helpers ─────────────────────────────────────────────────────────

// sortItems puts work items in the order a reader wants: most urgent status
// first, then priority, then ref.
//
// Deterministic to the last tie-break. Two identical assemblies differing in
// order is a diff in every prompt cache and a different answer for no reason.
func sortItems(in []ItemView) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i].Item, in[j].Item
		if r := statusRank(a.Status) - statusRank(b.Status); r != 0 {
			return r < 0
		}
		if r := priorityRank(a.Priority) - priorityRank(b.Priority); r != 0 {
			return r < 0
		}
		if a.DueDate != nil && b.DueDate != nil && !a.DueDate.Equal(*b.DueDate) {
			return a.DueDate.Before(*b.DueDate)
		}
		if (a.DueDate == nil) != (b.DueDate == nil) {
			return a.DueDate != nil
		}
		return a.Ref < b.Ref
	})
}

// statusRank orders by how much attention the status is asking for. `blocked`
// leads because a blocked item is the one thing in a plan that will not move on
// its own.
func statusRank(s plan.Status) int {
	switch s {
	case plan.StatusBlocked:
		return 0
	case plan.StatusInProgress:
		return 1
	case plan.StatusReview:
		return 2
	case plan.StatusTodo:
		return 3
	case plan.StatusDone:
		return 4
	default:
		return 5
	}
}

func priorityRank(p plan.Priority) int {
	switch p {
	case plan.PriorityUrgent:
		return 0
	case plan.PriorityHigh:
		return 1
	case plan.PriorityNormal:
		return 2
	case plan.PriorityLow:
		return 3
	default:
		// Untriaged sorts after triaged-as-low: somebody decided low, and
		// nobody has decided this.
		return 4
	}
}
