// Package ingest is draw-to-product: a photograph or a PDF of a hand-drawn
// architecture sketch, read by a vision model into the closed vocabulary.
//
// Three rules shape everything here, and all three are about not lying:
//
//  1. **One model call per page, never one for the document.** The "continues
//     on the next page" arrow is precisely where extraction goes wrong, and a
//     single call over five pages hides that failure inside a plausible answer.
//
//  2. **Merge is a distinct, reviewable step**, not a detail of extraction. It
//     reconciles what the pages said about each other, and every judgement it
//     declines to make is recorded rather than guessed.
//
//  3. **Low confidence is a first-class value.** An element the model could not
//     read is marked unreadable and surfaced. It is never given an invented
//     label to fill the box, and it cannot reach a diagram without a human
//     supplying the name.
package ingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is the ingest state machine (CONTRACT §6.1).
type Status string

const (
	StatusUploaded   Status = "uploaded"
	StatusExtracting Status = "extracting"
	StatusMerging    Status = "merging"
	StatusReview     Status = "review"
	StatusAccepted   Status = "accepted"
	StatusFailed     Status = "failed"
)

// transitions is the state machine as data, so an illegal move is caught by a
// lookup rather than by whichever caller remembered to check.
//
// `review` can go back to `extracting`: a per-page retry re-opens an ingest a
// human is already looking at, which is the whole point of retry being per page.
var transitions = map[Status][]Status{
	StatusUploaded:   {StatusExtracting, StatusFailed},
	StatusExtracting: {StatusMerging, StatusFailed},
	StatusMerging:    {StatusReview, StatusFailed},
	StatusReview:     {StatusAccepted, StatusExtracting, StatusFailed},
	StatusAccepted:   {},
	StatusFailed:     {StatusExtracting},
}

// Statuses returns every ingest status, in lifecycle order.
func Statuses() []Status {
	return []Status{StatusUploaded, StatusExtracting, StatusMerging, StatusReview, StatusAccepted, StatusFailed}
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool { _, ok := transitions[s]; return ok }

func (s Status) String() string { return string(s) }

// Terminal reports whether nothing further happens on its own.
func (s Status) Terminal() bool { return s == StatusAccepted || s == StatusFailed }

// CanBecome reports whether next is a legal move from s.
func (s Status) CanBecome(next Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// TransitionError is an illegal state move. Its message names what is legal,
// for the same reason the vocabulary errors do.
type TransitionError struct {
	From Status
	To   Status
}

func (e *TransitionError) Error() string {
	allowed := transitions[e.From]
	if len(allowed) == 0 {
		return fmt.Sprintf("this ingest is %s, which is final; nothing follows it", e.From)
	}
	names := make([]string, 0, len(allowed))
	for _, a := range allowed {
		names = append(names, string(a))
	}
	return fmt.Sprintf("an ingest that is %s cannot become %s; from %s it can only become %s",
		e.From, e.To, e.From, strings.Join(names, " or "))
}

// PageStatus is one page's extraction state.
//
// Separate from the ingest's status because **a failed page fails the page, not
// the ingest**: four readable pages and one smudged one is a reviewable result,
// not a dead upload.
type PageStatus string

const (
	PagePending    PageStatus = "pending"
	PageExtracting PageStatus = "extracting"
	PageExtracted  PageStatus = "extracted"
	PageFailed     PageStatus = "failed"
)

// Valid reports whether s is a known page status.
func (s PageStatus) Valid() bool {
	switch s {
	case PagePending, PageExtracting, PageExtracted, PageFailed:
		return true
	}
	return false
}

func (s PageStatus) String() string { return string(s) }

// SourceKind is what was uploaded.
type SourceKind string

const (
	SourceImage SourceKind = "image"
	SourcePDF   SourceKind = "pdf"
)

// Valid reports whether k is a known source kind.
func (k SourceKind) Valid() bool { return k == SourceImage || k == SourcePDF }

// Ingest is one upload and everything that happened to it.
type Ingest struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Status    Status

	SourceKind     SourceKind
	SourceFilename string
	// SourcePath is the uploaded bytes, unmodified and kept forever. The pages
	// are derived from it; when extraction is wrong this is what you look at.
	SourcePath string
	PageCount  int

	// DiagramID is set on accept, naming what this ingest produced.
	DiagramID *uuid.UUID

	ActorID     uuid.UUID
	ErrorClass  string
	ErrorDetail string

	// Merged is the merge step's output: written when the ingest reaches
	// review, rewritten after a per-page retry, and never recomputed on read.
	// Recomputing would make "what did I approve" unanswerable the moment the
	// merge rules change.
	Merged *Merged

	CreatedAt  time.Time
	UpdatedAt  time.Time
	MergedAt   *time.Time
	AcceptedAt *time.Time

	// Pages is populated when an ingest is read for review.
	Pages []*Page
}

// Page is one rasterized page and the model's reading of it.
type Page struct {
	ID       uuid.UUID
	IngestID uuid.UUID
	// Idx is 0-based and dense. A failed page keeps its slot, because "page 3
	// of 5 is unreadable" is the useful thing to say.
	Idx       int
	ImagePath string
	Status    PageStatus

	// Extracted is the model's answer, parsed and validated.
	Extracted *PageExtraction

	// Raw is the model's answer byte for byte, before parsing. Kept forever,
	// on success and on failure alike.
	//
	// Extracted is not a substitute: it is JSONB, so it has already reordered
	// keys, dropped anything the struct does not know about, and turned an
	// array it could not decode into a null. Each of those destroys the answer
	// to "what did the model actually say", which is the only question worth
	// asking when an extraction is wrong.
	Raw string

	Model       string
	TokensIn    int
	TokensOut   int
	Attempts    int
	ErrorClass  string
	ErrorDetail string
	DurationMS  int
	ExtractedAt *time.Time
}

// PageByIdx finds a page by its index.
func (i *Ingest) PageByIdx(idx int) *Page {
	for _, p := range i.Pages {
		if p.Idx == idx {
			return p
		}
	}
	return nil
}

// ExtractedPages counts the pages the model successfully read.
func (i *Ingest) ExtractedPages() int {
	n := 0
	for _, p := range i.Pages {
		if p.Status == PageExtracted {
			n++
		}
	}
	return n
}

// FailedPages lists the indices of pages that could not be read.
func (i *Ingest) FailedPages() []int {
	var out []int
	for _, p := range i.Pages {
		if p.Status == PageFailed {
			out = append(out, p.Idx)
		}
	}
	return out
}

// Settled reports whether every page has reached a terminal state, which is
// what lets extraction hand over to merge.
func (i *Ingest) Settled() bool {
	for _, p := range i.Pages {
		if p.Status != PageExtracted && p.Status != PageFailed {
			return false
		}
	}
	return len(i.Pages) > 0
}
