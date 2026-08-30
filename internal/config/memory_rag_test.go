package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig writes a YAML fragment plus the minimum required keys and
// loads it.
func loadMemoryConfig(t *testing.T, fragment string) Config {
	t.Helper()
	clearConfigEnv(t)

	base := `
telegram:
  bot_token: "test-token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "/tmp/openlight-test/agent.db"
`
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, base+fragment)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func TestMemoryRAGIsDisabledByDefault(t *testing.T) {
	cfg := loadMemoryConfig(t, "")

	// The legacy manual-memory skills keep their previous default; the
	// new subsystem must stay off so an upgrade changes nothing.
	if !cfg.Memory.Enabled {
		t.Fatal("memory.enabled default changed; the /remember skills would disappear")
	}
	if cfg.Memory.RAG.Enabled {
		t.Fatal("memory.rag.enabled must default to false")
	}
}

func TestMemoryRAGDefaultsAreProductionSane(t *testing.T) {
	cfg := loadMemoryConfig(t, `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      url: http://brain:11434
`)
	rag := cfg.Memory.RAG

	if !rag.Enabled {
		t.Fatal("rag not enabled")
	}
	// SQLite lands next to the archive so the whole subsystem is one
	// movable directory on the SSD.
	if rag.SQLite.Path != "/mnt/openlight/memory/memory.db" {
		t.Fatalf("sqlite path = %q", rag.SQLite.Path)
	}
	if rag.Vector.Provider != "qdrant" || rag.Vector.URL != "http://127.0.0.1:6334" {
		t.Fatalf("vector defaults wrong: %+v", rag.Vector)
	}
	if rag.Vector.Collection != "openlight_memory" {
		t.Fatalf("collection = %q", rag.Vector.Collection)
	}
	if rag.Vector.OnDisk == nil || !*rag.Vector.OnDisk {
		t.Fatal("vectors should default to on-disk on a memory-constrained Pi")
	}
	if rag.Embeddings.Model != "bge-m3" {
		t.Fatalf("embedding model = %q, want a multilingual default", rag.Embeddings.Model)
	}
	// Not an edge node, so Ollama is assumed local.
	if rag.Embeddings.Provider != "ollama" || rag.Embeddings.EmbeddingsViaBrain() {
		t.Fatalf("non-edge should embed against a local Ollama: %+v", rag.Embeddings)
	}
	if rag.Chunking.TargetTokens != 350 || rag.Chunking.OverlapTokens != 50 {
		t.Fatalf("chunking defaults wrong: %+v", rag.Chunking)
	}
	if rag.Retrieval.Candidates != 8 || rag.Retrieval.MaxResults != 5 || rag.Retrieval.MaxContextTokens != 500 {
		t.Fatalf("retrieval defaults wrong: %+v", rag.Retrieval)
	}
	if rag.Retrieval.Mode != "heuristic" {
		t.Fatalf("retrieval mode = %q, want heuristic so trivial commands skip the lookup", rag.Retrieval.Mode)
	}
	if rag.Ingestion.Workers != 1 {
		t.Fatalf("workers = %d; a Pi should default to one", rag.Ingestion.Workers)
	}
	if rag.Ingestion.RetryMaxInterval != 10*time.Minute {
		t.Fatalf("retry_max_interval = %v", rag.Ingestion.RetryMaxInterval)
	}
	if !rag.Conversations.AutoMemory || !rag.Conversations.Summarize || !rag.Facts.Enabled {
		t.Fatalf("automatic memory should be on once rag is enabled: %+v", rag)
	}
}

func TestMemoryRAGDefaultsEdgeNodesToBrainEmbeddings(t *testing.T) {
	// Ollama binds to loopback on the brain node, so an edge node cannot
	// reach :11434. Routing through the brain API is the only default
	// that can actually work there.
	cfg := loadMemoryConfig(t, `
node:
  node_role: edge
  brain_url: "http://brain:8787"
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
`)
	rag := cfg.Memory.RAG

	if rag.Embeddings.Provider != "brain" {
		t.Fatalf("edge embeddings provider = %q, want brain", rag.Embeddings.Provider)
	}
	if !rag.Embeddings.EmbeddingsViaBrain() {
		t.Fatal("EmbeddingsViaBrain should be true on an edge node")
	}
}

