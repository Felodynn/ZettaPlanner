package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
)

// Startup validation (D4, amended by D20).
//
// CONTRACT §8 requires three checks per model: /api/show for capability and
// maximum window, a live one-token completion, and /api/ps for the window the
// server actually allocated. The third one is the only check that proves
// num_ctx took effect, and it is the reason D4 exists.
//
// It does not work for Ollama Cloud. Verified against Ollama 0.30.11: a
// completed chat against glm-5.2:cloud leaves /api/ps returning
// {"models":[]}, because the weights are not on this machine. Rather than drop
// the guarantee for the two models v0 actually runs on, validation is tiered:
//
//	self_hosted → /api/show + live completion + /api/ps allocated == num_ctx
//	cloud       → /api/show + live completion + context probe
//
// The probe sends a large filler prompt and reads prompt_eval_count back. That
// number is the proof: a server reporting it evaluated 20,000 prompt tokens has
// a window of at least 20,000 tokens. A server that silently clamped to 4096
// reports 4096 no matter how much we send, and doubling the filler without
// moving the count is a clamp caught red-handed.
//
// The probe proves a floor, not the whole window, and says so. Proving the full
// 262,144 costs 262,144 input tokens; `models check --full-probe` will pay it
// on demand.

// DefaultProbeTokens is the floor the boot probe demands a cloud model prove.
// Large enough to rule out every common silent clamp (2048, 4096, 8192, 16384),
// small enough to pay on every restart.
const DefaultProbeTokens = 16384

// probeTokensPerWord is the observed expansion of probeFiller's unique
// alphanumeric words into tokens. Only a sizing hint: the probe never trusts it,
// it reads prompt_eval_count back and grows the filler if it fell short.
const probeTokensPerWord = 4

// ProofMethod records how a model's window was established.
type ProofMethod string

const (
	ProofPS    ProofMethod = "ps"    // /api/ps reported the allocated window
	ProofProbe ProofMethod = "probe" // prompt_eval_count established a floor
	ProofNone  ProofMethod = "none"  // embedding models have no window to prove
)

// Report is the outcome of validating one registry entry.
type Report struct {
	Name     string      `json:"name"`
	Model    string      `json:"model"`
	Endpoint string      `json:"endpoint"`
	Tier     config.Tier `json:"tier"`

	Capabilities  []string `json:"capabilities"`   // as reported by /api/show
	MaxContext    int      `json:"max_context"`    // the model's ceiling
	ConfiguredCtx int      `json:"configured_ctx"` // num_ctx we ask for

	// ProvenCtx is the window we can actually stand behind, and ProofMethod
	// says how. For a probe it is a floor, not an equality.
	ProvenCtx   int         `json:"proven_ctx"`
	ProofMethod ProofMethod `json:"proof_method"`
	ProofCached bool        `json:"proof_cached"`

	// FormatEnforced records whether the server honours a JSON Schema in
	// `format` or accepts it and answers in prose anyway (D38). Not a failure:
	// callers that need structure state it in the prompt and validate the
	// answer. It is reported because a deployment should know which of the two
	// it is running on.
	FormatEnforced bool `json:"format_enforced"`

	EmbeddingDim int           `json:"embedding_dim,omitempty"`
	Latency      time.Duration `json:"latency"`

	OK       []string `json:"ok"`
	Problems []string `json:"problems"`
}

// Failed reports whether this model is unusable.
func (r *Report) Failed() bool { return len(r.Problems) > 0 }

func (r *Report) pass(format string, args ...any) {
	r.OK = append(r.OK, fmt.Sprintf(format, args...))
}

