package llm

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type stubProvider struct{}

func (stubProvider) ClassifyRoute(context.Context, string, RouteClassificationRequest) (RouteClassification, error) {
	return RouteClassification{}, nil
}

func (stubProvider) ClassifySkill(context.Context, string, SkillClassificationRequest) (Classification, error) {
	return Classification{}, nil
}

func (stubProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (stubProvider) Chat(context.Context, []ChatMessage) (string, error) {
	return "", nil
}

func TestDefaultFactoryRegistryBuildsBuiltInProviders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := DefaultFactoryRegistry()

	cases := []struct {
		name       string
		cfg        ProviderConfig
		assertType func(t *testing.T, provider Provider)
	}{
		{
			name: "generic",
			cfg: ProviderConfig{
				Endpoint: "http://127.0.0.1:8080",
				Timeout:  time.Second,
			},
			assertType: func(t *testing.T, provider Provider) {
				t.Helper()
				if _, ok := provider.(*HTTPProvider); !ok {
					t.Fatalf("expected *HTTPProvider, got %T", provider)
				}
			},
		},
		{
			name: "ollama",
			cfg: ProviderConfig{
				Endpoint: "http://127.0.0.1:11434",
				Model:    "qwen2.5:0.5b",
				Timeout:  time.Second,
			},
			assertType: func(t *testing.T, provider Provider) {
				t.Helper()
				if _, ok := provider.(*OllamaProvider); !ok {
					t.Fatalf("expected *OllamaProvider, got %T", provider)
				}
			},
		},
		{
			name: "openai",
			cfg: ProviderConfig{
				Endpoint: "https://api.openai.com/v1",
				Model:    "gpt-4o-mini",
				APIKey:   "sk-test",
				Timeout:  time.Second,
			},
			assertType: func(t *testing.T, provider Provider) {
				t.Helper()
				if _, ok := provider.(*OpenAIProvider); !ok {
					t.Fatalf("expected *OpenAIProvider, got %T", provider)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, err := registry.Build(tc.name, tc.cfg, logger)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			tc.assertType(t, provider)
		})
	}
}

func TestBuildProviderUsesDefaultRegistry(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	provider, err := BuildProvider("ollama", ProviderConfig{
		Endpoint: "http://127.0.0.1:11434",
		Model:    "qwen2.5:0.5b",
		Timeout:  time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("BuildProvider returned error: %v", err)
	}
	if _, ok := provider.(*OllamaProvider); !ok {
		t.Fatalf("expected *OllamaProvider, got %T", provider)
	}
}

func TestFactoryRegistrySupportsCustomProviders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewFactoryRegistry()
	registry.MustRegister(NewProviderFactory("custom", func(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
		return stubProvider{}, nil
	}))

	provider, err := registry.Build("custom", ProviderConfig{}, logger)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if _, ok := provider.(stubProvider); !ok {
		t.Fatalf("expected stubProvider, got %T", provider)
	}
}

func TestFactoryRegistryRejectsUnknownProviders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := DefaultFactoryRegistry()

	if _, err := registry.Build("missing", ProviderConfig{}, logger); err == nil {
		t.Fatal("expected unknown provider error")
	}
}

func TestBuiltInProviderFactoriesExposeKnownNames(t *testing.T) {
	t.Parallel()

	registry := NewFactoryRegistry()
	if err := RegisterBuiltInProviderFactories(registry); err != nil {
		t.Fatalf("RegisterBuiltInProviderFactories returned error: %v", err)
	}

	names := registry.Names()
	expected := []string{"generic", "ollama", "openai"}
	for _, name := range expected {
		found := false
		for _, got := range names {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected provider name %q in %#v", name, names)
		}
	}
}
