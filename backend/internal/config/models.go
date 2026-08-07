package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Tier says who runs the weights, which decides how hard the startup check can
// look (D20).
//
// It is deliberately not the same axis as "is the endpoint on this machine".
// A self-hosted Ollama on another host is still self_hosted: we control the
// runtime, so /api/ps reports the window it actually allocated and that number
// is authoritative. Ollama Cloud runs the model on someone else's fleet and
// reports nothing in /api/ps — verified empirically: a completed chat against
// glm-5.2:cloud leaves /api/ps returning {"models":[]}. For that tier the
// allocated window has to be proven a different way (see llm.Validate).
type Tier string

const (
	TierSelfHosted Tier = "self_hosted"
	TierCloud      Tier = "cloud"
)

// Cap is a model capability. The first five are reported by /api/show and are
// checked against it at boot; anything else is a free-form label the config may
// carry for role matching (CONTRACT §8 uses `long_context` this way) and is
// never asserted against the server.
type Cap string

const (
	CapTools      Cap = "tools"
	CapVision     Cap = "vision"
	CapEmbedding  Cap = "embedding"
	CapThinking   Cap = "thinking"
	CapCompletion Cap = "completion"
)

// Verifiable reports whether /api/show is expected to confirm this capability.
func (c Cap) Verifiable() bool {
	switch c {
	case CapTools, CapVision, CapEmbedding, CapThinking, CapCompletion:
		return true
	}
	return false
}

// Role names the job a model is selected for.
const (
	RolePlanner = "planner"
	RoleVision  = "vision"
	RoleEmbed   = "embed"
)

// requiredRoles must all resolve at boot. A deployment missing any of them
// cannot serve its corresponding feature, and discovering that at the first
// ingest rather than at startup is the failure D4 exists to prevent.
var requiredRoles = []string{RolePlanner, RoleVision, RoleEmbed}

// Model is one entry in the registry.
type Model struct {
	// Name is the registry key. Populated on load, never read from the file.
	Name string `yaml:"-"`

	Endpoint string `yaml:"endpoint"`
	Tier     Tier   `yaml:"tier"`
	Model    string `yaml:"model"`
	Caps     []Cap  `yaml:"caps"`

	// NumCtx is mandatory for generation models and always sent on every
	// request (D4). Never left to the server's default.
	NumCtx int `yaml:"num_ctx"`

	// AssemblyBudget is the token ceiling context assembly targets, which is
	// deliberately not NumCtx. glm-5.2:cloud advertises a 1,000,000 window;
	// filling it is neither affordable nor good for recall. Defaults to NumCtx
	// when unset. Must not exceed it.
	AssemblyBudget int `yaml:"assembly_budget"`

	// Dim is the embedding dimension, required for embedding models and
	// asserted against both the live model and the chunks.embedding column.
	Dim int `yaml:"dim"`

	Concurrency int      `yaml:"concurrency"`
	KeepAlive   Duration `yaml:"keep_alive"`
	Timeout     Duration `yaml:"timeout"`

	// AuthEnv names an environment variable holding a bearer token. Unset is
	// normal: cloud models reached through a local Ollama daemon authenticate
	// with the daemon's own signing key and need no token here.
	AuthEnv string `yaml:"auth_env"`
}

// HasCap reports whether the model declares c.
func (m *Model) HasCap(c Cap) bool {
	for _, have := range m.Caps {
		if have == c {
			return true
		}
	}
	return false
}

// AuthToken returns the bearer token for this model, if one is configured.
func (m *Model) AuthToken() string {
	if m.AuthEnv == "" {
		return ""
	}
	return os.Getenv(m.AuthEnv)
}

// IsEmbedding reports whether this model serves the embed role.
func (m *Model) IsEmbedding() bool { return m.HasCap(CapEmbedding) }

// RoleSpec binds a job to the capabilities it needs and the models that may do
// it, in preference order.
type RoleSpec struct {
	Requires []Cap    `yaml:"requires"`
	Prefer   []string `yaml:"prefer"`
}

// Models is the parsed contents of models.yaml (CONTRACT §8).
type Models struct {
	Models map[string]*Model    `yaml:"models"`
	Roles  map[string]*RoleSpec `yaml:"roles"`

	// Path records where this came from, so validation errors can name the
	// file the operator has to edit.
	Path string `yaml:"-"`
}

// Duration is a time.Duration that parses from a YAML string ("30m").
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("expected a duration string such as \"30m\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a duration (try \"30m\", \"90s\"): %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }
func (d Duration) String() string          { return time.Duration(d).String() }

// LoadModels reads and structurally validates models.yaml.
//
// Structural only: everything here is checkable without touching a model
// server. The live checks — /api/show capabilities, an actual completion, the
// allocated window, the embedding dimension — are llm.Validate's job, because
// they need a client and a database connection.
func LoadModels(path string) (*Models, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("model registry %s does not exist; create it (see CONTRACT.md §8) or set ZP_MODELS_PATH", path)
		}
		return nil, fmt.Errorf("reading model registry %s: %w", path, err)
	}

	var m Models
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	// A misspelt key must fail loudly. Silently defaulting num_ctx because the
	// operator wrote num_context is precisely the D4 failure in a new costume.
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	m.Path = path

	if err := m.normalise(); err != nil {
		return nil, err
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Models) normalise() error {
	if m.Models == nil {
		return fmt.Errorf("%s declares no models; add a `models:` block (see CONTRACT.md §8)", m.Path)
	}
	for name, mdl := range m.Models {
		if mdl == nil {
			return fmt.Errorf("%s: model %q is empty", m.Path, name)
		}
		mdl.Name = name
		if mdl.Concurrency == 0 {
			mdl.Concurrency = 1
		}
		if mdl.KeepAlive == 0 {
			mdl.KeepAlive = Duration(30 * time.Minute)
		}
		if mdl.Timeout == 0 {
			mdl.Timeout = Duration(10 * time.Minute)
		}
		if mdl.AssemblyBudget == 0 {
			mdl.AssemblyBudget = mdl.NumCtx
		}
		mdl.Endpoint = strings.TrimRight(mdl.Endpoint, "/")
	}
	return nil
}

