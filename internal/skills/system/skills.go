package system

import (
	"context"
	"fmt"
	"strings"
	"time"

	"openlight/internal/skills"
	"openlight/internal/utils"
)

// ModelsInfo is a static snapshot of the model-related configuration the
// agent was started with. It is rendered by the `/models` skill and a
// summary line in `/status`.
//
// LLMProvider / LLMModel describe the legacy single-model profile (used as
// SMART when no separate FAST profile is configured). When the two-tier
// routing is active, FastProvider/FastModel describe the cheap classifier
// model and SmartProvider/SmartModel describe the reasoning model.
type ModelsInfo struct {
	LLMProfile  string
	LLMProvider string
	LLMModel    string
	LLMEndpoint string

	FastProvider string
	FastModel    string
	FastEndpoint string

	SmartProvider string
	SmartModel    string
	SmartEndpoint string

	// FastFallback is true when no dedicated FAST profile is configured and
	// FAST routing falls back to the SMART model.
	FastFallback bool

	// Per-profile keep_alive values that propagate to non-warmup requests.
	FastKeepAlive  string
	SmartKeepAlive string

	// SmartThink and SmartNumCtx describe the reasoning flag and context
	// window of the smart profile. Shown in /status and /models.
	SmartThink  bool
	SmartNumCtx int

	// Deep profile fields. DeepModel is empty when the profile is not configured.
	DeepModel    string
	DeepProvider string
	DeepThink    bool
	DeepNumCtx   int
	DeepKeepAlive string

	// Warmup snapshot — what the agent will pin into Ollama at startup.
	WarmupEnabled   bool
	WarmupProfiles  []string
	WarmupKeepAlive string
	WarmupPrompt    string

	// LoadedModelsLookup, when set, is invoked at /models render time to
	// fetch the current Ollama /api/ps snapshot. Kept as a callback so the
	// system package stays free of any LLM provider dependency.
	LoadedModelsLookup func(ctx context.Context) []LoadedModelInfo

	VisionEnabled  bool
	VisionProvider string
	VisionModel    string

	OCREnabled  bool
	OCRProvider string
	OCRLanguages []string

	VoiceEnabled  bool
	VoiceProvider string
	VoiceModel    string
}

// LoadedModelInfo is a UI-friendly view of an Ollama /api/ps entry. The
// system skill renders it without depending on the LLM provider package.
type LoadedModelInfo struct {
	Name      string
	Size      int64
	SizeVRAM  int64
	Processor string
	Context   int
	ExpiresAt string
}

func (m ModelsInfo) hasTwoTier() bool {
	smartModel := strings.TrimSpace(m.SmartModel)
	fastModel := strings.TrimSpace(m.FastModel)
	if smartModel == "" || fastModel == "" {
		return false
	}
	if m.FastFallback {
		return false
	}
	return !strings.EqualFold(smartModel, fastModel)
}

type statusSkill struct {
	provider Provider
	models   ModelsInfo
	hooks    Hooks
}

func NewStatusSkill(provider Provider, models ModelsInfo, hooks Hooks) skills.Skill {
	return &statusSkill{provider: provider, models: models, hooks: hooks}
}

func (s *statusSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "status",
		Group:       skills.GroupSystem,
		Description: "Show a compact system overview.",
		Aliases:     []string{"system status", "overall status"},
		Usage:       "/status",
	}
}

// Execute renders the operational dashboard for openLight. Each subsystem
// is queried independently and absent metrics degrade to "unknown" or are
// omitted entirely so a single broken probe (sysctl, Ollama, Telegram)
// never fails the whole command.
func (s *statusSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	var sections [][]string

	if basic := s.renderBasicSection(ctx); len(basic) > 0 {
		sections = append(sections, basic)
	}
	if process := s.renderProcessSection(ctx); len(process) > 0 {
		sections = append(sections, process)
	}
	if loaded := s.renderLoadedSection(ctx); len(loaded) > 0 {
		sections = append(sections, loaded)
	}
	if profiles := renderAIProfiles(s.models); len(profiles) > 0 {
		sections = append(sections, profiles)
	}
	if brain := s.renderBrainSection(ctx); len(brain) > 0 {
		sections = append(sections, brain)
	}
	if latency := s.renderLatencySection(); len(latency) > 0 {
		sections = append(sections, latency)
	}

	if len(sections) == 0 {
		return skills.Result{}, fmt.Errorf("%w: no system metrics available", skills.ErrUnavailable)
	}

	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		parts = append(parts, strings.Join(section, "\n"))
	}
	return skills.Result{Text: strings.Join(parts, "\n\n")}, nil
}

