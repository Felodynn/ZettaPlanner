package serialize

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// A slice is not a diagram, and is not serialized as one.
//
// §6.2's shape describes one diagram: `diagram:`, `level:`, then its nodes and
// edges. What a spec export needs is different — the entities a work item
// references, which may be drawn in four different pictures, plus the relations
// around them. Emitting that under `nodes:` and `edges:` would tell the reader
// it is looking at a diagram, and the first thing they would ask is which one.
//
// So the keys are `entities:` and `relations:`, each relation naming the diagram
// it was drawn in, and every determinism rule from Graph applies unchanged:
// sorted collections, fixed field order, an explicit node tree rather than a
// marshalled map. Recorded as D60.

// SliceEntity is one catalog entity in an architecture slice.
type SliceEntity struct {
	Slug    string
	Kind    diagram.NodeKind
	Name    string
	Summary string
	Attrs   map[string]any

	Provenance diagram.Provenance

	// Referenced separates what the work item actually points at from what was
	// pulled in as the far end of a relation. Without it a reader cannot tell
	// the subject of the slice from its surroundings, and a decomposer treats
	// the neighbours as in scope.
	Referenced bool
}

// SliceRelation is one relation drawn between two entities in the slice.
type SliceRelation struct {
	From     string
	To       string
	Relation diagram.Relation
	Label    string

	// Diagram is where it was drawn. A slice spans pictures, so the reader
	// needs to know which one to go and look at.
	Diagram string
}

// Slice serializes an architecture slice: the entities and the relations among
// them, spanning every diagram they appear in.
//
// Deterministic in the same way Graph is, and for the same reason — this output
// goes into a document a person reads and diffs, and a marshaller that felt
// different today produces a diff nobody can interpret.
func Slice(entities []SliceEntity, relations []SliceRelation) ([]byte, error) {
	root := mapping()
	if seq := sliceEntitySequence(entities); len(seq.Content) > 0 {
		put(root, "entities", seq)
	}
	if seq := sliceRelationSequence(relations, entities); len(seq.Content) > 0 {
		put(root, "relations", seq)
	}
	if len(root.Content) == 0 {
		return nil, nil
	}
	return encode(root)
}

// sliceEntitySequence emits entities ordered by kind, then slug — the same
// ordering the diagram serializer uses, so the two read alike.
func sliceEntitySequence(entities []SliceEntity) *yaml.Node {
	seen := make(map[string]bool, len(entities))
	live := make([]SliceEntity, 0, len(entities))
	for _, e := range entities {
		if e.Slug == "" || seen[e.Slug] {
			continue
		}
		seen[e.Slug] = true
		live = append(live, e)
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].Kind != live[j].Kind {
			return live[i].Kind < live[j].Kind
		}
		return live[i].Slug < live[j].Slug
	})

	seq := sequence()
	for _, e := range live {
		m := mapping()
		put(m, "id", scalar(e.Slug))
		put(m, "kind", scalar(string(e.Kind)))
		put(m, "name", scalar(e.Name))
		if summary := strings.TrimSpace(e.Summary); summary != "" {
			put(m, "summary", prose(summary))
		}
		if !e.Referenced {
			// Emitted only when false. A slice is mostly its subject, and a
			// `referenced: true` on every entry is noise that stops being read.
			put(m, "referenced", scalarTagged("false", "!!bool"))
		}
		if e.Provenance != "" {
			put(m, "provenance", scalar(string(e.Provenance)))
		}
		if attrs := attrsMapping(e.Attrs); attrs != nil {
			put(m, "attrs", attrs)
		}
		seq.Content = append(seq.Content, m)
	}
	return seq
}

// sliceRelationSequence emits relations ordered by (from, to, relation,
// diagram), deduplicated on that same key.
//
// Deduplication is not optional: a relation between two entities that are both
// in the slice is discovered twice, once from each end.
func sliceRelationSequence(relations []SliceRelation, entities []SliceEntity) *yaml.Node {
	known := make(map[string]bool, len(entities))
	for _, e := range entities {
		known[e.Slug] = true
	}

	seen := make(map[string]bool, len(relations))
	live := make([]SliceRelation, 0, len(relations))
	for _, r := range relations {
		if !known[r.From] || !known[r.To] {
			// A relation to something outside the slice would name a slug the
			// reader cannot look up. The far end is either in `entities` or the
			// relation does not belong here.
			continue
		}
		key := r.From + "\x00" + r.To + "\x00" + string(r.Relation) + "\x00" + r.Diagram
		if seen[key] {
			continue
		}
		seen[key] = true
		live = append(live, r)
	}
	sort.SliceStable(live, func(i, j int) bool {
		switch {
		case live[i].From != live[j].From:
			return live[i].From < live[j].From
		case live[i].To != live[j].To:
			return live[i].To < live[j].To
		case live[i].Relation != live[j].Relation:
			return live[i].Relation < live[j].Relation
		default:
			return live[i].Diagram < live[j].Diagram
		}
	})

	seq := sequence()
	for _, r := range live {
		m := mapping()
		put(m, "from", scalar(r.From))
		put(m, "to", scalar(r.To))
		put(m, "relation", scalar(string(r.Relation)))
		if label := strings.TrimSpace(r.Label); label != "" {
			put(m, "label", scalar(label))
		}
		if r.Diagram != "" {
			put(m, "diagram", scalar(r.Diagram))
		}
		seq.Content = append(seq.Content, m)
	}
	return seq
}
