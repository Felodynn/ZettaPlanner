package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseYAML is a minimal registry that must load cleanly. Every test case below
// breaks exactly one thing in it, so a failure names one cause.
const baseYAML = `
models:
  planner-cloud:
    endpoint: http://localhost:11434
    tier: cloud
    model: glm-5.2:cloud
    num_ctx: 262144
    assembly_budget: 120000
    caps: [tools, long_context]
    concurrency: 4
  vision-local:
    endpoint: http://localhost:11434
    tier: self_hosted
    model: gemma4:12b
    num_ctx: 32768
    caps: [vision]
  embed-local:
    endpoint: http://localhost:11434
    tier: self_hosted
    model: bge-m3
    caps: [embedding]
    dim: 1024
roles:
  planner: { requires: [tools],     prefer: [planner-cloud] }
  vision:  { requires: [vision],    prefer: [vision-local] }
  embed:   { requires: [embedding], prefer: [embed-local] }
`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadModelsValid(t *testing.T) {
	m, err := LoadModels(write(t, baseYAML))
	if err != nil {
		t.Fatalf("valid registry rejected: %v", err)
	}

	planner := m.Models["planner-cloud"]
	if planner.Name != "planner-cloud" {
		t.Errorf("Name not populated from the registry key: %q", planner.Name)
	}
	if planner.KeepAlive.Duration() == 0 {
		t.Error("keep_alive should default to a non-zero duration; a request that outlives it reloads the model mid-turn")
	}
	if planner.AssemblyBudget != 120000 {
		t.Errorf("assembly_budget = %d, want 120000", planner.AssemblyBudget)
	}

	// A generation model without an explicit assembly_budget targets the whole
	// window, which is the documented default.
	if got := m.Models["vision-local"].AssemblyBudget; got != 32768 {
		t.Errorf("assembly_budget should default to num_ctx, got %d", got)
	}

	candidates, err := m.Resolve(RolePlanner)
	if err != nil || len(candidates) != 1 || candidates[0].Name != "planner-cloud" {
		t.Errorf("Resolve(planner) = %v, %v", candidates, err)
	}
}

// TestLoadModelsRejects covers the failures that must be caught before the
// process serves a request. Each asserts on a substring of the message,
// because a validation error that does not name the fix is not much better
// than no validation at all.
func TestLoadModelsRejects(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(string) string
		wantText string
	}{
		{
			name:     "num_ctx omitted on a generation model",
			mutate:   func(s string) string { return strings.Replace(s, "    num_ctx: 262144\n", "", 1) },
			wantText: "num_ctx is unset",
		},
		{
			name:     "misspelt key",
			mutate:   func(s string) string { return strings.Replace(s, "num_ctx:", "num_context:", 1) },
			wantText: "field num_context not found",
		},
		{
			name:     "tier omitted",
			mutate:   func(s string) string { return strings.Replace(s, "    tier: cloud\n", "", 1) },
			wantText: "tier is unset",
		},
		{
			name:     "unknown tier",
			mutate:   func(s string) string { return strings.Replace(s, "tier: cloud", "tier: hybrid", 1) },
			wantText: `tier is "hybrid"`,
		},
		{
			name:     "embedding model without a dimension",
			mutate:   func(s string) string { return strings.Replace(s, "    dim: 1024\n", "", 1) },
			wantText: "dim is unset",
		},
		{
			name: "assembly_budget above num_ctx",
			mutate: func(s string) string {
				return strings.Replace(s, "assembly_budget: 120000", "assembly_budget: 500000", 1)
			},
			wantText: "exceeds num_ctx",
		},
		{
			name: "role prefers a model that does not exist",
			mutate: func(s string) string {
				return strings.Replace(s, "prefer: [planner-cloud]", "prefer: [planner-typo]", 1)
			},
			wantText: "not defined under models",
		},
		{
			name: "role prefers a model lacking the required capability",
			mutate: func(s string) string {
				return strings.Replace(s, "prefer: [planner-cloud]", "prefer: [embed-local]", 1)
			},
			wantText: `requires the "tools" capability`,
		},
		{
			name: "required role missing",
			mutate: func(s string) string {
				return strings.Replace(s, "  vision:  { requires: [vision],    prefer: [vision-local] }\n", "", 1)
			},
			wantText: "roles.vision is missing",
		},
		{
			name:     "endpoint without a scheme",
			mutate:   func(s string) string { return strings.Replace(s, "http://localhost:11434", "localhost:11434", 1) },
			wantText: "has no scheme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadModels(write(t, tc.mutate(baseYAML)))
			if err == nil {
				t.Fatal("expected the registry to be rejected, it loaded cleanly")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error does not name the fix.\n got: %v\nwant substring: %q", err, tc.wantText)
			}
		})
	}
}

func TestLoadModelsMissingFile(t *testing.T) {
	_, err := LoadModels(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing registry")
	}
	if !strings.Contains(err.Error(), "ZP_MODELS_PATH") {
		t.Errorf("error should name the environment variable that fixes it: %v", err)
	}
}

func TestCapVerifiable(t *testing.T) {
	// Synthetic labels are allowed in caps for role matching but must never be
	// asserted against /api/show, which does not know about them.
	if Cap("long_context").Verifiable() {
		t.Error("long_context is not reported by /api/show and must not be verified against it")
	}
	for _, c := range []Cap{CapTools, CapVision, CapEmbedding} {
		if !c.Verifiable() {
			t.Errorf("%q is reported by /api/show and must be verified", c)
		}
	}
}
