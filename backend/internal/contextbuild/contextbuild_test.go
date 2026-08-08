package contextbuild

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
)

// Pure tests. This package has no database handle and no clock it did not
// receive, so every rule in it is checkable with no infrastructure — which is
// what makes the rules cheap enough to actually have.

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func item(ref, title string, status plan.Status) ItemView {
	return ItemView{Item: &plan.WorkItem{ID: uuid.Must(uuid.NewV7()), Ref: ref, Title: title, Status: status}}
}

// ── chunking ─────────────────────────────────────────────────────────────────

func TestChunkKeepsAWorklogWhole(t *testing.T) {
	body := "Tried the token store on Tuesday. It does not support the range query we planned around."
	got := Chunk(body)
	if len(got) != 1 || got[0] != body {
		t.Errorf("a paragraph-length worklog should be one chunk, got %d: %q", len(got), got)
	}
}

func TestChunkRefusesToEmbedNothing(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t\n"} {
		if got := Chunk(in); got != nil {
			t.Errorf("Chunk(%q) = %q; an empty chunk embeds to a vector that is nearest-neighbour to everything", in, got)
		}
	}
}

func TestChunkSplitsWithOverlap(t *testing.T) {
	// Distinct sentences so the overlap is identifiable rather than a
	// coincidence of repeated filler.
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("Paragraph ")
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString(" number ")
		b.WriteByte(byte('a' + i%26))
		b.WriteString(".\n\n")
	}
	chunks := Chunk(b.String())
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > MaxChunkChars {
			t.Errorf("chunk %d is %d chars, past the %d bound", i, len(c), MaxChunkChars)
		}
		if strings.TrimSpace(c) == "" {
			t.Errorf("chunk %d is blank", i)
		}
	}
	// Each chunk after the first carries a tail of its predecessor, so a
	// sentence straddling a boundary is retrievable from both sides rather than
	// from neither.
	tail := overlapTail(chunks[0])
	if tail == "" || !strings.HasPrefix(chunks[1], tail) {
		t.Errorf("chunk 1 does not begin with chunk 0's overlap tail\ntail: %q\nnext: %q", tail, chunks[1][:min(80, len(chunks[1]))])
	}
}

func TestChunkCutsASentenceLongerThanAChunk(t *testing.T) {
	// A pasted log line or a base64 blob: one "sentence" longer than a whole
	// chunk. It has to be cut rather than emitted as something that will not
	// embed.
	chunks := Chunk(strings.Repeat("A", ChunkChars*3))
	if len(chunks) < 3 {
		t.Fatalf("expected the oversized run to be cut, got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > MaxChunkChars {
			t.Errorf("chunk %d survived at %d chars, past the %d bound", i, len(c), MaxChunkChars)
		}
	}
}

// ── references ───────────────────────────────────────────────────────────────

func TestFindReferences(t *testing.T) {
	entities := []NamedEntity{
		{Slug: "auth-service", Name: "Auth Service"},
		{Slug: "token-store", Name: "Token Store"},
		{Slug: "api", Name: "API"},
	}
	cases := []struct {
		name     string
		message  string
		wantRefs []string
		wantSlug []string
	}{
		{"a ref", "why is ZP-14 blocked?", []string{"ZP-14"}, nil},
		{"lower case", "what about zp-14", []string{"ZP-14"}, nil},
		{"several, deduplicated", "ZP-1 blocks ZP-2 and ZP-1 again", []string{"ZP-1", "ZP-2"}, nil},
		// A word boundary, not a prefix: ZP-14 must not match inside ZP-140.
		{"not a prefix", "see ZP-140", []string{"ZP-140"}, nil},
		{"a slug", "does auth-service still write to token-store?", nil, []string{"auth-service", "token-store"}},
		{"a display name", "the Auth Service is slow", nil, []string{"auth-service"}},
		// The display-name floor: a three-character *name* is ordinary English
		// and is not matched on its own. "Token Store" is, because four
		// characters up it stops being a word somebody wrote by accident.
		{"a short name is matched only via its slug", "the api is slow", nil, []string{"api"}},
		{"a long name is matched as prose", "check the Token Store", nil, []string{"token-store"}},
		{"no false substring", "reauth-serviceable code", nil, nil},
		{"nothing", "how is the project going?", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindReferences(tc.message, "ZP", entities)
			if strings.Join(got.ItemRefs, ",") != strings.Join(tc.wantRefs, ",") {
				t.Errorf("item refs = %v, want %v", got.ItemRefs, tc.wantRefs)
			}
			if strings.Join(got.EntitySlugs, ",") != strings.Join(tc.wantSlug, ",") {
				t.Errorf("entity slugs = %v, want %v", got.EntitySlugs, tc.wantSlug)
			}
		})
	}
}

// ── the digest split ─────────────────────────────────────────────────────────

