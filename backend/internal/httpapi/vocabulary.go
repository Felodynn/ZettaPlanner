package httpapi

import (
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
)

// The closed vocabulary is declared twice on purpose, and the two declarations
// have to agree.
//
// The `enum` tags on the request structs are what put the vocabulary into the
// OpenAPI document, which is what gives the frontend's generated client a real
// union type instead of a string. But huma validates against them before any of
// our code runs, and its message is "validation failed" with the valid values
// buried in a nested details array.
//
// That is fine for a browser and useless for a model. BACKEND.md Phase 2 is
// explicit: the error naming the valid values "is read by the agent in Phase 6
// and must teach". A bare refusal produces a second invalid value, then a third.
//
// So: keep the enum, and when a schema failure lands on a closed-set field,
// replace the message with the domain's teaching error — the same text the
// service layer produces when the agent reaches it through a tool rather than
// through HTTP. One vocabulary, one explanation, whichever door it came in.

// teachClosedVocabulary rewrites a validation error whose failures are
// closed-set violations. It returns the message unchanged when none of them are.
//
// path is the request path. It is needed because a field name alone does not
// identify a vocabulary: `body.status` is a work item's six-value set on one
// route and a milestone's deliberately coarser four-value set on another.
// Guessing from the value would teach the wrong set precisely when somebody
// sent a value that is valid in the other one — which is the most likely
// mistake there is.
func teachClosedVocabulary(path string, details []map[string]any) string {
	var lessons []string

	for _, d := range details {
		location, _ := d["location"].(string)
		value, _ := d["value"].(string)
		if location == "" || value == "" {
			continue
		}

		var err error
		switch {
		case strings.HasSuffix(location, ".relation"):
			err = dg.ValidateRelation(dg.Relation(value))
		case strings.Contains(location, "nodes[") && strings.HasSuffix(location, ".kind"):
			err = dg.ValidateNodeKind(dg.NodeKind(value))
		case strings.HasSuffix(location, ".level"):
			err = dg.ValidateLevel(dg.Level(value))
		case strings.HasSuffix(location, ".status") && strings.Contains(path, "/milestones"):
			err = pl.ValidateMilestoneStatus(pl.MilestoneStatus(value))
		case strings.HasSuffix(location, ".status"):
			err = pl.ValidateStatus(pl.Status(value))
		case strings.HasSuffix(location, ".priority"):
			err = pl.ValidatePriority(pl.Priority(value))
		case strings.HasSuffix(location, ".severity"):
			err = pl.ValidateSeverity(pl.Severity(value))
		case strings.HasSuffix(location, ".kind") && strings.Contains(path, "/worklogs"):
			err = pl.ValidateWorklogKind(pl.WorklogKind(value))
		}
		if err != nil {
			lessons = append(lessons, location+": "+err.Error())
		}
	}

	if len(lessons) == 0 {
		return ""
	}
	return strings.Join(lessons, "\n\n")
}