func (m *Models) validate() error {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	for _, name := range m.modelNames() {
		mdl := m.Models[name]
		where := fmt.Sprintf("models.%s", name)

		if mdl.Endpoint == "" {
			fail("%s.endpoint is empty; set it to an Ollama base URL such as http://localhost:11434", where)
		} else if !strings.HasPrefix(mdl.Endpoint, "http://") && !strings.HasPrefix(mdl.Endpoint, "https://") {
			fail("%s.endpoint %q has no scheme; write http:// or https://", where, mdl.Endpoint)
		}
		if mdl.Model == "" {
			fail("%s.model is empty; set it to an Ollama model tag such as glm-5.2:cloud", where)
		}
		switch mdl.Tier {
		case TierSelfHosted, TierCloud:
		case "":
			fail("%s.tier is unset; write %q if you run the weights (/api/ps proves the window) or %q for Ollama Cloud",
				where, TierSelfHosted, TierCloud)
		default:
			fail("%s.tier is %q; the only values are %q and %q", where, mdl.Tier, TierSelfHosted, TierCloud)
		}
		if len(mdl.Caps) == 0 {
			fail("%s.caps is empty; declare at least one of tools, vision, embedding", where)
		}
		if mdl.Concurrency < 1 {
			fail("%s.concurrency is %d; it must be at least 1", where, mdl.Concurrency)
		}

		if mdl.IsEmbedding() {
			if mdl.Dim <= 0 {
				fail("%s.dim is unset; an embedding model must declare its vector dimension (bge-m3 is 1024) so it can be checked against the chunks.embedding column", where)
			}
			continue
		}

		// Generation models from here down.
		if mdl.NumCtx <= 0 {
			fail("%s.num_ctx is unset; it MUST be explicit (D4) — a model that silently truncates at 4096 produces agents with apparent amnesia", where)
		}
		if mdl.AssemblyBudget > mdl.NumCtx {
			fail("%s.assembly_budget (%d) exceeds num_ctx (%d); the budget is what context assembly targets and cannot be larger than the window",
				where, mdl.AssemblyBudget, mdl.NumCtx)
		}
		if mdl.Dim != 0 {
			fail("%s.dim is set but %s has no `embedding` capability; dim belongs only on embedding models", where, name)
		}
	}

	if m.Roles == nil {
		fail("%s declares no roles; add a `roles:` block binding planner, vision and embed to models", m.Path)
	}
	for _, role := range requiredRoles {
		spec, ok := m.Roles[role]
		if !ok || spec == nil {
			fail("roles.%s is missing; every deployment needs one (see CONTRACT.md §8)", role)
			continue
		}
		if len(spec.Prefer) == 0 {
			fail("roles.%s.prefer is empty; list at least one model name from the models block, best first", role)
			continue
		}
		for i, want := range spec.Prefer {
			mdl, ok := m.Models[want]
			if !ok {
				fail("roles.%s.prefer[%d] names %q, which is not defined under models: (defined: %s)",
					role, i, want, strings.Join(m.modelNames(), ", "))
				continue
			}
			for _, req := range spec.Requires {
				if !mdl.HasCap(req) {
					fail("roles.%s requires the %q capability but model %q declares only [%s]; either add the capability or drop it from prefer",
						role, req, want, capsString(mdl.Caps))
				}
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%s is not usable:\n  - %s", m.Path, strings.Join(problems, "\n  - "))
}

func (m *Models) modelNames() []string {
	names := make([]string, 0, len(m.Models))
	for name := range m.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func capsString(caps []Cap) string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return strings.Join(out, ", ")
}

// Resolve returns the models eligible for a role, in preference order.
//
// Fallback ordering is consumed at turn boundaries only (D22): the chosen model
// is fixed for the whole turn and recorded on agent_turns.model. A turn that
// spans two models has two different context windows and two different
// tool-calling behaviours, and its trace cannot be read.
func (m *Models) Resolve(role string) ([]*Model, error) {
	spec, ok := m.Roles[role]
	if !ok || spec == nil {
		return nil, fmt.Errorf("no role %q in %s", role, m.Path)
	}
	out := make([]*Model, 0, len(spec.Prefer))
	for _, name := range spec.Prefer {
		if mdl, ok := m.Models[name]; ok {
			out = append(out, mdl)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("role %q resolves to no model in %s", role, m.Path)
	}
	return out, nil
}

// All returns every configured model, in stable name order.
func (m *Models) All() []*Model {
	names := m.modelNames()
	out := make([]*Model, 0, len(names))
	for _, n := range names {
		out = append(out, m.Models[n])
	}
	return out
}