func (s *statusSkill) renderBasicSection(ctx context.Context) []string {
	lines := make([]string, 0, 8)
	if hostname, err := s.provider.Hostname(ctx); err == nil {
		lines = append(lines, "Hostname: "+hostname)
	}
	if uptime, err := s.provider.Uptime(ctx); err == nil {
		lines = append(lines, "Uptime: "+formatUptimeShort(uptime))
	}
	if cpu, err := s.provider.CPUUsage(ctx); err == nil {
		lines = append(lines, "CPU: "+utils.FormatPercent(cpu))
	}
	if memory, err := s.provider.MemoryStats(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("Memory: %s / %s", utils.FormatBytes(memory.Used), utils.FormatBytes(memory.Total)))
	}
	lines = append(lines, "Swap: "+s.renderSwap(ctx))
	lines = append(lines, "Memory pressure: "+s.renderPressure(ctx))
	if disk, err := s.provider.DiskStats(ctx, "/"); err == nil {
		lines = append(lines, fmt.Sprintf("Disk: %s free / %s total", utils.FormatBytes(disk.Free), utils.FormatBytes(disk.Total)))
	}
	if temperature, err := s.provider.Temperature(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("CPU temp: %.0f°C", temperature))
	}
	return lines
}

func (s *statusSkill) renderSwap(ctx context.Context) string {
	swap, err := s.provider.SwapStats(ctx)
	if err != nil {
		return "unknown"
	}
	return utils.FormatBytes(swap.Used)
}

func (s *statusSkill) renderPressure(ctx context.Context) string {
	level, err := s.provider.MemoryPressure(ctx)
	if err != nil || strings.TrimSpace(level) == "" {
		return "unknown"
	}
	return level
}

func (s *statusSkill) renderProcessSection(ctx context.Context) []string {
	var lines []string
	if s.hooks.Agent != nil {
		info := s.hooks.Agent(ctx)
		state := "stopped"
		if info.Running {
			state = "running"
		}
		lines = append(lines, "Agent: "+state)
		if info.PID > 0 {
			lines = append(lines, fmt.Sprintf("PID: %d", info.PID))
		}
	}
	if s.hooks.Telegram != nil {
		state := strings.TrimSpace(s.hooks.Telegram(ctx))
		if state == "" {
			state = TelegramUnknown
		}
		lines = append(lines, "Telegram: "+state)
	}
	return lines
}

func (s *statusSkill) renderLoadedSection(ctx context.Context) []string {
	// The hook contract: nil LoadedModels means "no Ollama integration
	// wired" — skip the section. A non-nil hook with an empty result
	// plus OllamaAvailable=false means the daemon is down.
	if s.hooks.LoadedModels == nil {
		return nil
	}
	loaded := s.hooks.LoadedModels(ctx)
	if len(loaded) == 0 {
		if s.hooks.OllamaAvailable != nil && !s.hooks.OllamaAvailable(ctx) {
			return []string{"Ollama loaded: unavailable"}
		}
		return []string{"Ollama loaded: none"}
	}
	lines := []string{"Ollama loaded:"}
	for _, m := range loaded {
		lines = append(lines, "- "+formatLoadedModelStatus(m))
	}
	return lines
}

func (s *statusSkill) renderBrainSection(ctx context.Context) []string {
	if s.hooks.BrainStatus == nil {
		return nil
	}
	info := s.hooks.BrainStatus(ctx)
	state := "OFFLINE"
	if info.Online {
		state = "ONLINE"
	}
	header := "Brain: " + state
	if info.NodeID != "" {
		header += " (" + info.NodeID + ")"
	}
	lines := []string{header}
	if info.Online {
		lines = append(lines, fmt.Sprintf("- ping: %.0f ms", info.PingMs))
		if info.CPUPct > 0 {
			lines = append(lines, "- CPU: "+utils.FormatPercent(info.CPUPct))
		}
		if info.MemTotalGB > 0 {
			lines = append(lines, fmt.Sprintf("- Memory: %.1f GiB / %.1f GiB", info.MemUsedGB, info.MemTotalGB))
		}
		if info.UptimeS > 0 {
			lines = append(lines, "- uptime: "+formatUptimeShort(time.Duration(info.UptimeS)*time.Second))
		}
		if info.Model != "" {
			line := "- smart: " + info.Model
			if info.SmartLatencyMs > 0 {
				line += fmt.Sprintf(" (%.0f ms)", info.SmartLatencyMs)
			}
			lines = append(lines, line)
		}
		if info.FastModel != "" {
			line := "- fast: " + info.FastModel
			if info.FastLatencyMs > 0 {
				line += fmt.Sprintf(" (%.0f ms)", info.FastLatencyMs)
			}
			lines = append(lines, line)
		}
	} else if info.Error != "" {
		lines = append(lines, "- error: "+info.Error)
	}
	return lines
}

