package plan

import (
	"fmt"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// CycleError is a `blocks` edge that would close a loop.
//
// Rejected at write time, never cleaned up later. A cycle that lands in the
// database is found eventually by an agent that traverses it forever, and by
// then it is somebody's Tuesday afternoon rather than a 400 with the path in it
// (BACKEND.md Phase 4).
//
// Path is the loop as human-facing refs, first element repeated last, so the
// message can print it as a ring rather than making the reader close it
// themselves.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	if len(e.Path) < 2 {
		return "That dependency would make a work item block itself."
	}
	start := e.Path[0]
	return fmt.Sprintf(
		"That would make %s block itself: %s. `blocks` has to stay acyclic — a cycle is a set of items each "+
			"waiting for the next, which no order of work can satisfy. Drop one of those edges, or make it "+
			"`relates`, which carries no ordering.",
		start, strings.Join(e.Path, " → "))
}

// Is makes the cycle answer to ErrInvalid, so a caller that only wants to know
// whether the request was at fault does not need the type. The HTTP layer
// matches the type first, to keep the path in the error details.
func (e *CycleError) Is(target error) bool { return target == domain.ErrInvalid }
