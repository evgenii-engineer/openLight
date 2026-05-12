package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"openlight/internal/skills"
)

type stubProvider struct {
	swapErr     error
	pressureErr error
	swap        SwapStats
	pressure    string
}

func (stubProvider) CPUUsage(context.Context) (float64, error) {
	return 12.5, nil
}

func (stubProvider) MemoryStats(context.Context) (MemoryStats, error) {
	return MemoryStats{Total: 8 * 1024, Available: 2 * 1024, Used: 6 * 1024}, nil
}

func (p stubProvider) SwapStats(context.Context) (SwapStats, error) {
	if p.swapErr != nil {
		return SwapStats{}, p.swapErr
	}
	return p.swap, nil
}

func (p stubProvider) MemoryPressure(context.Context) (string, error) {
	if p.pressureErr != nil {
		return "", p.pressureErr
	}
	if p.pressure == "" {
		return PressureGreen, nil
	}
	return p.pressure, nil
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

	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty status response")
	}
	for _, want := range []string{
		"Hostname: pi-zero",
		"Uptime: 3h 12m",
		"CPU: 12.5%",
		"Memory: ",
		"Swap: 0 B",
		"Memory pressure: green",
		"Disk:",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in status output, got:\n%s", want, result.Text)
		}
	}
}

func TestStatusSkillIncludesSwapWhenAvailable(t *testing.T) {
	t.Parallel()

	provider := stubProvider{swap: SwapStats{Total: 4 * 1024 * 1024 * 1024, Used: 512 * 1024 * 1024, Free: 3*1024*1024*1024 + 512*1024*1024}}
	result, err := NewStatusSkill(provider, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Swap: 512.0 MiB") {
		t.Fatalf("expected swap usage in output, got:\n%s", result.Text)
	}
}

func TestStatusSkillSwapUnknownGracefully(t *testing.T) {
	t.Parallel()

	provider := stubProvider{swapErr: errors.New("sysctl: not found")}
	result, err := NewStatusSkill(provider, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Swap: unknown") {
		t.Fatalf("expected Swap: unknown, got:\n%s", result.Text)
	}
}

func TestStatusSkillMemoryPressureUnknownGracefully(t *testing.T) {
	t.Parallel()

	provider := stubProvider{pressureErr: errors.New("not supported")}
	result, err := NewStatusSkill(provider, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Memory pressure: unknown") {
		t.Fatalf("expected Memory pressure: unknown, got:\n%s", result.Text)
	}
}

func TestStatusSkillRendersConfiguredAIProfiles(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{
		FastProvider:   "ollama",
		FastModel:      "qwen2.5:1.5b",
		SmartProvider:  "ollama",
		SmartModel:     "gemma3-12b-8k",
		VisionEnabled:  true,
		VisionProvider: "ollama",
		VisionModel:    "qwen2.5vl:3b",
		OCREnabled:     true,
		OCRProvider:    "tesseract",
		OCRLanguages:   []string{"eng", "rus"},
		VoiceEnabled:   true,
		VoiceProvider:  "whisper_cli",
		VoiceModel:     "~/models/ggml-small.bin",
	}
	result, err := NewStatusSkill(stubProvider{}, info, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{
		"AI profiles:",
		"- fast: ollama / qwen2.5:1.5b",
		"- smart: ollama / gemma3-12b-8k",
		"- vision: ollama / qwen2.5vl:3b",
		"- OCR: tesseract / eng+rus",
		"- voice: whisper_cli / ~/models/ggml-small.bin",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in status output, got:\n%s", want, result.Text)
		}
	}
}

// Backward compatibility: legacy single-model configs without a separate
// fast profile should still surface their LLM via the AI profiles block.
func TestStatusSkillIncludesLegacyLLMWhenOnlyLLMModelSet(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{LLMProvider: "ollama", LLMModel: "qwen3:12b"}
	result, err := NewStatusSkill(stubProvider{}, info, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "ollama / qwen3:12b") {
		t.Fatalf("expected legacy LLM in output, got:\n%s", result.Text)
	}
}

func TestStatusSkillRendersOllamaLoadedModels(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		LoadedModels: func(context.Context) []LoadedModelInfo {
			return []LoadedModelInfo{
				{Name: "gemma3-12b-8k:latest", Processor: "100% GPU", Context: 8192, ExpiresAt: "forever"},
			}
		},
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{
		"Ollama loaded:",
		"- gemma3-12b-8k · 100% GPU · ctx 8192 · forever",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in status output, got:\n%s", want, result.Text)
		}
	}
}

func TestStatusSkillHandlesOllamaUnavailable(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		LoadedModels:    func(context.Context) []LoadedModelInfo { return nil },
		OllamaAvailable: func(context.Context) bool { return false },
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Ollama loaded: unavailable") {
		t.Fatalf("expected Ollama unavailable, got:\n%s", result.Text)
	}
}

func TestStatusSkillHandlesOllamaNoneLoaded(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		LoadedModels: func(context.Context) []LoadedModelInfo { return nil },
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Ollama loaded: none") {
		t.Fatalf("expected Ollama loaded: none, got:\n%s", result.Text)
	}
}