func (s *statusSkill) renderLatencySection() []string {
	if s.hooks.Latency == nil {
		return nil
	}
	snapshot := s.hooks.Latency()
	if len(snapshot) == 0 {
		return []string{"Last LLM latency: unknown"}
	}
	order := []string{"fast", "smart"}
	seen := make(map[string]struct{}, len(order))
	lines := []string{"Last LLM latency:"}
	for _, key := range order {
		if d, ok := snapshot[key]; ok {
			lines = append(lines, fmt.Sprintf("- %s: %d ms", key, d.Milliseconds()))
			seen[key] = struct{}{}
		}
	}
	for key, d := range snapshot {
		if _, ok := seen[key]; ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %d ms", key, d.Milliseconds()))
	}
	return lines
}

// formatUptimeShort drops the seconds suffix from FormatDuration so the
// status block stays compact in Telegram. Output: "6d 2h 16m" or "5m" for
// short uptimes.
func formatUptimeShort(d time.Duration) string {
	if d < time.Minute {
		return utils.FormatDuration(d)
	}
	return utils.FormatDuration(d.Truncate(time.Minute))
}

// formatLoadedModelStatus renders one entry of the "Ollama loaded:" list.
// Example: "gemma3-12b-8k · 100% GPU · ctx 8192 · forever".
func formatLoadedModelStatus(m LoadedModelInfo) string {
	parts := []string{shortModelName(m.Name)}
	if p := strings.TrimSpace(m.Processor); p != "" {
		parts = append(parts, p)
	}
	if m.Context > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", m.Context))
	}
	if expires := strings.TrimSpace(m.ExpiresAt); expires != "" {
		parts = append(parts, expires)
	}
	return strings.Join(parts, " · ")
}

// shortModelName trims Ollama's ":latest" suffix that adds noise without
// disambiguating anything for users who only have one tag pulled.
func shortModelName(name string) string {
	name = strings.TrimSpace(name)
	return strings.TrimSuffix(name, ":latest")
}

func renderAIProfiles(info ModelsInfo) []string {
	type profile struct {
		label, provider, model string
		enabled                bool
	}
	entries := []profile{
		{"fast", info.FastProvider, info.FastModel, strings.TrimSpace(info.FastModel) != "" && !info.FastFallback},
		{"smart", info.SmartProvider, info.SmartModel, strings.TrimSpace(info.SmartModel) != ""},
		{"deep", info.DeepProvider, info.DeepModel, strings.TrimSpace(info.DeepModel) != ""},
		{"vision", info.VisionProvider, info.VisionModel, info.VisionEnabled},
	}
	// Fast can fall back to smart; surface that explicitly rather than
	// silently dropping the line.
	if info.FastFallback && strings.TrimSpace(info.FastModel) == "" {
		entries[0].enabled = true
		entries[0].provider = info.SmartProvider
		entries[0].model = info.SmartModel + " (fallback)"
	}

	var rendered []string
	for _, e := range entries {
		if !e.enabled {
			continue
		}
		line := "- " + e.label + ": " + formatProfileLine(e.provider, e.model)
		// Append think/ctx meta for smart and deep profiles.
		switch e.label {
		case "smart":
			line += formatModelMeta(info.SmartThink, info.SmartNumCtx, "")
		case "deep":
			line += formatModelMeta(info.DeepThink, info.DeepNumCtx, "")
		}
		rendered = append(rendered, line)
	}
	if info.OCREnabled {
		ocr := strings.TrimSpace(info.OCRProvider)
		if ocr == "" {
			ocr = "enabled"
		}
		if len(info.OCRLanguages) > 0 {
			ocr += " / " + strings.Join(info.OCRLanguages, "+")
		}
		rendered = append(rendered, "- OCR: "+ocr)
	}
	if info.VoiceEnabled {
		rendered = append(rendered, "- voice: "+formatProfileLine(info.VoiceProvider, info.VoiceModel))
	}

	// Legacy single-model configs that don't populate the tier fields:
	// fall back to LLMProvider/LLMModel so something still shows.
	if len(rendered) == 0 {
		if model := strings.TrimSpace(info.LLMModel); model != "" {
			rendered = append(rendered, "- llm: "+formatProfileLine(info.LLMProvider, info.LLMModel))
		}
	}

	if len(rendered) == 0 {
		return nil
	}
	return append([]string{"AI profiles:"}, rendered...)
}

