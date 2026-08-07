package ingest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	ing "github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
)

// maxExtractAttempts is how many times one page is offered to the model within
// a single extraction.
//
// Two. The first attempt is the page; the second is the page plus the teaching
// error, and it exists because the one thing a model reliably gets wrong is a
// closed vocabulary, and the one thing that reliably fixes it is being shown
// the valid values with their meanings. A third attempt has never in practice
// succeeded where the second failed, and the page-level retry endpoint is there
// for the cases a human wants to push.
const maxExtractAttempts = 2

// extractPage runs one page image through the vision model.
//
// Schema-constrained through Ollama's `format` field, not asked-for-JSON in
// prose: in LumixAgents Phase 4 a model asked for JSON in prose left the
// content field empty and put everything in its reasoning channel, failing
// roughly half of all calls. Constraining the format removed the failure mode
// rather than parsing around it.
func (s *Service) extractPage(ctx context.Context, client *llm.Client, imagePath string, pageIdx, pageCount int) (*ing.PageExtraction, *llm.ChatResponse, error) {
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading page image: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	messages := []llm.Message{
		{Role: "system", Content: ing.ExtractionPrompt(pageIdx, pageCount)},
		{
			Role:    "user",
			Content: "Read this page and return its boxes and arrows.",
			Images:  []string{encoded},
		},
	}

	// Thinking off and temperature at zero. This is a reading task, not a
	// reasoning one: the schema already constrains the shape, and the reasoning
	// channel is exactly where a structured answer goes to die. Whether
	// thinking improves accuracy on a bad photograph is worth measuring, and
	// the measurement belongs in dogfood rather than in a guess made here.
	think := false
	temperature := 0.0

	var (
		last     *llm.ChatResponse
		lastErr  error
		schema   = ing.ExtractionSchema()
		attempts int
	)
	for attempts = 1; attempts <= maxExtractAttempts; attempts++ {
		resp, err := client.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Format:   schema,
			Think:    &think,
			Options:  llm.Options{Temperature: &temperature},
		})
		if err != nil {
			// A transport or model failure is not something a second identical
			// call fixes, and retrying one silently would hide a rate limit
			// behind a longer wait.
			return nil, resp, err
		}
		last = resp

		extraction, err := parseExtraction(resp.Message.Content)
		if err == nil {
			return extraction, resp, nil
		}
		lastErr = err

		if attempts == maxExtractAttempts {
			break
		}
		// Hand the teaching error back verbatim. Its text lists every valid
		// value with its meaning, which is the only instruction that has ever
		// reliably fixed a closed-vocabulary miss.
		messages = append(messages,
			llm.Message{Role: "assistant", Content: resp.Message.Content},
			llm.Message{Role: "user", Content: err.Error() + "\n\nReturn the whole page again, corrected."},
		)
	}
	return nil, last, lastErr
}

// parseExtraction decodes and validates one page's model output.
func parseExtraction(content string) (*ing.PageExtraction, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, &ExtractionError{
			Class:   "empty_output",
			Message: "The model returned no content at all. If this repeats, the page may be too large or the model may be putting its answer in a reasoning channel.",
		}
	}

	// Ollama Cloud discards the `format` field (D38), so a cloud model's answer
	// arrives as prose with the JSON somewhere inside it — usually in a
	// ```json fence. Finding the object is normalising a wrapper, not parsing
	// around the failure mode: everything below still rejects an answer that
	// does not match the schema, and the retry still carries the teaching
	// error. What is *not* done here is inferring a field from a key the model
	// invented.
	body, ok := firstJSONObject(trimmed)
	if !ok {
		return nil, &ExtractionError{
			Class:   "malformed_json",
			Message: "The model's output contains no JSON object. Return a single JSON object matching the schema, and nothing else — no prose, no explanation.",
		}
	}

	var out ing.PageExtraction
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, &ExtractionError{
			Class:   "malformed_json",
			Message: fmt.Sprintf("The model's output is not valid JSON (%v). Return a single JSON object matching the schema and nothing else.", err),
		}
	}

	if err := normaliseExtraction(&out); err != nil {
		return nil, err
	}

	// A page with arrows but no boxes is not a page that was read; it is an
	// answer that ignored half the schema. This is the failure that started
	// D38: `nodes` simply absent, `edges` populated, and the decoder reporting
	// success over an extraction with nothing in it. An empty result and a
	// successful one must never look the same.
	if len(out.Nodes) == 0 {
		return nil, &ExtractionError{
			Class: "no_nodes",
			Message: "The `nodes` array is missing or empty. Every arrow starts and ends at a box, so a page with arrows has boxes. " +
				"Return every box on the page under the key `nodes`, including the ones you cannot read — those get legible: false " +
				"and a positional name. If the page genuinely contains no boxes at all, return both `nodes` and `edges` as empty arrays.",
		}
	}
	return &out, nil
}