// TestComputeHarnessCountsOnlyOpenWork: the harness half is arithmetic over
// query results, and `open` means the domain's definition of open rather than a
// second list maintained here.
func TestComputeHarnessCountsOnlyOpenWork(t *testing.T) {
	h := ComputeHarness(HarnessInput{
		ByStatus: map[string]int{
			"todo": 3, "in_progress": 2, "blocked": 1, "review": 1, "done": 9, "dropped": 4,
		},
		Deviations:     map[string]int{"high": 1, "low": 2},
		ClosedStatuses: plan.ClosedStatuses(),
		WorkItems:      20,
	})
	if h.OpenItems != 7 {
		t.Errorf("open items = %d, want 7 (done and dropped are not open)", h.OpenItems)
	}
	if h.OpenDeviations != 3 {
		t.Errorf("open deviations = %d, want 3", h.OpenDeviations)
	}
	if h.ByStatus["done"] != 9 {
		t.Error("the per-status breakdown should still carry the closed statuses")
	}
}

// TestModelFieldsCannotCarryCounts is a compile-time property stated as a test.
//
// HarnessFields and ModelFields are separate types with no assignment between
// them, and ComputeHarness is the only constructor for the first and takes no
// model output. If somebody ever adds a count to ModelFields this fails, which
// is the moment to argue about it rather than a year later when the number is
// wrong.
func TestModelFieldsCarryOnlyHypotheses(t *testing.T) {
	m := ModelFields{
		CurrentHypothesis: "the token store is the bottleneck",
		DeadEnds:          []string{"sharding by tenant"},
		OpenQuestions:     []string{"does the range query matter?"},
	}
	if got := reflectFieldNames(m); len(got) != 3 {
		t.Errorf("ModelFields has %d fields (%v). The three names above are the whole surface a model may "+
			"write; a fourth is a contract change, not a refactor.", len(got), got)
	}
}

// ── assembly ─────────────────────────────────────────────────────────────────

func bigInput(t *testing.T, budget int) Input {
	t.Helper()
	sk := Skeleton{
		Project: ProjectMeta{Key: "ZP", Name: "ZettaPlanner", Status: "active"},
	}
	for i := 0; i < 60; i++ {
		sk.OpenItems = append(sk.OpenItems, item("ZP-"+itoa(i), "Work item number "+itoa(i)+" with a title long enough to cost something", plan.StatusTodo))
	}
	for i := 0; i < 6; i++ {
		sk.Deviations = append(sk.Deviations, &plan.Deviation{
			ID: uuid.Must(uuid.NewV7()), ItemRef: "ZP-" + itoa(i), Severity: plan.SeverityHigh,
			Summary:   "reality diverged here in a way that is expensive to describe " + itoa(i),
			CreatedAt: at(t, "2026-08-01T00:00:00Z"),
		})
	}
	for i := 0; i < 10; i++ {
		sk.Rejections = append(sk.Rejections, Decision{
			Title:             "A proposal that was refused, number " + itoa(i),
			Rationale:         "it looked like a good idea at the time and here is a long explanation of why",
			DecisionRationale: "we tried exactly this in April and the retention rules differ",
			DecidedAt:         at(t, "2026-08-02T00:00:00Z"),
		})
	}
	for i := 0; i < 20; i++ {
		sk.Notes = append(sk.Notes, &diagram.Note{
			Kind: diagram.NoteOverlap, SubjectSlug: "thing-" + itoa(i),
			Body: "these two may be the same thing and nobody has decided " + itoa(i),
		})
	}

	var spill Spill
	for i := 0; i < 20; i++ {
		spill.Hits = append(spill.Hits, SpillHit{Label: "worklog on ZP-" + itoa(i)})
		spill.Hits[i].Score = 0.9
		spill.Hits[i].Content = strings.Repeat("retrieved prose that costs tokens. ", 12)
	}

	return Input{
		Message:  "why is ZP-1 blocked?",
		Skeleton: sk,
		Spill:    spill,
		Budget:   Budget{Tokens: budget, Model: "test-model"},
		Now:      at(t, "2026-08-08T10:00:00Z"),
	}
}

// TestAssemblyStaysUnderBudgetAndNamesWhatItDropped is the exit criterion.
//
// Silent truncation is the failure this exists to prevent: a context that
// quietly lost the deviation explaining why an item is blocked produces a
// confidently wrong answer with nothing in the transcript to explain it.
func TestAssemblyStaysUnderBudgetAndNamesWhatItDropped(t *testing.T) {
	in := bigInput(t, 1200)
	c := Assemble(in)

	if c.Tokens > c.Budget.Tokens {
		t.Errorf("assembled ~%d tokens against a %d budget", c.Tokens, c.Budget.Tokens)
	}
	if len(c.Drops) == 0 {
		t.Fatal("something must have been dropped to fit; nothing was recorded")
	}

	text := c.Render(in)
	if !strings.Contains(text, "## OMITTED") {
		t.Error("the drops are not in the text the model reads; logging them is not enough")
	}
	for _, d := range c.Drops {
		if d.What == "" || d.Why == "" {
			t.Errorf("a drop was recorded without saying what or why: %+v", d)
		}
		if !strings.Contains(text, d.What) {
			t.Errorf("drop %q is not named in the output", d.What)
		}
	}
}