func formatProfileLine(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	switch {
	case provider != "" && model != "":
		return provider + " / " + model
	case model != "":
		return model
	case provider != "":
		return provider
	default:
		return "disabled"
	}
}

type modelsSkill struct {
	info ModelsInfo
}

func NewModelsSkill(info ModelsInfo) skills.Skill {
	return &modelsSkill{info: info}
}

func (s *modelsSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "models",
		Group:       skills.GroupSystem,
		Description: "Show which LLM / vision / OCR / voice models the agent is configured to use.",
		Aliases:     []string{"model", "which model", "what model", "llm model", "llm status", "llm_status"},
		Usage:       "/models",
	}
}

func (s *modelsSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	lines := make([]string, 0, 12)

	for _, line := range formatLLMLines(s.info) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	for _, line := range formatWarmupLines(s.info) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	for _, line := range formatLoadedModelLines(ctx, s.info) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, formatVisionLine(s.info))
	lines = append(lines, formatOCRLine(s.info))
	lines = append(lines, formatVoiceLine(s.info))

	if len(lines) == 0 {
		return skills.Result{}, fmt.Errorf("%w: no model configuration available", skills.ErrUnavailable)
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

func formatWarmupLines(info ModelsInfo) []string {
	if !info.WarmupEnabled && len(info.WarmupProfiles) == 0 {
		return nil
	}
	lines := []string{"Warmup:"}
	state := "disabled"
	if info.WarmupEnabled {
		state = "enabled"
	}
	lines = append(lines, "- state: "+state)
	if len(info.WarmupProfiles) > 0 {
		lines = append(lines, "- profiles: "+strings.Join(info.WarmupProfiles, ", "))
	}
	if v := strings.TrimSpace(info.WarmupKeepAlive); v != "" {
		lines = append(lines, "- keep_alive: "+v)
	}
	if v := strings.TrimSpace(info.WarmupPrompt); v != "" {
		lines = append(lines, "- prompt: "+v)
	}
	return lines
}

func formatLoadedModelLines(ctx context.Context, info ModelsInfo) []string {
	if info.LoadedModelsLookup == nil {
		return nil
	}
	loaded := info.LoadedModelsLookup(ctx)
	if len(loaded) == 0 {
		return []string{"Loaded models: (none)"}
	}
	lines := []string{"Loaded models:"}
	for _, m := range loaded {
		parts := []string{"- " + m.Name}
		if m.Processor != "" {
			parts = append(parts, "processor="+m.Processor)
		}
		if m.Context > 0 {
			parts = append(parts, fmt.Sprintf("ctx=%d", m.Context))
		}
		if m.SizeVRAM > 0 {
			parts = append(parts, fmt.Sprintf("vram=%s", formatBytesShort(m.SizeVRAM)))
		}
		if m.ExpiresAt != "" {
			parts = append(parts, "until="+m.ExpiresAt)
		}
		lines = append(lines, strings.Join(parts, " · "))
	}
	return lines
}

func formatBytesShort(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1fGB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.0fMB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.0fKB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// formatLLMLines renders the LLM section of the /models output. It returns
// one line for the legacy single-model case and several lines (LLM header
// plus fast/smart/endpoint sub-lines) when two-tier routing is active.
func formatLLMLines(info ModelsInfo) []string {
	if info.hasTwoTier() {
		lines := []string{"LLM:"}

		fastProv := strings.TrimSpace(info.FastProvider)
		fastModel := strings.TrimSpace(info.FastModel)
		fastLine := "- fast: "
		if fastProv != "" {
			fastLine += fastProv + " / " + fastModel
		} else {
			fastLine += fastModel
		}
		if ka := strings.TrimSpace(info.FastKeepAlive); ka != "" {
			fastLine += " (keep_alive=" + ka + ")"
		}
		lines = append(lines, fastLine)

		smartProv := strings.TrimSpace(info.SmartProvider)
		smartModel := strings.TrimSpace(info.SmartModel)
		smartLine := "- smart: "
		if smartProv != "" {
			smartLine += smartProv + " / " + smartModel
		} else {
			smartLine += smartModel
		}
		smartLine += formatModelMeta(info.SmartThink, info.SmartNumCtx, info.SmartKeepAlive)
		lines = append(lines, smartLine)

		if deepModel := strings.TrimSpace(info.DeepModel); deepModel != "" {
			deepProv := strings.TrimSpace(info.DeepProvider)
			deepLine := "- deep: "
			if deepProv != "" {
				deepLine += deepProv + " / " + deepModel
			} else {
				deepLine += deepModel
			}
			deepLine += formatModelMeta(info.DeepThink, info.DeepNumCtx, info.DeepKeepAlive)
			lines = append(lines, deepLine)
		}

		endpoint := strings.TrimSpace(info.SmartEndpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(info.FastEndpoint)
		}
		if endpoint == "" {
			endpoint = strings.TrimSpace(info.LLMEndpoint)
		}
		if endpoint != "" {
			lines = append(lines, "- endpoint: "+endpoint)
		}
		if profile := strings.TrimSpace(info.LLMProfile); profile != "" {
			lines = append(lines, "- profile: "+profile)
		}
		return lines
	}

	line := formatLegacyLLMLine(info)
	if line == "" {
		return nil
	}
	return []string{line}
}

// formatModelMeta returns the parenthetical suffix for a model line:
// think, ctx, and keep_alive. Empty fields are omitted.
// Example: " (think=true · ctx=8192 · keep_alive=10m)"
func formatModelMeta(think bool, numCtx int, keepAlive string) string {
	var parts []string
	if think {
		parts = append(parts, "think=true")
	} else {
		parts = append(parts, "think=false")
	}
	if numCtx > 0 {
		parts = append(parts, fmt.Sprintf("ctx=%d", numCtx))
	}
	if ka := strings.TrimSpace(keepAlive); ka != "" {
		parts = append(parts, "keep_alive="+ka)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " · ") + ")"
}

func formatLegacyLLMLine(info ModelsInfo) string {
	model := strings.TrimSpace(info.LLMModel)
	if model == "" {
		return ""
	}
	parts := []string{}
	provider := strings.TrimSpace(info.LLMProvider)
	if provider != "" {
		parts = append(parts, provider+" / "+model)
	} else {
		parts = append(parts, model)
	}
	if profile := strings.TrimSpace(info.LLMProfile); profile != "" {
		parts = append(parts, "profile="+profile)
	}
	if endpoint := strings.TrimSpace(info.LLMEndpoint); endpoint != "" {
		parts = append(parts, endpoint)
	}
	if info.FastFallback {
		parts = append(parts, "fast=fallback")
	}
	return "LLM: " + strings.Join(parts, " · ")
}

func formatVisionLine(info ModelsInfo) string {
	if !info.VisionEnabled {
		return "Vision: disabled"
	}
	provider := strings.TrimSpace(info.VisionProvider)
	model := strings.TrimSpace(info.VisionModel)
	switch {
	case provider != "" && model != "":
		return "Vision: " + provider + " / " + model
	case model != "":
		return "Vision: " + model
	case provider != "":
		return "Vision: " + provider
	default:
		return "Vision: enabled"
	}
}

func formatOCRLine(info ModelsInfo) string {
	if !info.OCREnabled {
		return "OCR: disabled"
	}
	provider := strings.TrimSpace(info.OCRProvider)
	out := "OCR: "
	if provider != "" {
		out += provider
	} else {
		out += "enabled"
	}
	if len(info.OCRLanguages) > 0 {
		out += " (" + strings.Join(info.OCRLanguages, "+") + ")"
	}
	return out
}

func formatVoiceLine(info ModelsInfo) string {
	if !info.VoiceEnabled {
		return "Voice: disabled"
	}
	provider := strings.TrimSpace(info.VoiceProvider)
	model := strings.TrimSpace(info.VoiceModel)
	switch {
	case provider != "" && model != "":
		return "Voice: " + provider + " / " + model
	case model != "":
		return "Voice: " + model
	case provider != "":
		return "Voice: " + provider
	default:
		return "Voice: enabled"
	}
}

type cpuSkill struct {
	provider Provider
}

func NewCPUSkill(provider Provider) skills.Skill {
	return &cpuSkill{provider: provider}
}

func (s *cpuSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "cpu",
		Group:       skills.GroupSystem,
		Description: "Show CPU usage.",
		Aliases:     []string{"processor usage", "show cpu"},
		Usage:       "/cpu",
	}
}

func (s *cpuSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	usage, err := s.provider.CPUUsage(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "CPU usage: " + utils.FormatPercent(usage)}, nil
}

type memorySkill struct {
	provider Provider
}

func NewMemorySkill(provider Provider) skills.Skill {
	return &memorySkill{provider: provider}
}

func (s *memorySkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory",
		Group:       skills.GroupSystem,
		Description: "Show RAM usage.",
		Aliases:     []string{"ram", "show memory usage"},
		Usage:       "/memory",
	}
}

