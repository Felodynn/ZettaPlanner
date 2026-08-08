package agent

import (
	"fmt"
	"time"

	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
)

// Limits are the three hard ceilings, enforced independently.
//
// Independently is the point. A loop bounded only by step count spends fifteen
// minutes on forty enormous steps; one bounded only by tokens spins forever on
// cheap ones. Whichever trips first ends the turn, and the turn records which,
// because "it hit MaxTurns" and "it hit MaxWallClock" call for different fixes.
type Limits struct {
	MaxSteps     int
	MaxTokens    int
	MaxWallClock time.Duration
}

// DefaultLimits are BACKEND.md Phase 6's numbers. Tune later with evidence:
// Phase 6's measurement set is what produces the evidence, and moving them
// before then would be moving them on a feeling.
func DefaultLimits() Limits {
	return Limits{
		MaxSteps:     40,
		MaxTokens:    300_000,
		MaxWallClock: 15 * time.Minute,
	}
}

// normalise fills in anything a caller left zero.
func (l Limits) normalise() Limits {
	d := DefaultLimits()
	if l.MaxSteps <= 0 {
		l.MaxSteps = d.MaxSteps
	}
	if l.MaxTokens <= 0 {
		l.MaxTokens = d.MaxTokens
	}
	if l.MaxWallClock <= 0 {
		l.MaxWallClock = d.MaxWallClock
	}
	return l
}

// exceeded reports which limit a turn has passed, if any.
func (l Limits) exceeded(steps, tokens int, elapsed time.Duration) (th.FinishReason, string) {
	switch {
	case steps >= l.MaxSteps:
		return th.FinishMaxTurns, fmt.Sprintf("stopped after %d steps, the configured ceiling", l.MaxSteps)
	case tokens >= l.MaxTokens:
		return th.FinishMaxTokens, fmt.Sprintf("stopped after %d tokens, the configured ceiling", l.MaxTokens)
	case elapsed >= l.MaxWallClock:
		return th.FinishMaxWall, fmt.Sprintf("stopped after %s, the configured ceiling", l.MaxWallClock)
	}
	return "", ""
}