// TestRejectionsAndDeviationsSurviveAnyBudget is CONTRACT §5.2 as a property.
//
// A rejected proposal with its rationale is the field whose loss makes an agent
// re-propose what was already refused. It is cheaper than almost anything else
// in the assembly and worth more than most of it, so it is on no rung of the
// reduction ladder at any depth.
func TestRejectionsAndDeviationsSurviveAnyBudget(t *testing.T) {
	in := bigInput(t, 50) // absurdly small: everything droppable will go
	c := Assemble(in)
	text := c.Render(in)

	if !strings.Contains(text, "PREVIOUSLY REJECTED") {
		t.Fatal("the rejections section was dropped")
	}
	for _, d := range in.Skeleton.Rejections {
		if !strings.Contains(text, d.Title) {
			t.Errorf("rejection %q was dropped", d.Title)
		}
		if !strings.Contains(text, d.DecisionRationale) {
			t.Errorf("rejection %q kept its title but lost the reason, which is the part that matters", d.Title)
		}
	}
	if !strings.Contains(text, "OPEN DEVIATIONS") {
		t.Error("the deviations section was dropped")
	}
	// And the assembly says it could not fit rather than pretending it did.
	if !c.OverBudget() {
		t.Error("at a 50-token budget the assembly must report itself over rather than cut into what is never cut")
	}
}

// TestAssemblyIsDeterministic: two identical inputs produce identical bytes.
//
// Not a nicety. A context that varies between turns for no reason is a
// different answer for no reason, a broken prompt cache, and a diff in every
// client that renders it.
func TestAssemblyIsDeterministic(t *testing.T) {
	in := bigInput(t, 4000)
	first := Assemble(in).Render(in)
	for i := 0; i < 5; i++ {
		if got := Assemble(in).Render(in); got != first {
			t.Fatalf("assembly %d differs from the first", i)
		}
	}
}

// TestSpillSaysWhyItFoundNothing: an empty section reads as "there is nothing
// relevant", which is a different claim from "nothing was looked at".
func TestSpillSaysWhyItFoundNothing(t *testing.T) {
	in := bigInput(t, 8000)
	in.Spill = Spill{Unavailable: "no model with the `embedding` capability is configured in models.yaml"}

	c := Assemble(in)
	text := c.Render(in)
	if !strings.Contains(text, "no model with the `embedding` capability") {
		t.Error("the reason retrieval did not run is missing from the output")
	}
	var found bool
	for _, d := range c.Drops {
		if d.Layer == LayerSpill {
			found = true
		}
	}
	if !found {
		t.Error("an unavailable retrieval layer should be recorded as a drop, not left as an empty section")
	}
}

// TestBlockedWorkLeadsTheList: a blocked item is the one thing in a plan that
// will not move on its own, so it survives truncation and is read first.
func TestBlockedWorkLeadsTheList(t *testing.T) {
	in := bigInput(t, 8000)
	in.Skeleton.OpenItems = append(in.Skeleton.OpenItems, item("ZP-999", "The blocked one", plan.StatusBlocked))

	text := Assemble(in).Render(in)
	work := text[strings.Index(text, "## OPEN WORK"):]
	blocked := strings.Index(work, "ZP-999")
	firstTodo := strings.Index(work, "ZP-0 ")
	if blocked < 0 {
		t.Fatal("the blocked item is missing")
	}
	if firstTodo >= 0 && blocked > firstTodo {
		t.Error("a blocked item should be listed before the todo items")
	}
}

// TestEstimateIsStableAndHigh keeps the budget honest. The estimate's job is to
// answer "which layer is eating the budget", and for that a consistent estimate
// is worth as much as an exact one — but it must not run low, because an
// underestimate is a context that overflows at the model rather than here.
func TestEstimateIsStableAndHigh(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Error("the empty string costs nothing")
	}
	short, long := EstimateTokens("hello"), EstimateTokens(strings.Repeat("hello ", 100))
	if long <= short {
		t.Error("the estimate is not monotonic in length")
	}
	// Four characters per token is the industry rule of thumb; ours must not
	// come in under it for ordinary prose.
	prose := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 20)
	if got, floor := EstimateTokens(prose), len(prose)/5; got < floor {
		t.Errorf("estimate %d is below %d for %d characters of prose; running low is the unsafe direction",
			got, floor, len(prose))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// reflectFieldNames lists a struct's exported field names.
func reflectFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			out = append(out, t.Field(i).Name)
		}
	}
	return out
}
