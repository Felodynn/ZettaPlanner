package plan

import (
	"strings"
	"testing"
)

// TestRingTrimsTheTail.
//
// The cycle walk starts at the item being written and follows edges until one
// repeats, so the path it returns can carry a tail before the loop closes.
// Printing that tail names an item that is not in the cycle as though it were —
// and that is precisely the item somebody would then go and edit, leaving the
// actual cycle in place.
//
// In practice the tail should be empty: `blocks` is acyclic before every write,
// so any new cycle runs through the item just written. This is what makes the
// message correct anyway, rather than correct by an argument.
func TestRingTrimsTheTail(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"already a ring", []string{"ZP-1", "ZP-2", "ZP-1"}, "ZP-1 ZP-2 ZP-1"},
		{"tail before the loop", []string{"ZP-1", "ZP-2", "ZP-3", "ZP-2"}, "ZP-2 ZP-3 ZP-2"},
		{"self loop", []string{"ZP-1", "ZP-1"}, "ZP-1 ZP-1"},
		{"nothing to trim", []string{"ZP-1"}, "ZP-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(ring(tc.in), " "); got != tc.want {
				t.Errorf("ring(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