func (s *memorySkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	stats, err := s.provider.MemoryStats(ctx)
	if err != nil {
		return skills.Result{}, err
	}

	text := fmt.Sprintf(
		"Memory usage: %s used / %s total (%s free)",
		utils.FormatBytes(stats.Used),
		utils.FormatBytes(stats.Total),
		utils.FormatBytes(stats.Available),
	)
	return skills.Result{Text: text}, nil
}

type diskSkill struct {
	provider Provider
}

func NewDiskSkill(provider Provider) skills.Skill {
	return &diskSkill{provider: provider}
}

func (s *diskSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "disk",
		Group:       skills.GroupSystem,
		Description: "Show disk usage for the root filesystem.",
		Aliases:     []string{"storage", "disk space"},
		Usage:       "/disk",
	}
}

func (s *diskSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	stats, err := s.provider.DiskStats(ctx, "/")
	if err != nil {
		return skills.Result{}, err
	}

	text := fmt.Sprintf(
		"Disk usage: %s used / %s total (%s free)",
		utils.FormatBytes(stats.Used),
		utils.FormatBytes(stats.Total),
		utils.FormatBytes(stats.Free),
	)
	return skills.Result{Text: text}, nil
}

type uptimeSkill struct {
	provider Provider
}

func NewUptimeSkill(provider Provider) skills.Skill {
	return &uptimeSkill{provider: provider}
}