// firstJSONObject extracts the first balanced {...} from a response, ignoring
// braces inside strings.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}

	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// Braces inside a label such as "{tenant}/sessions" are text.
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// normaliseExtraction enforces the closed vocabulary and the ranges, in place.
//
// A value outside the vocabulary is a rejection with the teaching error, never
// a coercion. Coercing `microservice` to `service` would be right often enough
// to be trusted and wrong often enough to matter, and the model would never
// find out.
func normaliseExtraction(e *ing.PageExtraction) error {
	if e.Level != "" && !e.Level.Valid() {
		// Advisory, so a bad level is dropped rather than failing a page whose
		// boxes are fine. The human picks the level on accept anyway.
		e.Level = ""
	}
	e.Title = strings.TrimSpace(e.Title)

	names := make(map[string]bool, len(e.Nodes))
	kept := e.Nodes[:0]
	for i := range e.Nodes {
		n := &e.Nodes[i]
		if err := dg.ValidateNodeKind(n.Kind); err != nil {
			return err
		}
		n.Name = strings.TrimSpace(n.Name)
		n.Verbatim = strings.TrimSpace(n.Verbatim)
		n.Summary = strings.TrimSpace(n.Summary)
		n.Parent = strings.TrimSpace(n.Parent)
		n.Confidence = clamp01(n.Confidence)

		// A box the model claims to have read but gave no name to was not read.
		// Recorded as unreadable rather than dropped: there is a box there.
		if n.Name == "" {
			n.Legible = false
			n.Name = fmt.Sprintf("unnamed element %d", i+1)
		}
		if !n.Legible && n.Confidence > ing.ReviewThreshold {
			// The model contradicted itself: it could not read the text but is
			// confident about it. Believe the admission, not the number.
			n.Confidence = ing.ReviewThreshold - 0.01
		}

		// Two boxes with the same name on one page is one box read twice, or
		// two boxes the model could not tell apart. Either way the second one
		// is not new information and would collide on merge.
		key := dg.NormaliseName(n.Name)
		if n.Legible && key != "" {
			if names[key] {
				continue
			}
			names[key] = true
		}
		kept = append(kept, *n)
	}
	e.Nodes = kept

	edges := e.Edges[:0]
	for i := range e.Edges {
		ed := &e.Edges[i]
		if err := dg.ValidateRelation(ed.Relation); err != nil {
			return err
		}
		ed.From = strings.TrimSpace(ed.From)
		ed.To = strings.TrimSpace(ed.To)
		ed.Label = strings.TrimSpace(ed.Label)
		ed.Confidence = clamp01(ed.Confidence)
		if ed.From == "" || ed.To == "" || dg.NormaliseName(ed.From) == dg.NormaliseName(ed.To) {
			// An arrow with no ends, or one from a box to itself, is a reading
			// artefact rather than a relation.
			continue
		}
		edges = append(edges, *ed)
	}
	e.Edges = edges

	notes := e.Notes[:0]
	for _, n := range e.Notes {
		if n = strings.TrimSpace(n); n != "" {
			notes = append(notes, n)
		}
	}
	e.Notes = notes

	return nil
}

func clamp01(v float32) float32 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// ExtractionError is a page that could not be turned into structured data.
//
// Class is what lands in ingest_pages.error_class, so "which failure mode is
// this model actually having" is a GROUP BY rather than a grep through prose.
type ExtractionError struct {
	Class   string
	Message string
}

func (e *ExtractionError) Error() string { return e.Message }

// classify names the failure mode for a page that did not extract.
func classify(err error) (string, string) {
	var ex *ExtractionError
	if errors.As(err, &ex) {
		return ex.Class, ex.Message
	}
	var vocab *dg.VocabularyError
	if errors.As(err, &vocab) {
		return "vocabulary", vocab.Error()
	}
	switch {
	case errors.Is(err, llm.ErrContextOverflow):
		return "context_overflow", err.Error()
	case errors.Is(err, llm.ErrRateLimit):
		return "rate_limit", err.Error()
	case errors.Is(err, llm.ErrModelUnavailable):
		return "model_unavailable", err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "The model did not answer within the configured timeout."
	case errors.Is(err, context.Canceled):
		return "cancelled", "Extraction was cancelled."
	}
	return "extraction_failed", err.Error()
}

// truncateDetail keeps an error message readable in a review payload.
func truncateDetail(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
