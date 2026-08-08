package domain

import (
	"fmt"
	"sort"
	"strings"
)

// VocabularyError is a value outside a closed set.
//
// Its message lists every valid value **with its meaning**. That text is read by
// the vision model on ingest, by the planner agent when a tool call is rejected,
// and by a human reading a toast, and it is the only teaching any of them gets —
// a bare "invalid kind" produces a second invalid kind (BACKEND.md Phase 2).
//
// It lives in the root domain package rather than in domain/diagram because the
// plan has closed vocabularies too (statuses, priorities, severities), and the
// alternative was either a second error type the HTTP layer had to learn about
// or a plan package importing the diagram package for an error shape.
type VocabularyError struct {
	Field string
	Value string
	Valid []string
	Hint  string
}

func (e *VocabularyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q is not a valid %s. The %s vocabulary is closed and these are all of it:\n", e.Value, e.Field, e.Field)
	for _, v := range e.Valid {
		fmt.Fprintf(&b, "  - %s\n", v)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "%s", e.Hint)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Is makes a vocabulary error answer to ErrInvalid, so a caller that only wants
// to know "was this the client's fault" does not have to know the type. The
// HTTP layer matches the type first and keeps the teaching text (errmap.go).
func (e *VocabularyError) Is(target error) bool { return target == ErrInvalid }

// Glossed renders a value-and-meaning table for a closed set, padded so the
// meanings line up. Every teaching error is built from this, which is what stops
// one vocabulary's error reading differently from another's.
func Glossed[T ~string](values []T, meaning func(T) string) []string {
	width := 0
	for _, v := range values {
		if len(v) > width {
			width = len(v)
		}
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprintf("%-*s %s", width+2, v, meaning(v)))
	}
	return out
}

// SortedStrings is a small helper for building the Valid list from a map, where
// the iteration order would otherwise vary between two identical requests.
func SortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