func TestMemoryRAGHonoursAnExplicitOllamaProviderOnEdge(t *testing.T) {
	// Someone who has deliberately exposed Ollama on the tailnet can
	// still opt into the direct path.
	cfg := loadMemoryConfig(t, `
node:
  node_role: edge
  brain_url: "http://brain:8787"
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      provider: ollama
      url: "http://brain:11434"
`)
	rag := cfg.Memory.RAG

	if rag.Embeddings.EmbeddingsViaBrain() {
		t.Fatal("explicit provider: ollama should not be overridden")
	}
	if rag.Embeddings.URL != "http://brain:11434" {
		t.Fatalf("url = %q", rag.Embeddings.URL)
	}
}

func TestMemoryRAGDefaultsALocalOllamaEndpoint(t *testing.T) {
	cfg := loadMemoryConfig(t, `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
`)
	if cfg.Memory.RAG.Embeddings.URL != "http://127.0.0.1:11434" {
		t.Fatalf("url = %q, want the local Ollama default", cfg.Memory.RAG.Embeddings.URL)
	}
}

func TestMemoryRAGAcceptsAnEmbeddingsEndpointAlias(t *testing.T) {
	cfg := loadMemoryConfig(t, `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      endpoint: http://brain:11434/
`)
	if cfg.Memory.RAG.Embeddings.URL != "http://brain:11434" {
		t.Fatalf("endpoint alias not applied: %q", cfg.Memory.RAG.Embeddings.URL)
	}
}

func TestMemoryRAGValidationRejectsBadCombinations(t *testing.T) {
	cases := []struct {
		name     string
		fragment string
		want     string
	}{
		{
			name: "brain provider without a brain url",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      provider: brain
`,
			want: "node.brain_url",
		},
		{
			name: "unknown embeddings provider",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      provider: openai
`,
			want: "embeddings.provider",
		},
		{
			name: "overlap not smaller than target",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      url: http://brain:11434
    chunking:
      target_tokens: 100
      overlap_tokens: 100
`,
			want: "overlap_tokens",
		},
		{
			name: "max_results above candidates",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      url: http://brain:11434
    retrieval:
      candidates: 4
      max_results: 9
`,
			want: "max_results",
		},
		{
			name: "unknown retrieval mode",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      url: http://brain:11434
    retrieval:
      mode: telepathy
`,
			want: "retrieval.mode",
		},
		{
			name: "unknown vector provider",
			fragment: `
memory:
  rag:
    enabled: true
    storage:
      root: /mnt/openlight/memory
    embeddings:
      url: http://brain:11434
    vector:
      provider: pinecone
`,
			want: "vector.provider",
		},
	}

	base := `
telegram:
  bot_token: "test-token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "/tmp/openlight-test/agent.db"
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			path := filepath.Join(t.TempDir(), "agent.yaml")
			writeConfig(t, path, base+tc.fragment)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestMemoryRAGValidationIsSkippedWhenDisabled(t *testing.T) {
	// A half-written rag block must not break startup while the feature
	// is off — that is what makes the rollout safe.
	cfg := loadMemoryConfig(t, `
memory:
  rag:
    enabled: false
    retrieval:
      mode: telepathy
      candidates: 2
      max_results: 99
`)
	if cfg.Memory.RAG.Enabled {
		t.Fatal("rag should be disabled")
	}
}

func TestMemoryRAGEnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("MEMORY_RAG_ENABLED", "true")
	t.Setenv("MEMORY_RAG_STORAGE_ROOT", "/mnt/ssd/mem")
	t.Setenv("MEMORY_RAG_EMBEDDINGS_URL", "http://brain:11434")
	t.Setenv("MEMORY_RAG_EMBEDDINGS_MODEL", "bge-m3:latest")
	t.Setenv("MEMORY_RAG_RETRIEVAL_MODE", "always")
	t.Setenv("MEMORY_RAG_MAX_CONTEXT_TOKENS", "250")

	base := `
telegram:
  bot_token: "test-token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "/tmp/openlight-test/agent.db"
`
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, base)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rag := cfg.Memory.RAG

	if !rag.Enabled || rag.Storage.Root != "/mnt/ssd/mem" {
		t.Fatalf("env overrides not applied: %+v", rag)
	}
	// The derived SQLite path must follow the overridden root, not the
	// value computed during the first normalize pass.
	if rag.SQLite.Path != "/mnt/ssd/mem/memory.db" {
		t.Fatalf("sqlite path = %q, want it derived from the overridden root", rag.SQLite.Path)
	}
	if rag.Embeddings.Model != "bge-m3:latest" || rag.Retrieval.Mode != "always" {
		t.Fatalf("env overrides not applied: %+v", rag)
	}
	if rag.Retrieval.MaxContextTokens != 250 {
		t.Fatalf("max_context_tokens = %d", rag.Retrieval.MaxContextTokens)
	}
}
