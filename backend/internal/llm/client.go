// Package llm is the only place that speaks to a model server.
//
// Ollama-native /api/chat, for local and cloud alike (D4). /v1/chat/completions
// silently discards num_ctx, and a model that truncates at 4096 produces agents
// with apparent amnesia that cost days to diagnose. Every request here sets
// options.num_ctx explicitly and a keep_alive long enough to survive a turn.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
)

// Client talks to one configured model. One Client per registry entry, not one
// per endpoint: the num_ctx, keep_alive and timeout it applies are the model's,
// and sharing a client across models is how a request ends up carrying the
// wrong window.
type Client struct {
	cfg  *config.Model
	http *http.Client
}

// New builds a client for a registry entry.
func New(m *config.Model) *Client {
	return &Client{
		cfg: m,
		http: &http.Client{
			Timeout: m.Timeout.Duration(),
			Transport: &http.Transport{
				MaxIdleConnsPerHost: m.Concurrency + 1,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Config returns the registry entry this client was built from.
func (c *Client) Config() *config.Model { return c.cfg }

func (c *Client) ref() modelRef {
	return modelRef{registry: c.cfg.Name, model: c.cfg.Model, endpoint: c.cfg.Endpoint}
}

// ── wire types ───────────────────────────────────────────────────────────────

// Message is one turn of an Ollama conversation.
type Message struct {
	Role      string     `json:"role"` // system | user | assistant | tool
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"` // base64, no data: prefix
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// ToolCall is a model-emitted call.
type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction carries the name and arguments of a tool call.
type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Tool is a tool offered to the model.
type Tool struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes one callable tool.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Options are the per-request generation options. num_ctx is always populated.
type Options struct {
	NumCtx      int      `json:"num_ctx"`
	NumPredict  *int     `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// ChatRequest is a call to /api/chat. Model, Options.NumCtx and KeepAlive are
// filled in by the client from the registry entry; callers do not set them.
type ChatRequest struct {
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`

	// Format constrains the output to a JSON schema. Ingest extraction uses
	// this rather than asking for JSON in prose: in LumixAgents Phase 4 a model
	// asked for JSON in prose left content empty and put everything in its
	// reasoning channel, failing roughly half of all calls.
	Format json.RawMessage `json:"format,omitempty"`

	// Think enables or suppresses the reasoning channel. nil leaves the
	// model's default in place.
	Think *bool `json:"think,omitempty"`

	Options Options `json:"options"`

	// NumPredict caps output tokens for this call only.
	NumPredict *int `json:"-"`

	model     string
	keepAlive string
	stream    bool
}

// ChatResponse is one /api/chat response object. In a stream, every chunk has
// this shape and only the last has Done set.
type ChatResponse struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Message    Message   `json:"message"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason"`

	TotalDuration   int64 `json:"total_duration"`
	LoadDuration    int64 `json:"load_duration"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
}

// ── chat ─────────────────────────────────────────────────────────────────────

// Chat runs a non-streaming completion.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.stream = false
	c.applyDefaults(&req)

	var out ChatResponse
	if err := c.post(ctx, "/api/chat", req, &out); err != nil {
		return nil, err
	}
	if out.DoneReason == "length" && len(out.Message.ToolCalls) == 0 && strings.TrimSpace(out.Message.Content) == "" {
		// The model spent its whole budget without producing anything usable.
		// Reported as tool_parse so the loop's single retry applies.
		return &out, toolParseError(c.ref(), "generation stopped at the token limit with no content and no tool call", nil)
	}
	return &out, nil
}

// ChatStream runs a streaming completion, invoking onChunk for every chunk as
// it arrives, and returns the final aggregated response.
//
// Aggregation happens here rather than at the SSE layer so that a client
// reconnect (CONTRACT §7.2, Last-Event-ID) never changes what gets persisted.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onChunk func(ChatResponse) error) (*ChatResponse, error) {
	req.stream = true
	c.applyDefaults(&req)

	resp, err := c.do(ctx, "/api/chat", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var (
		final   ChatResponse
		content strings.Builder
		think   strings.Builder
		calls   []ToolCall
	)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk ChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			return nil, toolParseError(c.ref(), fmt.Sprintf("stream chunk is not JSON: %s", truncate(string(line), 200)), err)
		}
		content.WriteString(chunk.Message.Content)
		think.WriteString(chunk.Message.Thinking)
		calls = append(calls, chunk.Message.ToolCalls...)

		if onChunk != nil {
			if err := onChunk(chunk); err != nil {
				return nil, err
			}
		}
		if chunk.Done {
			final = chunk
		}
	}
	if err := sc.Err(); err != nil {
		return nil, normalise(ctx, c.ref(), 0, "reading stream", err)
	}
	if !final.Done {
		return nil, normalise(ctx, c.ref(), 0, "stream ended before a done chunk", io.ErrUnexpectedEOF)
	}

	final.Message.Content = content.String()
	final.Message.Thinking = think.String()
	final.Message.ToolCalls = calls
	return &final, nil
}

func (c *Client) applyDefaults(req *ChatRequest) {
	req.model = c.cfg.Model
	req.keepAlive = c.cfg.KeepAlive.String()
	req.Options.NumCtx = c.cfg.NumCtx
	if req.NumPredict != nil {
		req.Options.NumPredict = req.NumPredict
	}
}

// MarshalJSON injects the client-owned fields. Doing it here rather than in a
// separate wire struct keeps num_ctx impossible to omit.
func (r ChatRequest) MarshalJSON() ([]byte, error) {
	type alias ChatRequest
	return json.Marshal(struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		KeepAlive string `json:"keep_alive,omitempty"`
		alias
	}{
		Model:     r.model,
		Stream:    r.stream,
		KeepAlive: r.keepAlive,
		alias:     alias(r),
	})
}

// ── embeddings ───────────────────────────────────────────────────────────────

type embedRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	KeepAlive string   `json:"keep_alive,omitempty"`
	Truncate  *bool    `json:"truncate,omitempty"`
}

// EmbedResponse is the /api/embed response.
type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns one vector per input string, in order.
func (c *Client) Embed(ctx context.Context, inputs ...string) (*EmbedResponse, error) {
	if len(inputs) == 0 {
		return &EmbedResponse{Model: c.cfg.Model}, nil
	}
	// Truncation would silently produce a vector for less text than we stored,
	// which is a retrieval bug that never surfaces as an error. Chunking is
	// contextbuild's job; refusing oversized input here is how we find out it
	// failed to do it.
	no := false
	req := embedRequest{
		Model:     c.cfg.Model,
		Input:     inputs,
		KeepAlive: c.cfg.KeepAlive.String(),
		Truncate:  &no,
	}
	var out EmbedResponse
	if err := c.post(ctx, "/api/embed", req, &out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(inputs) {
		return nil, toolParseError(c.ref(),
			fmt.Sprintf("asked for %d embeddings and got %d", len(inputs), len(out.Embeddings)), nil)
	}
	return &out, nil
}

// ── introspection ────────────────────────────────────────────────────────────

// ShowResponse is the /api/show response, trimmed to what validation needs.
type ShowResponse struct {
	Capabilities []string       `json:"capabilities"`
	ModelInfo    map[string]any `json:"model_info"`
	Details      struct {
		ParentModel       string `json:"parent_model"`
		Family            string `json:"family"`
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
}

// Architecture returns general.architecture, which prefixes the per-model keys
// in ModelInfo (for example glm5.2.context_length).
func (s *ShowResponse) Architecture() string {
	if v, ok := s.ModelInfo["general.architecture"].(string); ok {
		return v
	}
	return ""
}

// MaxContext returns the model's trained context length, as reported by
// /api/show. This is a ceiling, not a promise: it says what the model can do,
// never what the server allocated.
func (s *ShowResponse) MaxContext() (int, bool) {
	arch := s.Architecture()
	if arch != "" {
		if n, ok := numeric(s.ModelInfo[arch+".context_length"]); ok {
			return n, true
		}
	}
	for k, v := range s.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if n, ok := numeric(v); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// EmbeddingLength returns the model's embedding dimension from /api/show.
func (s *ShowResponse) EmbeddingLength() (int, bool) {
	arch := s.Architecture()
	if arch != "" {
		if n, ok := numeric(s.ModelInfo[arch+".embedding_length"]); ok && n > 0 {
			return n, true
		}
	}
	for k, v := range s.ModelInfo {
		if strings.HasSuffix(k, ".embedding_length") {
			if n, ok := numeric(v); ok && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// HasCapability reports whether /api/show lists cap.
func (s *ShowResponse) HasCapability(cap string) bool {
	for _, c := range s.Capabilities {
		if strings.EqualFold(c, cap) {
			return true
		}
	}
	return false
}

// Show fetches model metadata.
func (c *Client) Show(ctx context.Context) (*ShowResponse, error) {
	var out ShowResponse
	if err := c.post(ctx, "/api/show", map[string]string{"model": c.cfg.Model}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PSModel is one loaded model as reported by /api/ps.
type PSModel struct {
	Name string `json:"name"`
	// ContextLength is the window the server actually allocated. This is the
	// only number that proves num_ctx took effect (D4).
	ContextLength int       `json:"context_length"`
	SizeVRAM      int64     `json:"size_vram"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type psResponse struct {
	Models []PSModel `json:"models"`
}

// PS returns the models the server currently has resident.
//
// Cloud models never appear here — verified against Ollama 0.30.11: a completed
// chat against glm-5.2:cloud leaves /api/ps returning an empty list, because
// the weights are not on this machine. That is why validation is tiered (D20).
func (c *Client) PS(ctx context.Context) ([]PSModel, error) {
	var out psResponse
	if err := c.get(ctx, "/api/ps", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// Loaded returns the /api/ps entry for this client's model, if resident.
func (c *Client) Loaded(ctx context.Context) (*PSModel, error) {
	models, err := c.PS(ctx)
	if err != nil {
		return nil, err
	}
	for i := range models {
		if models[i].Name == c.cfg.Model {
			return &models[i], nil
		}
	}
	return nil, nil
}

// ── transport ────────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	resp, err := c.do(ctx, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return c.decode(ctx, resp, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+path, nil)
	if err != nil {
		return normalise(ctx, c.ref(), 0, "building request", err)
	}
	c.authorise(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return normalise(ctx, c.ref(), 0, "", err)
	}
	defer resp.Body.Close()
	return c.decode(ctx, resp, out)
}

func (c *Client) do(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, normalise(ctx, c.ref(), 0, "encoding request", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return nil, normalise(ctx, c.ref(), 0, "building request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorise(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, normalise(ctx, c.ref(), 0, "", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, normalise(ctx, c.ref(), resp.StatusCode, errorMessage(msg), nil)
	}
	return resp, nil
}

func (c *Client) decode(ctx context.Context, resp *http.Response, out any) error {
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return normalise(ctx, c.ref(), resp.StatusCode, errorMessage(msg), nil)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return normalise(ctx, c.ref(), resp.StatusCode, "reading response", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return toolParseError(c.ref(), fmt.Sprintf("response is not the expected JSON: %s", truncate(string(raw), 200)), err)
	}
	return nil
}

func (c *Client) authorise(req *http.Request) {
	if tok := c.cfg.AuthToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// errorMessage pulls Ollama's {"error": "..."} out of a body, falling back to
// the raw bytes so nothing is lost when the shape is unfamiliar.
func errorMessage(body []byte) string {
	var wrapped struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Error != "" {
		return wrapped.Error
	}
	return truncate(strings.TrimSpace(string(body)), 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func numeric(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
