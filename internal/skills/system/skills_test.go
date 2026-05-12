package system

import (
	"context"
	"strings"
	"testing"
	"time"

	"openlight/internal/skills"
)

type stubProvider struct{}

func (stubProvider) CPUUsage(context.Context) (float64, error) {
	return 12.5, nil
}

func (stubProvider) MemoryStats(context.Context) (MemoryStats, error) {
	return MemoryStats{Total: 8 * 1024, Available: 2 * 1024, Used: 6 * 1024}, nil
}

func (stubProvider) DiskStats(context.Context, string) (DiskStats, error) {
	return DiskStats{Path: "/", Total: 100 * 1024, Free: 60 * 1024, Used: 40 * 1024}, nil
}

func (stubProvider) Uptime(context.Context) (time.Duration, error) {
	return 3*time.Hour + 12*time.Minute, nil
}

func (stubProvider) Hostname(context.Context) (string, error) {
	return "pi-zero", nil
}

func (stubProvider) IPAddresses(context.Context) ([]string, error) {
	return []string{"192.168.1.10"}, nil
}

func (stubProvider) Temperature(context.Context) (float64, error) {
	return 44.5, nil
}

func TestStatusSkillIncludesAvailableMetrics(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty status response")
	}
}

func TestStatusSkillIncludesLLMSummaryWhenAvailable(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{LLMProvider: "ollama", LLMModel: "qwen3:12b"}
	result, err := NewStatusSkill(stubProvider{}, info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "LLM: ollama / qwen3:12b") {
		t.Fatalf("expected LLM line, got: %q", result.Text)
	}
}

func TestModelsSkillRendersAllComponents(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{
		LLMProfile:     "ollama",
		LLMProvider:    "ollama",
		LLMModel:       "qwen3:12b",
		LLMEndpoint:    "http://127.0.0.1:11434",
		VisionEnabled:  true,
		VisionProvider: "ollama",
		VisionModel:    "qwen2.5vl:3b",
		OCREnabled:     true,
		OCRProvider:    "tesseract",
		OCRLanguages:   []string{"eng", "rus"},
	}
	result, err := NewModelsSkill(info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{
		"LLM: ollama / qwen3:12b",
		"profile=ollama",
		"http://127.0.0.1:11434",
		"Vision: ollama / qwen2.5vl:3b",
		"OCR: tesseract (eng+rus)",
		"Voice: disabled",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in response, got: %q", want, result.Text)
		}
	}
}

func TestModelsSkillRendersFastAndSmartTiers(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{
		LLMProvider:   "ollama",
		LLMModel:      "gemma3-12b-8k",
		LLMEndpoint:   "http://127.0.0.1:11434",
		FastProvider:  "ollama",
		FastModel:     "gemma3:4b-4k",
		SmartProvider: "ollama",
		SmartModel:    "gemma3-12b-8k",
		SmartEndpoint: "http://127.0.0.1:11434",
		VisionEnabled: true,
		VisionProvider: "ollama",
		VisionModel:    "qwen2.5vl:3b",
		OCREnabled:     true,
		OCRProvider:    "tesseract",
		OCRLanguages:   []string{"eng", "rus"},
		VoiceEnabled:   true,
		VoiceProvider:  "whisper_cli",
		VoiceModel:     "~/models/ggml-small.bin",
	}

	result, err := NewModelsSkill(info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	for _, want := range []string{
		"LLM:",
		"- fast: ollama / gemma3:4b-4k",
		"- smart: ollama / gemma3-12b-8k",
		"- endpoint: http://127.0.0.1:11434",
		"Vision: ollama / qwen2.5vl:3b",
		"OCR: tesseract (eng+rus)",
		"Voice: whisper_cli / ~/models/ggml-small.bin",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in response, got:\n%s", want, result.Text)
		}
	}
}

func TestModelsSkillFallbackRendersLegacyLineWithFastFallback(t *testing.T) {
	t.Parallel()

	// When no dedicated fast profile is configured, the FastFallback flag is
	// set so the legacy single-line LLM output explicitly says fast=fallback.
	info := ModelsInfo{
		LLMProvider:   "ollama",
		LLMModel:      "gemma3-12b-8k",
		LLMEndpoint:   "http://127.0.0.1:11434",
		SmartProvider: "ollama",
		SmartModel:    "gemma3-12b-8k",
		FastFallback:  true,
	}

	result, err := NewModelsSkill(info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "LLM: ollama / gemma3-12b-8k") {
		t.Fatalf("expected legacy single-line LLM output, got:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "fast=fallback") {
		t.Fatalf("expected fast=fallback marker, got:\n%s", result.Text)
	}
}

func TestModelsSkillRendersWarmupSection(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{
		LLMProvider:     "ollama",
		LLMModel:        "gemma3-12b-8k",
		LLMEndpoint:     "http://127.0.0.1:11434",
		FastProvider:    "ollama",
		FastModel:       "qwen2.5:1.5b",
		SmartProvider:   "ollama",
		SmartModel:      "gemma3-12b-8k",
		FastKeepAlive:   "10m",
		SmartKeepAlive:  "-1",
		WarmupEnabled:   true,
		WarmupProfiles:  []string{"smart"},
		WarmupKeepAlive: "-1",
		WarmupPrompt:    "warmup",
		LoadedModelsLookup: func(_ context.Context) []LoadedModelInfo {
			return []LoadedModelInfo{
				{Name: "gemma3-12b-8k:latest", Processor: "100% GPU", Context: 8192, ExpiresAt: "forever", SizeVRAM: 8 << 30},
			}
		},
	}

	result, err := NewModelsSkill(info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	for _, want := range []string{
		"- fast: ollama / qwen2.5:1.5b (keep_alive=10m)",
		"- smart: ollama / gemma3-12b-8k (keep_alive=-1)",
		"Warmup:",
		"- state: enabled",
		"- profiles: smart",
		"- keep_alive: -1",
		"- prompt: warmup",
		"Loaded models:",
		"gemma3-12b-8k:latest",
		"processor=100% GPU",
		"ctx=8192",
		"until=forever",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in response, got:\n%s", want, result.Text)
		}
	}
}

func TestModelsSkillLoadedModelsLookupNilNoSection(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{
		FastProvider:  "ollama",
		FastModel:     "qwen2.5:1.5b",
		SmartProvider: "ollama",
		SmartModel:    "gemma3-12b-8k",
	}
	result, err := NewModelsSkill(info).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if strings.Contains(result.Text, "Loaded models") {
		t.Fatalf("loaded models section should be omitted when lookup is nil, got:\n%s", result.Text)
	}
}

func TestModelsSkillHasLLMStatusAlias(t *testing.T) {
	t.Parallel()

	def := NewModelsSkill(ModelsInfo{}).Definition()
	if def.Name != "models" {
		t.Fatalf("unexpected models skill name: %q", def.Name)
	}
	foundLLMStatus := false
	for _, alias := range def.Aliases {
		if alias == "llm status" || alias == "llm_status" {
			foundLLMStatus = true
		}
	}
	if !foundLLMStatus {
		t.Fatalf("expected llm status alias on models skill, got aliases=%v", def.Aliases)
	}
}

func TestCPUSkillFormatsUsage(t *testing.T) {
	t.Parallel()

	result, err := NewCPUSkill(stubProvider{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "CPU usage: 12.5%" {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}
