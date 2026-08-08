package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// The tool inventory is the enforcement mechanism, not prompt instruction
// (BACKEND.md Phase 6).
//
// **There is no direct-write tool for a manager-owned field.** Not a tool that
// refuses — a tool that does not exist. `update_dev_field` is the only write
// path onto a work item and it validates against the ownership table and the
// requesting user's assignment, so even it cannot be used to reach one.
//
// A prompt saying "do not change due dates" is a request. An inventory without
// a tool that changes due dates is a fact, and it holds when the model is
// confused, adversarial, or simply reading a stale system prompt from a
// context window that got truncated.

// Kind groups tools by what they can do.
type Kind string

const (
	KindRead    Kind = "read"
	KindWrite   Kind = "write"
	KindPropose Kind = "propose"
	KindControl Kind = "control"
)

// Result is what a tool did.
type Result struct {
	// Content is what the model sees. Written to teach: a refusal names the
	// boundary and the route, a success says what actually changed.
	Content string

	// Produced marks a tool call as real output. A turn that ends having
	// produced nothing terminates as `no_output` rather than as success — a
	// turn that declines in prose looks identical to a completed one
	// otherwise, which is the gap LumixAgents Phase 6 left open.
	Produced bool

	// Denied marks a refusal, for the escalation counter and the trace.
	Denied bool

	// Stop ends the loop. Set by the control tools.
	Stop bool
}

// Tool is one callable capability.
type Tool struct {
	Name string
	Kind Kind
	// Object names what a write tool writes, so the ownership table can be
	// asked whether its parameters are things this agent may set. Empty for
	// read, propose and control tools, which write nothing.
	Object      policy.ObjectKind
	Description string
	// Params is the JSON Schema offered to the model. Hand-written rather than
	// reflected, because the description text inside it is prompt: it is where
	// a closed vocabulary gets taught and where a boundary gets named before
	// the model trips over it.
	Params json.RawMessage
	Run    func(ctx context.Context, s *session, a args) (Result, error)
}

// inventory returns every tool, in a fixed order.
func inventory() []Tool {
	var out []Tool
	out = append(out, readTools()...)
	out = append(out, writeTools()...)
	out = append(out, proposeTools()...)
	out = append(out, controlTools()...)
	return out
}

// toolset indexes the inventory by name.
type toolset struct {
	byName map[string]Tool
	order  []string
}

func newToolset(tools []Tool) *toolset {
	ts := &toolset{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		ts.byName[t.Name] = t
		ts.order = append(ts.order, t.Name)
	}
	sort.Strings(ts.order)
	return ts
}

func (ts *toolset) get(name string) (Tool, bool) {
	t, ok := ts.byName[normaliseToolName(name)]
	return t, ok
}

func (ts *toolset) names() []string { return ts.order }

// offered renders the inventory for the model.
func (ts *toolset) offered() []llm.Tool {
	out := make([]llm.Tool, 0, len(ts.order))
	for _, name := range ts.order {
		t := ts.byName[name]
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Params,
			},
		})
	}
	return out
}

// ── arguments ────────────────────────────────────────────────────────────────

// args is a tool call's arguments as the model sent them.
//
// The accessors are forgiving about *shape* and strict about *meaning*: a model
// that sends 40 where a string was asked for meant 40, and rejecting it teaches
// nothing. One that omits a required argument entirely gets a refusal naming
// the argument, because guessing what it meant is how a turn edits the wrong
// item.
type args map[string]any

func (a args) str(key string) string {
	v, ok := a[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func (a args) required(key string) (string, error) {
	if v := a.str(key); v != "" {
		return v, nil
	}
	return "", missingArg(key)
}

func (a args) intVal(key string) (int, bool) {
	v, ok := a[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

func (a args) boolVal(key string) (bool, bool) {
	v, ok := a[key]
	if !ok || v == nil {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return b, err == nil
	}
	return false, false
}

func (a args) strs(key string) []string {
	v, ok := a[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		// A model that sends "ZP-1, ZP-2" for an array meant two items.
		var out []string
		for _, part := range strings.Split(t, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// objs reads an array of objects, which is how a proposal's changes arrive.
func (a args) objs(key string) []args {
	v, ok := a[key]
	if !ok || v == nil {
		return nil
	}
	raw, isSlice := v.([]any)
	if !isSlice {
		if single, isObj := v.(map[string]any); isObj {
			return []args{args(single)}
		}
		return nil
	}
	out := make([]args, 0, len(raw))
	for _, item := range raw {
		if m, isObj := item.(map[string]any); isObj {
			out = append(out, args(m))
		}
	}
	return out
}

// obj reads a nested object, which is how a change's `after` arrives.
func (a args) obj(key string) map[string]any {
	v, ok := a[key]
	if !ok || v == nil {
		return nil
	}
	if m, isObj := v.(map[string]any); isObj {
		return m
	}
	// Some models send a nested object as a JSON string. Accepting that is not
	// laxity: the alternative is refusing a call whose meaning is unambiguous.
	if s, isStr := v.(string); isStr {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
	}
	return nil
}

// missingArg is a refusal that names the argument rather than the call.
func missingArg(key string) error {
	return &toolError{Message: fmt.Sprintf(
		"this call is missing the required argument `%s`. Send it and call again — "+
			"the harness will not guess which one you meant, because guessing is how a turn edits the wrong thing.", key)}
}

// toolError is a refusal the model should read and act on, as distinct from a
// harness failure it can do nothing about.
type toolError struct {
	Message string
	// Denied marks a policy refusal rather than a malformed call. Both are
	// returned to the model; only the first escalates.
	Denied bool
}

func (e *toolError) Error() string { return e.Message }

func denied(format string, a ...any) error {
	return &toolError{Message: fmt.Sprintf(format, a...), Denied: true}
}

func refuse(format string, a ...any) error {
	return &toolError{Message: fmt.Sprintf(format, a...)}
}

// schema is a small helper for the hand-written parameter schemas.
func schema(s string) json.RawMessage { return json.RawMessage(s) }