func (s *uptimeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "uptime",
		Group:       skills.GroupSystem,
		Description: "Show system uptime.",
		Aliases:     []string{"running for"},
		Usage:       "/uptime",
	}
}

func (s *uptimeSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	uptime, err := s.provider.Uptime(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "Uptime: " + utils.FormatDuration(uptime)}, nil
}

type hostnameSkill struct {
	provider Provider
}

func NewHostnameSkill(provider Provider) skills.Skill {
	return &hostnameSkill{provider: provider}
}

func (s *hostnameSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "hostname",
		Group:       skills.GroupSystem,
		Description: "Show the system hostname.",
		Aliases:     []string{"host"},
		Usage:       "/hostname",
	}
}

func (s *hostnameSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	hostname, err := s.provider.Hostname(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "Hostname: " + hostname}, nil
}

type ipSkill struct {
	provider Provider
}

func NewIPSkill(provider Provider) skills.Skill {
	return &ipSkill{provider: provider}
}

func (s *ipSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "ip",
		Group:       skills.GroupSystem,
		Description: "Show local IPv4 addresses.",
		Aliases:     []string{"ip address", "network ip"},
		Usage:       "/ip",
	}
}

func (s *ipSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	addresses, err := s.provider.IPAddresses(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "IP addresses: " + strings.Join(addresses, ", ")}, nil
}

type temperatureSkill struct {
	provider Provider
}

func NewTemperatureSkill(provider Provider) skills.Skill {
	return &temperatureSkill{provider: provider}
}

func (s *temperatureSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "temperature",
		Group:       skills.GroupSystem,
		Description: "Show system temperature when available.",
		Aliases:     []string{"temp", "cpu temp"},
		Usage:       "/temperature",
	}
}

func (s *temperatureSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	temperature, err := s.provider.Temperature(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: fmt.Sprintf("Temperature: %.1fC", temperature)}, nil
}
