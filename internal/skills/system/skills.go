package system

import (
	"context"
	"fmt"
	"strings"

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
}

func NewStatusSkill(provider Provider, models ModelsInfo) skills.Skill {
	return &statusSkill{provider: provider, models: models}
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

func (s *statusSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	lines := make([]string, 0, 6)

	if hostname, err := s.provider.Hostname(ctx); err == nil {
		lines = append(lines, "Hostname: "+hostname)
	}
	if cpu, err := s.provider.CPUUsage(ctx); err == nil {
		lines = append(lines, "CPU: "+utils.FormatPercent(cpu))
	}
	if memory, err := s.provider.MemoryStats(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("Memory: %s used / %s total", utils.FormatBytes(memory.Used), utils.FormatBytes(memory.Total)))
	}
	if disk, err := s.provider.DiskStats(ctx, "/"); err == nil {
		lines = append(lines, fmt.Sprintf("Disk: %s free / %s total", utils.FormatBytes(disk.Free), utils.FormatBytes(disk.Total)))
	}
	if uptime, err := s.provider.Uptime(ctx); err == nil {
		lines = append(lines, "Uptime: "+utils.FormatDuration(uptime))
	}
	if temperature, err := s.provider.Temperature(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("Temperature: %.1fC", temperature))
	}
	if line := formatLLMSummary(s.models); line != "" {
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return skills.Result{}, fmt.Errorf("%w: no system metrics available", skills.ErrUnavailable)
	}

	return skills.Result{Text: strings.Join(lines, "\n")}, nil
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

func formatLLMSummary(info ModelsInfo) string {
	if info.hasTwoTier() {
		fastProv := strings.TrimSpace(info.FastProvider)
		fastModel := strings.TrimSpace(info.FastModel)
		smartProv := strings.TrimSpace(info.SmartProvider)
		smartModel := strings.TrimSpace(info.SmartModel)
		fast := fastModel
		if fastProv != "" {
			fast = fastProv + " / " + fastModel
		}
		smart := smartModel
		if smartProv != "" {
			smart = smartProv + " / " + smartModel
		}
		return fmt.Sprintf("LLM: fast=%s, smart=%s", fast, smart)
	}

	model := strings.TrimSpace(info.LLMModel)
	if model == "" {
		return ""
	}
	provider := strings.TrimSpace(info.LLMProvider)
	if provider == "" {
		return "LLM: " + model
	}
	return fmt.Sprintf("LLM: %s / %s", provider, model)
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
		if ka := strings.TrimSpace(info.SmartKeepAlive); ka != "" {
			smartLine += " (keep_alive=" + ka + ")"
		}
		lines = append(lines, smartLine)

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
