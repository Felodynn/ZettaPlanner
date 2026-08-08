package serialize_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize"
)

func sliceFixture() ([]serialize.SliceEntity, []serialize.SliceRelation) {
	entities := []serialize.SliceEntity{
		{Slug: "token-store", Kind: diagram.KindDatastore, Name: "Token Store", Referenced: true},
		{Slug: "auth-service", Kind: diagram.KindService, Name: "Auth Service",
			Summary: "Issues and validates session tokens", Referenced: true,
			Attrs: map[string]any{"language": "go", "replicas": 3.0}},
		{Slug: "billing-api", Kind: diagram.KindService, Name: "Billing API",
			Provenance: diagram.ProvVLMExtracted},
	}
	relations := []serialize.SliceRelation{
		{From: "auth-service", To: "token-store", Relation: diagram.RelWritesTo,
			Label: "session rows", Diagram: "auth-context"},
		// The same edge as seen from the other end: Hops reports one row per
		// direction, and both arrive here.
		{From: "auth-service", To: "token-store", Relation: diagram.RelWritesTo,
			Label: "session rows", Diagram: "auth-context"},
		{From: "billing-api", To: "auth-service", Relation: diagram.RelCalls, Diagram: "auth-context"},
	}
	return entities, relations
}

func TestSliceIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	entities, relations := sliceFixture()

	first, err := serialize.Slice(entities, relations)
	if err != nil {
		t.Fatalf("serializing: %v", err)
	}

	// Reversed, which is what a different query plan would hand over.
	reversed := make([]serialize.SliceEntity, 0, len(entities))
	for i := len(entities) - 1; i >= 0; i-- {
		reversed = append(reversed, entities[i])
	}
	reversedRels := make([]serialize.SliceRelation, 0, len(relations))
	for i := len(relations) - 1; i >= 0; i-- {
		reversedRels = append(reversedRels, relations[i])
	}

	second, err := serialize.Slice(reversed, reversedRels)
	if err != nil {
		t.Fatalf("serializing reversed: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("the same slice serialized two ways produced different bytes:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

func TestSliceReportsTheEdgeOnceAndMarksWhatIsOnlyContext(t *testing.T) {
	entities, relations := sliceFixture()
	out, err := serialize.Slice(entities, relations)
	if err != nil {
		t.Fatalf("serializing: %v", err)
	}
	body := string(out)

	if n := strings.Count(body, "relation: writes_to"); n != 1 {
		t.Fatalf("the same edge was reported %d times; Hops sees it from both ends and the serializer "+
			"has to collapse that:\n%s", n, body)
	}
	if !strings.Contains(body, "referenced: false") {
		t.Fatalf("billing-api is a neighbour, not scope, and nothing in the output says so:\n%s", body)
	}
	if strings.Contains(body, "referenced: true") {
		t.Fatalf("`referenced: true` on the subject is noise on every entry; only the exception is emitted:\n%s", body)
	}
	// A JSONB round trip makes every number a float64. Printing the replica
	// count as 3.0 puts a diff in the file the first time a row is re-read.
	if !strings.Contains(body, "replicas: 3\n") {
		t.Fatalf("a whole number came back from JSONB as a float and was printed as one:\n%s", body)
	}
}

func TestSliceDropsARelationWhoseFarEndIsNotInIt(t *testing.T) {
	entities := []serialize.SliceEntity{
		{Slug: "auth-service", Kind: diagram.KindService, Name: "Auth Service", Referenced: true},
	}
	relations := []serialize.SliceRelation{
		{From: "auth-service", To: "deleted-thing", Relation: diagram.RelCalls, Diagram: "auth-context"},
	}
	out, err := serialize.Slice(entities, relations)
	if err != nil {
		t.Fatalf("serializing: %v", err)
	}
	if strings.Contains(string(out), "deleted-thing") {
		t.Fatalf("the slice names a slug the reader cannot look up anywhere in it:\n%s", out)
	}
}

func TestAnEmptySliceIsNoBytesAtAll(t *testing.T) {
	out, err := serialize.Slice(nil, nil)
	if err != nil {
		t.Fatalf("serializing: %v", err)
	}
	if len(out) != 0 {
		// `{}` in a fenced yaml block in a spec.md reads as "the architecture is
		// empty", which is a different claim from "nothing was referenced".
		t.Fatalf("an empty slice produced %q; the caller decides how to say `nothing`", out)
	}
}
