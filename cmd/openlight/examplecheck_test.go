package main

import (
	"path/filepath"
	"testing"

	"openlight/internal/config"
)

func TestExampleConfigsParse(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "x")
	t.Setenv("ALLOWED_USER_IDS", "1")
	t.Setenv("SQLITE_PATH", "./tmp.db")

	for _, name := range []string{"agent.example.yaml", "agent.macmini.example.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "configs", name)
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("Load %s: %v", path, err)
			}
			if cfg.Telegram.BotToken == "" {
				t.Fatalf("expected bot token to be set from env")
			}
		})
	}
}

func TestMacminiExampleHasFastSmartProfiles(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "x")
	t.Setenv("ALLOWED_USER_IDS", "1")
	t.Setenv("SQLITE_PATH", "./tmp.db")

	cfg, err := config.Load(filepath.Join("..", "..", "configs", "agent.macmini.example.yaml"))
	if err != nil {
		t.Fatalf("Load macmini example: %v", err)
	}
	if !cfg.LLM.HasProfile("fast") || !cfg.LLM.HasProfile("smart") {
		t.Fatalf("expected macmini example to define both fast and smart profiles")
	}
	fast := cfg.LLM.ResolveProfile("fast")
	if fast.Model == "" {
		t.Fatalf("expected macmini example to set a fast model")
	}
	smart := cfg.LLM.ResolveProfile("smart")
	if smart.Model != "gemma3-12b-8k" {
		t.Fatalf("unexpected smart model: %q", smart.Model)
	}
	if fast.Provider != "ollama" || smart.Provider != "ollama" {
		t.Fatalf("fast/smart should inherit ollama provider: fast=%q smart=%q", fast.Provider, smart.Provider)
	}

	// Default warmup policy: SMART only — FAST warms quickly on demand,
	// vision is rarely called and would eat 3GB unnecessarily.
	if !cfg.LLM.Warmup.Includes("smart") {
		t.Fatalf("macmini example should warmup smart, got %v", cfg.LLM.Warmup.Profiles)
	}
	if cfg.LLM.Warmup.Includes("fast") || cfg.LLM.Warmup.Includes("vision") {
		t.Fatalf("macmini example should not warmup fast/vision, got %v", cfg.LLM.Warmup.Profiles)
	}
	if cfg.LLM.Warmup.KeepAliveString() != "-1" {
		t.Fatalf("macmini example should warmup with keep_alive=-1, got %q", cfg.LLM.Warmup.KeepAliveString())
	}
}