func TestStatusSkillRendersTelegramConnected(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		Telegram: func(context.Context) string { return TelegramConnected },
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Telegram: connected") {
		t.Fatalf("expected Telegram: connected, got:\n%s", result.Text)
	}
}

func TestStatusSkillSurvivesMissingOptionalHooks(t *testing.T) {
	t.Parallel()

	// All hooks left nil simulate the no-LLM no-Telegram boot path. The
	// command must still complete without panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("status skill panicked with nil hooks: %v", r)
		}
	}()
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if strings.Contains(result.Text, "Telegram:") {
		t.Fatalf("expected no Telegram section without hook, got:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "Ollama loaded:") {
		t.Fatalf("expected no Ollama section without hook, got:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "Last LLM latency") {
		t.Fatalf("expected no Latency section without hook, got:\n%s", result.Text)
	}
}

func TestStatusSkillRendersLatency(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		Latency: func() map[string]time.Duration {
			return map[string]time.Duration{
				"fast":  420 * time.Millisecond,
				"smart": 3182 * time.Millisecond,
			}
		},
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{
		"Last LLM latency:",
		"- fast: 420 ms",
		"- smart: 3182 ms",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in status output, got:\n%s", want, result.Text)
		}
	}
}

func TestStatusSkillLatencyUnknownWhenStoreEmpty(t *testing.T) {
	t.Parallel()

	hooks := Hooks{Latency: func() map[string]time.Duration { return nil }}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Last LLM latency: unknown") {
		t.Fatalf("expected unknown latency, got:\n%s", result.Text)
	}
}

func TestStatusSkillRendersAgentSelfStatus(t *testing.T) {
	t.Parallel()

	hooks := Hooks{
		Agent: func(context.Context) AgentInfo {
			return AgentInfo{Running: true, PID: 16350}
		},
	}
	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{
		"Agent: running",
		"PID: 16350",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in status output, got:\n%s", want, result.Text)
		}
	}
}

func TestLatencyStoreRecordAndSnapshot(t *testing.T) {
	t.Parallel()

	store := NewLatencyStore()
	if got := store.Snapshot(); got != nil {
		t.Fatalf("expected nil snapshot on empty store, got %v", got)
	}
	store.Record("fast", 250*time.Millisecond)
	store.Record("smart", 1500*time.Millisecond)
	store.Record("fast", 300*time.Millisecond) // overwrites previous

	snap := store.Snapshot()
	if got := snap["fast"]; got != 300*time.Millisecond {
		t.Fatalf("expected fast=300ms, got %v", got)
	}
	if got := snap["smart"]; got != 1500*time.Millisecond {
		t.Fatalf("expected smart=1500ms, got %v", got)
	}

	// Mutating the snapshot must not affect the store.
	snap["fast"] = 0
	if got := store.Snapshot()["fast"]; got != 300*time.Millisecond {
		t.Fatalf("snapshot is not a defensive copy: got %v", got)
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
		LLMProvider:    "ollama",
		LLMModel:       "gemma3-12b-8k",
		LLMEndpoint:    "http://127.0.0.1:11434",
		FastProvider:   "ollama",
		FastModel:      "gemma3:4b-4k",
		SmartProvider:  "ollama",
		SmartModel:     "gemma3-12b-8k",
		SmartEndpoint:  "http://127.0.0.1:11434",
		VisionEnabled:  true,
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

// Smoke check that the section separators are blank lines so Telegram
// renders distinct visual groups.
func TestStatusSkillSectionsSeparatedByBlankLine(t *testing.T) {
	t.Parallel()

	info := ModelsInfo{FastProvider: "ollama", FastModel: "qwen", SmartProvider: "ollama", SmartModel: "gemma"}
	hooks := Hooks{
		Agent: func(context.Context) AgentInfo { return AgentInfo{Running: true, PID: 1} },
	}
	result, err := NewStatusSkill(stubProvider{}, info, hooks).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "\n\n") {
		t.Fatalf("expected blank-line separators between sections, got:\n%s", result.Text)
	}
}

// ensure stubProvider satisfies the (extended) Provider interface
var _ Provider = stubProvider{}

// quick sanity that the FormatBytes "0 B" edge keeps working — relied on
// by the swap rendering for systems with no swap configured.
func TestStatusSkillSwapZero(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubProvider{}, ModelsInfo{}, Hooks{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Text, "Swap: 0 B") {
		t.Fatalf("expected zero swap to render as 0 B, got:\n%s", result.Text)
	}
}

func TestFormatLoadedModelStatusOmitsEmptyFields(t *testing.T) {
	t.Parallel()

	got := formatLoadedModelStatus(LoadedModelInfo{Name: "model:latest"})
	if got != "model" {
		t.Fatalf("expected stripped name only, got %q", got)
	}

	got = formatLoadedModelStatus(LoadedModelInfo{Name: "x", Context: 512})
	want := "x · ctx 512"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Just to keep fmt import used in the test file when assertions tighten.
var _ = fmt.Sprintf
