package domain

// Provenance is where an object came from (CONTRACT §3.7).
//
// It lives here rather than in domain/diagram because §3.7 is explicit that it
// applies to "every node, edge, work item, and plan field". The re-evaluate flow
// (§5.4) depends on it: showing the vision model exactly which of its
// extractions a human corrected is the highest-signal input it will ever get
// about where its reading was weak, and that only works if `human_edited` means
// the same thing everywhere it is written.
type Provenance string

const (
	ProvVLMExtracted   Provenance = "vlm_extracted"
	ProvHumanCreated   Provenance = "human_created"
	ProvHumanEdited    Provenance = "human_edited"
	ProvAgentProposed  Provenance = "agent_proposed"
	ProvAgentCommitted Provenance = "agent_committed"
	ProvSystem         Provenance = "system"
)

// Provenances returns every origin, in contract order.
func Provenances() []Provenance {
	return []Provenance{
		ProvVLMExtracted, ProvHumanCreated, ProvHumanEdited,
		ProvAgentProposed, ProvAgentCommitted, ProvSystem,
	}
}

// Valid reports whether p is a known provenance.
func (p Provenance) Valid() bool {
	switch p {
	case ProvVLMExtracted, ProvHumanCreated, ProvHumanEdited,
		ProvAgentProposed, ProvAgentCommitted, ProvSystem:
		return true
	}
	return false
}

func (p Provenance) String() string { return string(p) }
