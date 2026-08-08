package contextbuild

import (
	"strings"
	"unicode"
)

// Chunking applies to **spill only** (CONTRACT §6.3 layer 3): worklogs, past
// messages, imported documents.
//
// Work items, notes, proposals and entities are never chunked. They are
// retrieved by query, and chunking one destroys the structure that made it
// findable — a note split into three passages is a note that surfaces in a
// similarity search about something else and is missing when you ask about its
// own subject.

const (
	// ChunkChars is the target size, in characters rather than tokens because
	// the splitter has no tokenizer and a character bound is one it can
	// actually honour. Roughly 300 tokens.
	ChunkChars = 1200

	// OverlapChars is carried from the end of one chunk into the start of the
	// next, so a sentence that straddles a boundary is retrievable from both
	// sides rather than from neither.
	OverlapChars = 150

	// MinChunkChars stops a trailing fragment becoming its own chunk. A
	// 20-character chunk embeds to a vector that matches almost anything.
	MinChunkChars = 80

	// MaxChunkChars is the true upper bound: the target, plus the overlap
	// carried in from the predecessor, plus the separator between them.
	//
	// The overlap is **additive on purpose**. Trimming the payload to make room
	// for it would mean the overlap shrank the content it exists to protect,
	// which is the opposite of the point. Every embedding model in the registry
	// takes far more than this in one input, so the slack costs nothing.
	MaxChunkChars = ChunkChars + OverlapChars + 2
)

// Chunk splits text for embedding.
//
// Paragraph boundaries first, then sentence boundaries, then a hard cut. Most
// worklogs are a paragraph and come back as one chunk, which is the intended
// case: the sizes above matter for imported documents, and **Q2 (optimal chunk
// size and overlap) is still open** — these are defaults chosen to be
// measurable, not measured.
//
// Returns nil for text that is only whitespace: an empty chunk embeds to a
// vector that is nearest-neighbour to everything.
func Chunk(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= ChunkChars {
		return []string{text}
	}

	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		s := strings.TrimSpace(current.String())
		current.Reset()
		if s == "" {
			return
		}
		out = append(out, s)
	}

	for _, para := range splitParagraphs(text) {
		for _, piece := range splitOversized(para) {
			if current.Len() > 0 && current.Len()+len(piece)+2 > ChunkChars {
				tail := overlapTail(current.String())
				flush()
				current.WriteString(tail)
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(piece)
		}
	}
	flush()

	// A final fragment shorter than MinChunkChars is folded back into its
	// predecessor rather than embedded alone.
	if len(out) > 1 && len(out[len(out)-1]) < MinChunkChars {
		out[len(out)-2] += "\n\n" + out[len(out)-1]
		out = out[:len(out)-1]
	}
	return out
}

func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitOversized breaks a single paragraph longer than ChunkChars at sentence
// boundaries, and failing that at a hard character cut.
func splitOversized(para string) []string {
	if len(para) <= ChunkChars {
		return []string{para}
	}

	var (
		out     []string
		current strings.Builder
	)
	for _, sentence := range splitSentences(para) {
		if current.Len() > 0 && current.Len()+len(sentence)+1 > ChunkChars {
			out = append(out, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if len(sentence) > ChunkChars {
			// One sentence longer than a whole chunk: a pasted log line, a URL,
			// a base64 blob. Cut it rather than emit something that will not
			// embed, and cut on a rune boundary.
			for len(sentence) > ChunkChars {
				cut := ChunkChars
				for cut > 0 && !isRuneStart(sentence[cut]) {
					cut--
				}
				out = append(out, sentence[:cut])
				sentence = sentence[cut:]
			}
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(sentence)
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func splitSentences(para string) []string {
	var (
		out   []string
		start int
	)
	runes := []rune(para)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '.' && runes[i] != '!' && runes[i] != '?' {
			continue
		}
		// A boundary only when whitespace follows, so "0.9" and "e.g." stay put.
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
			out = append(out, s)
		}
		start = i + 1
	}
	if s := strings.TrimSpace(string(runes[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// overlapTail returns the trailing OverlapChars of a chunk, trimmed forward to
// a word boundary so the next chunk does not begin mid-word.
func overlapTail(s string) string {
	if len(s) <= OverlapChars {
		return s
	}
	tail := s[len(s)-OverlapChars:]
	if i := strings.IndexFunc(tail, unicode.IsSpace); i >= 0 {
		tail = tail[i+1:]
	}
	return strings.TrimSpace(tail)
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