func (r *Report) fail(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

// Registry holds one validated client per configured model and resolves roles.
type Registry struct {
	cfg     *config.Models
	clients map[string]*Client
	reports map[string]*Report
}

// NewRegistry builds clients for every configured model. Nothing is contacted
// until Validate runs.
func NewRegistry(cfg *config.Models) *Registry {
	r := &Registry{
		cfg:     cfg,
		clients: make(map[string]*Client, len(cfg.Models)),
		reports: make(map[string]*Report, len(cfg.Models)),
	}
	for _, m := range cfg.All() {
		r.clients[m.Name] = New(m)
	}
	return r
}

// Client returns the client for a registry entry.
func (r *Registry) Client(name string) (*Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Candidates returns the clients eligible for a role, in preference order.
//
// The caller picks one and holds it for a whole turn (D22). Falling back
// mid-turn changes the context window and the tool-calling behaviour underneath
// a running loop, and makes both the trace and the Phase 6 measurements
// unreadable.
func (r *Registry) Candidates(role string) ([]*Client, error) {
	models, err := r.cfg.Resolve(role)
	if err != nil {
		return nil, err
	}
	out := make([]*Client, 0, len(models))
	for _, m := range models {
		if c, ok := r.clients[m.Name]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// Reports returns the last validation outcome per model, in stable order.
func (r *Registry) Reports() []*Report {
	if r == nil {
		return nil
	}
	out := make([]*Report, 0, len(r.reports))
	for _, m := range r.cfg.All() {
		if rep, ok := r.reports[m.Name]; ok {
			out = append(out, rep)
		}
	}
	return out
}

// ValidateOptions tunes the startup checks.
type ValidateOptions struct {
	// FullProbe makes cloud models prove their entire configured window
	// instead of the DefaultProbeTokens floor. Costs num_ctx input tokens per
	// cloud model.
	FullProbe bool

	// NoCache forces a fresh probe even if a recent identical one succeeded.
	NoCache bool

	// CacheDir holds the probe cache. Empty disables caching entirely.
	CacheDir string

	// ChunkDim is the dimension of the chunks.embedding column, supplied by
	// the store. Embedding models are asserted against it. Zero skips the
	// check, which is correct only before the first migration has run.
	ChunkDim int

	Log *slog.Logger
}

// Validate runs the tiered checks against every configured model and returns
// one report each. The error is non-nil if any model is unusable; the reports
// are still returned so the caller can print all of the problems at once rather
// than making the operator fix them one restart at a time.
func (r *Registry) Validate(ctx context.Context, opts ValidateOptions) ([]*Report, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	cache := loadProbeCache(opts.CacheDir)

	var failed []string
	for _, m := range r.cfg.All() {
		rep := r.validateOne(ctx, m, opts, cache, log)
		r.reports[m.Name] = rep
		if rep.Failed() {
			failed = append(failed, m.Name)
		}
	}
	cache.save(opts.CacheDir)

	if len(failed) == 0 {
		return r.Reports(), nil
	}
	return r.Reports(), fmt.Errorf("model validation failed for %s; run `zettaplanner models check` for the detail",
		strings.Join(failed, ", "))
}

func (r *Registry) validateOne(ctx context.Context, m *config.Model, opts ValidateOptions, cache *probeCache, log *slog.Logger) *Report {
	c := r.clients[m.Name]
	rep := &Report{
		Name:          m.Name,
		Model:         m.Model,
		Endpoint:      m.Endpoint,
		Tier:          m.Tier,
		ConfiguredCtx: m.NumCtx,
		ProofMethod:   ProofNone,
	}

	// ── 1. /api/show: capability and ceiling ────────────────────────────
	show, err := c.Show(ctx)
	if err != nil {
		rep.fail("/api/show failed: %v", err)
		return rep
	}
	rep.Capabilities = show.Capabilities
	rep.pass("/api/show reachable; capabilities [%s]", strings.Join(show.Capabilities, ", "))

	for _, cap := range m.Caps {
		if !cap.Verifiable() {
			continue
		}
		if !show.HasCapability(string(cap)) {
			rep.fail("config declares the %q capability but /api/show reports only [%s]; either the wrong model tag is configured or that capability should be removed from models.%s.caps",
				cap, strings.Join(show.Capabilities, ", "), m.Name)
		}
	}

	if max, ok := show.MaxContext(); ok {
		rep.MaxContext = max
		if m.NumCtx > max {
			rep.fail("num_ctx is %d but %s tops out at %d; lower models.%s.num_ctx to %d or below",
				m.NumCtx, m.Model, max, m.Name, max)
		}
	} else if !m.IsEmbedding() {
		rep.pass("/api/show reports no context_length for %s; the ceiling could not be checked", m.Model)
	}

	// ── 2. live call ────────────────────────────────────────────────────
	if m.IsEmbedding() {
		r.validateEmbedding(ctx, c, m, show, rep, opts)
		return rep
	}

	start := time.Now()
	one := 1
	no := false
	resp, err := c.Chat(ctx, ChatRequest{
		Messages:   []Message{{Role: "user", Content: "Reply with the single word OK."}},
		Think:      &no,
		NumPredict: &one,
	})
	rep.Latency = time.Since(start)
	if err != nil {
		rep.fail("live completion failed: %v", err)
		return rep
	}
	rep.pass("live completion in %s (%d prompt tokens, %d generated)",
		rep.Latency.Round(time.Millisecond), resp.PromptEvalCount, resp.EvalCount)

	// ── 3. structured output (D38) ──────────────────────────────────────
	r.checkFormatEnforcement(ctx, c, rep)

	// ── 4. window proof, tiered (D20) ───────────────────────────────────
	switch m.Tier {
	case config.TierSelfHosted:
		r.proveByPS(ctx, c, m, rep)
	case config.TierCloud:
		r.proveByProbe(ctx, c, m, rep, opts, cache, log)
	}
	return rep
}

// formatProbeSchema is the smallest schema that can tell enforcement from
// prose. Deliberately tiny: this runs at every boot, for every model.
var formatProbeSchema = json.RawMessage(
	`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)

// checkFormatEnforcement finds out whether the server honours `format`.
//
// It does not always. Measured against Ollama 0.30.11: a request carrying a
// JSON Schema in `format` is answered with a schema-conforming object by a
// locally-served gemma4:12b and with markdown prose by gemma4:31b-cloud and
// glm-5.2:cloud. The field is accepted and discarded, exactly as
// /v1/chat/completions discards num_ctx (D4) and exactly as /api/ps reports
// nothing for a cloud model (D20). Three times now the same shape: a guarantee
// that holds for weights we run and silently does not for weights we rent.
//
// This is a warning, not a failure. The ingest extractor copes — it restates
// the shape in the prompt, finds the object inside whatever prose arrives, and
// retries with a teaching error — but "copes" is a different thing from
// "guaranteed", and which one a deployment is getting should be visible in
// `models check` rather than discovered from a diagram with no boxes in it.
func (r *Registry) checkFormatEnforcement(ctx context.Context, c *Client, rep *Report) {
	no := false
	zero := 0.0
	resp, err := c.Chat(ctx, ChatRequest{
		Messages: []Message{{Role: "user", Content: "What is the capital of France?"}},
		Format:   formatProbeSchema,
		Think:    &no,
		Options:  Options{Temperature: &zero},
	})
	if err != nil {
		rep.pass("structured-output check could not run: %v", err)
		return
	}

	var probe struct {
		Answer *string `json:"answer"`
	}
	content := strings.TrimSpace(resp.Message.Content)
	if err := json.Unmarshal([]byte(content), &probe); err == nil && probe.Answer != nil {
		rep.FormatEnforced = true
		rep.pass("the server enforces `format`: a schema-constrained request came back as a conforming object")
		return
	}

	rep.FormatEnforced = false
	rep.pass("the server ACCEPTS BUT IGNORES `format`: a schema-constrained request came back as %s. "+
		"Structured output is not guaranteed for this model; callers must state the shape in the prompt and validate the answer themselves (D38)",
		truncate(content, 80))
}

// proveByPS is the original D4 check: only /api/ps says what the server
// actually allocated.
func (r *Registry) proveByPS(ctx context.Context, c *Client, m *config.Model, rep *Report) {
	loaded, err := c.Loaded(ctx)
	if err != nil {
		rep.fail("/api/ps failed: %v", err)
		return
	}
	if loaded == nil {
		rep.fail("/api/ps does not list %s as loaded immediately after a completion; if models.%s.keep_alive is very short, raise it — the allocated window cannot be checked otherwise",
			m.Model, m.Name)
		return
	}
	rep.ProofMethod = ProofPS
	rep.ProvenCtx = loaded.ContextLength
	if loaded.ContextLength != m.NumCtx {
		rep.fail("num_ctx is %d but %s allocated %d; the server did not honour the request, and every prompt above %d tokens will be silently truncated",
			m.NumCtx, m.Endpoint, loaded.ContextLength, loaded.ContextLength)
		return
	}
	rep.pass("/api/ps confirms an allocated window of %d tokens", loaded.ContextLength)
}

// proveByProbe establishes a floor for a model whose runtime we do not control.
func (r *Registry) proveByProbe(ctx context.Context, c *Client, m *config.Model, rep *Report, opts ValidateOptions, cache *probeCache, log *slog.Logger) {
	rep.ProofMethod = ProofProbe

	target := DefaultProbeTokens
	if opts.FullProbe || m.NumCtx < target {
		target = m.NumCtx
	}

	key := probeKey(m, target)
	if !opts.NoCache {
		if proven, ok := cache.get(key); ok {
			rep.ProvenCtx = proven
			rep.ProofCached = true
			rep.pass("context probe (cached, within %s) proved a window of at least %d tokens", probeTTL, proven)
			r.noteProbeCeiling(m, rep, target)
			return
		}
	}

	log.InfoContext(ctx, "probing cloud model context window",
		"model", m.Model, "registry", m.Name, "target_tokens", target)

	proven, err := c.probeWindow(ctx, target)
	if err != nil {
		if ClassOf(err) == ClassContextOverflow {
			rep.fail("context probe was refused as an overflow at %d tokens, but models.%s.num_ctx claims %d; the real window is smaller than configured",
				target, m.Name, m.NumCtx)
			return
		}
		rep.fail("context probe failed: %v", err)
		return
	}
	rep.ProvenCtx = proven
	if proven < target {
		rep.fail("context probe evaluated only %d prompt tokens after the filler was grown twice, so %s is clamping well below the configured num_ctx of %d; every prompt above %d tokens will be silently truncated",
			proven, m.Endpoint, m.NumCtx, proven)
		return
	}
	rep.pass("context probe proved a window of at least %d tokens (%d prompt tokens evaluated)", target, proven)
	cache.put(key, proven)
	r.noteProbeCeiling(m, rep, target)
}

// noteProbeCeiling records, in the report itself, exactly what the probe did
// not prove. Silent truncation is the failure mode; a validation that overstates
// what it checked is the same failure one level up.
func (r *Registry) noteProbeCeiling(m *config.Model, rep *Report, target int) {
	if target < m.NumCtx {
		rep.pass("the window above %d tokens is unverified; `zettaplanner models check --full-probe` proves the configured %d at a cost of %d input tokens",
			target, m.NumCtx, m.NumCtx)
	}
}

func (r *Registry) validateEmbedding(ctx context.Context, c *Client, m *config.Model, show *ShowResponse, rep *Report, opts ValidateOptions) {
	start := time.Now()
	resp, err := c.Embed(ctx, "zettaplanner embedding dimension probe")
	rep.Latency = time.Since(start)
	if err != nil {
		rep.fail("live embedding call failed: %v", err)
		return
	}
	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
		rep.fail("live embedding call returned no vector")
		return
	}

	got := len(resp.Embeddings[0])
	rep.EmbeddingDim = got
	rep.pass("live embedding in %s, %d dimensions", rep.Latency.Round(time.Millisecond), got)

	if got != m.Dim {
		rep.fail("models.%s.dim is %d but %s returns %d-dimensional vectors; correct the config or configure the model you meant",
			m.Name, m.Dim, m.Model, got)
	}
	if declared, ok := show.EmbeddingLength(); ok && declared != got {
		rep.pass("note: /api/show reports embedding_length %d while the live call returned %d", declared, got)
	}

	// The check that matters. A dimension change against an existing corpus
	// does not fail at query time — it silently searches a mixed vector space,
	// and every retrieval after it is quietly wrong.
	if opts.ChunkDim > 0 && got != opts.ChunkDim {
		rep.fail("the chunks.embedding column is VECTOR(%d) but %s returns %d dimensions; changing the embedding model requires re-embedding the corpus (CONTRACT §8) — restore the previous model or run the re-embed migration",
			opts.ChunkDim, m.Model, got)
	} else if opts.ChunkDim > 0 {
		rep.pass("dimension matches the chunks.embedding column (%d)", opts.ChunkDim)
	}
}

// probeWindow sends filler and reads prompt_eval_count back.
//
// Returns the number of prompt tokens the server reports having evaluated,
// which is a hard floor on its window. If the first attempt falls short of the
// target, the filler is doubled once: a count that does not move when the input
// doubles is a clamp, and reporting the stuck value names the real window.
func (c *Client) probeWindow(ctx context.Context, target int) (int, error) {
	// Measured against glm-5.2:cloud on Ollama 0.30.11: 4096 filler words
	// evaluated as 20,891 prompt tokens, so roughly five tokens per word. Start
	// below the target on that ratio and grow rather than above it and waste;
	// the loop corrects either way, and only the cheap case is common.
	words := target / probeTokensPerWord
	if words < 64 {
		words = 64
	}
	var best int

	for attempt := 0; attempt < 3; attempt++ {
		four := 4
		no := false
		resp, err := c.Chat(ctx, ChatRequest{
			Messages: []Message{
				{Role: "user", Content: probeFiller(words) + "\n\nReply with the single word OK."},
			},
			Think:      &no,
			NumPredict: &four,
		})
		if err != nil {
			// A tool_parse from an empty single-token reply is irrelevant here:
			// the prompt_eval_count is what the probe wanted, and we have it.
			if resp != nil && resp.PromptEvalCount > best {
				best = resp.PromptEvalCount
			}
			if ClassOf(err) != ClassToolParse {
				return best, err
			}
		} else if resp.PromptEvalCount > best {
			best = resp.PromptEvalCount
		}

		if best >= target {
			return best, nil
		}
		words *= 2
	}
	return best, nil
}

// probeFiller builds n whitespace-separated unique tokens. Uniqueness matters:
// a repeated string invites nothing in particular from a tokeniser, but unique
// alphanumeric words cannot merge across the space boundary, so the token count
// is never below the word count.
func probeFiller(n int) string {
	var b strings.Builder
	b.Grow(n * 8)
	b.WriteString("Ignore the following filler; it exists only to measure the context window.\n")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "zp%06dx", i)
	}
	return b.String()
}

// ── probe cache ──────────────────────────────────────────────────────────────

// probeTTL is how long a successful probe stands. Long enough that a
// development restart loop does not pay for one every time, short enough that a
// provider quietly changing an allocation is caught the same day.
const probeTTL = 24 * time.Hour

const probeCacheFile = "probe-cache.json"

type probeEntry struct {
	Proven int       `json:"proven"`
	At     time.Time `json:"at"`
}

type probeCache struct {
	Entries map[string]probeEntry `json:"entries"`
	dirty   bool
}

func probeKey(m *config.Model, target int) string {
	return fmt.Sprintf("%s|%s|%d|%d", m.Endpoint, m.Model, m.NumCtx, target)
}

func loadProbeCache(dir string) *probeCache {
	c := &probeCache{Entries: map[string]probeEntry{}}
	if dir == "" {
		return c
	}
	raw, err := os.ReadFile(filepath.Join(dir, probeCacheFile))
	if err != nil {
		return c
	}
	_ = json.Unmarshal(raw, c)
	if c.Entries == nil {
		c.Entries = map[string]probeEntry{}
	}
	return c
}

func (c *probeCache) get(key string) (int, bool) {
	e, ok := c.Entries[key]
	if !ok || time.Since(e.At) > probeTTL {
		return 0, false
	}
	return e.Proven, true
}

func (c *probeCache) put(key string, proven int) {
	c.Entries[key] = probeEntry{Proven: proven, At: time.Now()}
	c.dirty = true
}

func (c *probeCache) save(dir string) {
	if dir == "" || !c.dirty {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, probeCacheFile), raw, 0o600)
}
