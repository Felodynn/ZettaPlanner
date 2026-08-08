package agent

import (
	"context"
	"fmt"
)

// Control tools end a turn deliberately.
//
// Both of them count as output. `handoff_to_human` in particular: an agent that
// hits a wall and *says so, naming the wall*, has done something useful — it is
// a turn ending in prose and no handoff that is the failure, because from the
// outside that looks exactly like a completed run.

func controlTools() []Tool {
	return []Tool{
		{
			Name: "finish",
			Kind: KindControl,
			Description: "End the turn. Call this when the work is done. `summary` is what the person who asked " +
				"will read, so say what changed and what you did not do, not what you intend to do next.",
			Params: schema(`{"type":"object","properties":{
				"summary":{"type":"string","description":"What you did, in the words you would use to a colleague."}},
				"required":["summary"]}`),
			Run: runFinish,
		},
		{
			Name: "handoff_to_human",
			Kind: KindControl,
			Description: "End the turn and hand back, naming what you could not do and why. Use this when a boundary " +
				"stops you, when the request is ambiguous in a way that matters, or when acting would need a judgement " +
				"that is not yours. There is no way to ask a question and wait — this is how you ask one.",
			Params: schema(`{"type":"object","properties":{
				"reason":{"type":"string","description":"What stopped you, specifically. 'I need more information' is not a reason; 'ZP-14 has two plausible readings of \"done\" and they imply different dates' is."},
				"question":{"type":"string","description":"The question you want answered, if there is one."}},
				"required":["reason"]}`),
			Run: runHandoff,
		},
	}
}

func runFinish(_ context.Context, s *session, a args) (Result, error) {
	summary, err := a.required("summary")
	if err != nil {
		return Result{}, err
	}
	s.summary = summary

	// `finish` does not itself count as output. A turn that read three things
	// and called finish has answered nothing, and letting the call launder that
	// into a success is exactly the hole this phase exists to close: a run that
	// anticipates a wall and stops looks identical to a completed one otherwise.
	return Result{Stop: true, Content: "Turn ended."}, nil
}

func runHandoff(_ context.Context, s *session, a args) (Result, error) {
	reason, err := a.required("reason")
	if err != nil {
		return Result{}, err
	}
	s.handoff = reason
	if q := a.str("question"); q != "" {
		s.handoff += "\n\nQuestion: " + q
	}
	s.summary = s.handoff

	return Result{
		Stop:     true,
		Produced: true,
		Content:  fmt.Sprintf("Handed back to a human: %s", reason),
	}, nil
}
