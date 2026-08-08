package serialize

import (
	"sort"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// The files the git projection writes that are not a node or an edge.
//
// Everything here obeys the same rule as the rest of the package: an explicit
// node tree, fixed field order, sorted collections. A projection is only worth
// having if an empty diff means nothing changed, and that property is destroyed
// by exactly one marshalled map.
//
// Nothing here carries a timestamp, a count, or a "generated at". Each of those
// produces a commit whose diff is a number moving, and a history full of those
// is a history nobody reads — which costs more than the field was worth.

// ProjectFile is the root project.yaml.
type ProjectFile struct {
	Key         string
	Name        string
	Description string
	Diagrams    []DiagramRef
}

// DiagramRef is one diagram in the project index.
type DiagramRef struct {
	Slug     string
	Name     string
	Level    diagram.Level
	Revision int
}

// Project serializes project.yaml.
func Project(p ProjectFile) ([]byte, error) {
	root := mapping()
	put(root, "project", scalar(p.Key))
	put(root, "name", scalar(p.Name))
	if d := strings.TrimSpace(p.Description); d != "" {
		put(root, "description", prose(d))
	}

	refs := append([]DiagramRef(nil), p.Diagrams...)
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Slug < refs[j].Slug })
	if len(refs) > 0 {
		seq := sequence()
		for _, d := range refs {
			m := mapping()
			put(m, "diagram", scalar(d.Slug))
			put(m, "name", scalar(d.Name))
			put(m, "level", scalar(string(d.Level)))
			put(m, "revision", scalarTagged(itoa(d.Revision), "!!int"))
			seq.Content = append(seq.Content, m)
		}
		put(root, "diagrams", seq)
	}
	return encode(root)
}

// DiagramMeta serializes one diagram's own file: what the picture is, not what
// is in it. Its contents are one file per node and one per edge (§6.2), because
// a single big document makes every diff useless.
func DiagramMeta(d *diagram.Diagram) ([]byte, error) {
	root := mapping()
	put(root, "diagram", scalar(d.Slug))
	put(root, "name", scalar(d.Name))
	put(root, "level", scalar(string(d.Level)))
	put(root, "revision", scalarTagged(itoa(d.CurrentRevision), "!!int"))
	return encode(root)
}

// Entity serializes one catalog entity — the project-level identity behind
// every placement of it (§3.4).
//
// Projected in its own file as well as inside each diagram's nodes, and that is
// not duplication: "when did this become a queue" is a question about the
// entity, and answering it from the node files means reading four of them and
// hoping they agree.
func Entity(e *diagram.Entity, placedIn []string) ([]byte, error) {
	root := mapping()
	put(root, "id", scalar(e.Slug))
	put(root, "kind", scalar(string(e.Kind)))
	put(root, "name", scalar(e.Name))
	if s := strings.TrimSpace(e.Summary); s != "" {
		put(root, "summary", prose(s))
	}
	put(root, "provenance", scalar(string(e.Provenance)))
	if attrs := attrsMapping(e.Attrs); attrs != nil {
		put(root, "attrs", attrs)
	}

	slugs := append([]string(nil), placedIn...)
	sort.Strings(slugs)
	if len(slugs) > 0 {
		seq := sequence()
		for _, s := range slugs {
			seq.Content = append(seq.Content, scalar(s))
		}
		put(root, "drawn_in", seq)
	}
	return encode(root)
}

// EntityPath is the stable, slug-derived path for a catalog entity.
func EntityPath(slug string) string { return "entities/" + slug + ".yaml" }

// DiagramDir is the directory one diagram's files live under. Slug-derived, so
// renaming a diagram's display name does not move every file it owns.
func DiagramDir(slug string) string { return "diagrams/" + slug }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
