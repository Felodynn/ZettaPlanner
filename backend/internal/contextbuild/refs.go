package contextbuild

import (
	"regexp"
	"sort"
	"strings"
)

// References is what a message names, resolved against what actually exists.
//
// Expansion starts here (§6.3 layer 2): "from whatever the message references".
// The resolution is **deterministic and closed** — it matches refs against the
// project's key and names against the entities that exist, and finds nothing
// else. Asking a model which items a sentence is about would make the context
// depend on a second inference, and a wrong guess there is invisible: the
// assembled context looks complete and is about the wrong item.
type References struct {
	// ItemRefs are human-facing work item refs, upper-cased: ZP-14.
	ItemRefs []string
	// EntitySlugs are catalog entity slugs named directly or by display name.
	EntitySlugs []string
}

// Empty reports whether the message named nothing.
func (r References) Empty() bool { return len(r.ItemRefs) == 0 && len(r.EntitySlugs) == 0 }

// NamedEntity is one candidate the resolver may match against.
type NamedEntity struct {
	Slug string
	Name string
}

// minNameMatch is the shortest **display name** matched case-insensitively.
//
// Three-character names — "API", "S3", "Job" — are ordinary English, and
// matching them pulls a whole architecture slice into the context on the
// strength of somebody writing "the api".
//
// The floor deliberately does not apply to slugs, short ones included. A slug
// is an identifier somebody chose and typed; a display name is prose that
// happens to be a label. The asymmetry means a short-slugged entity is
// over-included now and then rather than being unreachable by expansion
// forever — and `context show` prints what expansion started from, so an
// over-inclusion is visible in one line while a silent miss is not visible at
// all.
const minNameMatch = 4

// FindReferences resolves what a message names.
func FindReferences(message, projectKey string, entities []NamedEntity) References {
	var out References
	if strings.TrimSpace(message) == "" {
		return out
	}

	if projectKey != "" {
		// Word-bounded so that "ZP-14" in "ZP-140" does not match, and
		// case-insensitive because people type zp-14.
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(projectKey) + `-(\d+)\b`)
		seen := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(message, -1) {
			ref := strings.ToUpper(projectKey) + "-" + m[1]
			if !seen[ref] {
				seen[ref] = true
				out.ItemRefs = append(out.ItemRefs, ref)
			}
		}
	}

	lower := strings.ToLower(message)
	seen := map[string]bool{}
	for _, e := range entities {
		if e.Slug == "" || seen[e.Slug] {
			continue
		}
		if containsWord(lower, strings.ToLower(e.Slug)) ||
			(len(e.Name) >= minNameMatch && containsWord(lower, strings.ToLower(e.Name))) {
			seen[e.Slug] = true
			out.EntitySlugs = append(out.EntitySlugs, e.Slug)
		}
	}

	// Sorted so the same message always expands in the same order, which is
	// what makes two identical turns produce byte-identical context.
	sort.Strings(out.ItemRefs)
	sort.Strings(out.EntitySlugs)
	return out
}

// containsWord reports whether needle appears in haystack delimited by
// something other than a word character. Both are already lower-cased.
//
// A plain strings.Contains would match "auth-service" inside
// "reauth-serviceable", and hyphens mean the standard word-boundary regex does
// not do what is wanted here either.
func containsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		if !wordChar(byteAt(haystack, start-1)) && !wordChar(byteAt(haystack, end)) {
			return true
		}
		i = start + 1
		if i >= len(haystack) {
			return false
		}
	}
}

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return ' '
	}
	return s[i]
}

func wordChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
