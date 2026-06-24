package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	basellm "openlight/internal/llm"
	systemskills "openlight/internal/skills/system"
)

// TelegramHealthHolder is a runtime-scoped indirection that lets the
// agent.go entrypoint plug a live Telegram health probe into the /status
// skill after the bot has been constructed. The status skill captures the
// holder via closure during registry construction; the probe is bound
// later, once the bot exists.
type TelegramHealthHolder struct {
	mu    sync.RWMutex
	probe func(ctx context.Context) string
}

func NewTelegramHealthHolder() *TelegramHealthHolder {
	return &TelegramHealthHolder{}
}

func (h *TelegramHealthHolder) Bind(probe func(ctx context.Context) string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.probe = probe
	h.mu.Unlock()
}

func (h *TelegramHealthHolder) State(ctx context.Context) string {
	if h == nil {
		return systemskills.TelegramUnknown
	}
	h.mu.RLock()
	probe := h.probe
	h.mu.RUnlock()
	if probe == nil {
		return systemskills.TelegramUnknown
	}
	state := strings.TrimSpace(probe(ctx))
	if state == "" {
		return systemskills.TelegramUnknown
	}
	return state
}

// latencyProvider wraps a basellm.Provider and records call latency into
// a shared LatencyStore under a fixed profile label ("fast" / "smart").
// It forwards all four Provider methods plus PsLister.ListLoadedModels so
// the runtime can still detect Ollama's /api/ps support via type
// assertion.
type latencyProvider struct {
	inner   basellm.Provider
	store   *systemskills.LatencyStore
	profile string
}

func wrapWithLatency(inner basellm.Provider, profile string, store *systemskills.LatencyStore) basellm.Provider {
	if inner == nil || store == nil || strings.TrimSpace(profile) == "" {
		return inner
	}
	return &latencyProvider{inner: inner, store: store, profile: profile}
}

func (p *latencyProvider) record(start time.Time, err error) {
	if err != nil {
		return
	}
	p.store.Record(p.profile, time.Since(start))
}

func (p *latencyProvider) ClassifyRoute(ctx context.Context, text string, request basellm.RouteClassificationRequest) (basellm.RouteClassification, error) {
	start := time.Now()
	out, err := p.inner.ClassifyRoute(ctx, text, request)
	p.record(start, err)
	return out, err
}

func (p *latencyProvider) ClassifySkill(ctx context.Context, text string, request basellm.SkillClassificationRequest) (basellm.Classification, error) {
	start := time.Now()
	out, err := p.inner.ClassifySkill(ctx, text, request)
	p.record(start, err)
	return out, err
}

func (p *latencyProvider) Summarize(ctx context.Context, text string) (string, error) {
	start := time.Now()
	out, err := p.inner.Summarize(ctx, text)
	p.record(start, err)
	return out, err
}

func (p *latencyProvider) Chat(ctx context.Context, messages []basellm.ChatMessage) (string, error) {
	start := time.Now()
	out, err := p.inner.Chat(ctx, messages)
	p.record(start, err)
	return out, err
}

// ListLoadedModels forwards to the inner provider so the runtime's
// PsLister type-assertion still detects Ollama-backed providers after
// they are wrapped for latency tracking. Returns ErrUnsupported when the
// inner provider does not expose /api/ps.
func (p *latencyProvider) ListLoadedModels(ctx context.Context) ([]basellm.LoadedModel, error) {
	if lister, ok := p.inner.(basellm.PsLister); ok {
		return lister.ListLoadedModels(ctx)
	}
	return nil, basellm.ErrPsListerUnsupported
}

// buildSystemHooks assembles the status-skill Hooks struct: Ollama
// loaded-models lookup (with availability tracking), Telegram health,
// latency snapshot, and agent self-status (PID + running flag).
// brainURL, when non-empty, activates the BrainStatus hook for edge nodes.
func buildSystemHooks(
	smartProvider basellm.Provider,
	latency *systemskills.LatencyStore,
	telegramHolder *TelegramHealthHolder,
	pid int,
	brainURL string,
) systemskills.Hooks {
	hooks := systemskills.Hooks{
		Agent: func(_ context.Context) systemskills.AgentInfo {
			return systemskills.AgentInfo{Running: true, PID: pid}
		},
	}
	if latency != nil {
		hooks.Latency = latency.Snapshot
	}
	if telegramHolder != nil {
		hooks.Telegram = telegramHolder.State
	}

	if url := strings.TrimSpace(brainURL); url != "" {
		hooks.BrainStatus = buildBrainStatusProbe(url)
		// Edge node: loaded models live on brain, not locally.
		hooks.LoadedModels = buildBrainModelsProbe(url)
		hooks.OllamaAvailable = func(ctx context.Context) bool { return true }
		return hooks
	}

	lister, ok := smartProvider.(basellm.PsLister)
	if !ok {
		return hooks
	}

	var ollamaState ollamaAvailability
	hooks.LoadedModels = func(ctx context.Context) []systemskills.LoadedModelInfo {
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		loaded, err := lister.ListLoadedModels(lookupCtx)
		ollamaState.Set(err == nil)
		if err != nil || len(loaded) == 0 {
			return nil
		}
		out := make([]systemskills.LoadedModelInfo, 0, len(loaded))
		for _, m := range loaded {
			out = append(out, systemskills.LoadedModelInfo{
				Name:      strings.TrimSpace(m.Name),
				Size:      m.Size,
				SizeVRAM:  m.SizeVRAM,
				Processor: strings.TrimSpace(m.Processor),
				Context:   m.ContextLen,
				ExpiresAt: formatExpiry(m.ExpiresAt, time.Now()),
			})
		}
		return out
	}
	hooks.OllamaAvailable = func(ctx context.Context) bool {
		return ollamaState.Get()
	}
	return hooks
}

// foreverThreshold marks anything expiring more than five years out as
// effectively forever. Ollama signals keep_alive=-1 by emitting either
// the unix epoch or a saturated far-future timestamp (≈ year 2318),
// depending on version; both should render the same way.
const foreverThreshold = 5 * 365 * 24 * time.Hour

// formatExpiry condenses Ollama's expires_at field into the per-model
// status line. "" when the timestamp is missing, "forever" for pinned
// entries, "in 5m" / "in 1h 30m" for time-limited entries.
func formatExpiry(expiresAt, now time.Time) string {
	if expiresAt.IsZero() {
		return ""
	}
	if expiresAt.Unix() <= 0 || expiresAt.After(now.Add(foreverThreshold)) {
		return "forever"
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return "expired"
	}
	return "in " + shortDuration(remaining)
}

// shortDuration renders a positive duration as a compact "1h 5m" / "30s"
// string suitable for inline rendering. Anything under a minute keeps
// second precision so users can tell a model is about to drop.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return formatDurationSeconds(d)
	}
	d = d.Round(time.Minute)
	hours := int64(d / time.Hour)
	minutes := int64((d % time.Hour) / time.Minute)
	switch {
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func formatDurationSeconds(d time.Duration) string {
	secs := int64(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

// ollamaAvailability caches the result of the most recent /api/ps probe
// so the status skill can distinguish "no models loaded" from "Ollama is
// down" without firing a second HTTP request.
type ollamaAvailability struct {
	mu        sync.RWMutex
	available bool
	observed  bool
}

func (a *ollamaAvailability) Set(ok bool) {
	a.mu.Lock()
	a.available = ok
	a.observed = true
	a.mu.Unlock()
}

func (a *ollamaAvailability) Get() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.observed {
		// No probe has fired yet; default to "available" so the status
		// skill rendering proceeds to query LoadedModels itself.
		return true
	}
	return a.available
}

// buildBrainStatusProbe returns a BrainStatus hook that probes the brain's
// /health endpoint each time /status is rendered.
func buildBrainStatusProbe(brainURL string) func(ctx context.Context) systemskills.BrainStatusInfo {
	base := strings.TrimRight(brainURL, "/")
	return func(ctx context.Context) systemskills.BrainStatusInfo {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()

		t0 := time.Now()
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, base+"/health", nil)
		if err != nil {
			return systemskills.BrainStatusInfo{Error: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		pingMs := float64(time.Since(t0).Milliseconds())
		if err != nil {
			msg := err.Error()
			if len(msg) > 60 {
				msg = msg[:60]
			}
			return systemskills.BrainStatusInfo{Error: msg}
		}
		defer resp.Body.Close()

		var h map[string]any
		if decErr := json.NewDecoder(resp.Body).Decode(&h); decErr != nil {
			return systemskills.BrainStatusInfo{Online: true, PingMs: pingMs}
		}

		info := systemskills.BrainStatusInfo{
			Online: h["status"] == "ok",
			PingMs: pingMs,
		}
		if v, ok := h["node_id"].(string); ok {
			info.NodeID = v
		}
		if v, ok := h["model"].(string); ok {
			info.Model = v
		}
		if v, ok := h["fast_model"].(string); ok {
			info.FastModel = v
		}
		if v, ok := h["smart_latency_ms"].(float64); ok {
			info.SmartLatencyMs = v
		}
		if v, ok := h["fast_latency_ms"].(float64); ok {
			info.FastLatencyMs = v
		}
		if v, ok := h["uptime_s"].(float64); ok {
			info.UptimeS = int64(v)
		}

		if info.Online {
			sysReq, sysErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/system", nil)
			if sysErr == nil {
				if sysResp, sysDoErr := http.DefaultClient.Do(sysReq); sysDoErr == nil {
					var s map[string]any
					if json.NewDecoder(sysResp.Body).Decode(&s) == nil {
						if v, ok := s["cpu_pct"].(float64); ok {
							info.CPUPct = v
						}
						if v, ok := s["mem_used_gb"].(float64); ok {
							info.MemUsedGB = v
						}
						if v, ok := s["mem_total_gb"].(float64); ok {
							info.MemTotalGB = v
						}
					}
					sysResp.Body.Close()
				}
			}
		}

		return info
	}
}

// buildBrainModelsProbe returns a LoadedModels hook that fetches the list of
// currently loaded Ollama models from the brain's GET /models endpoint.
func buildBrainModelsProbe(brainURL string) func(ctx context.Context) []systemskills.LoadedModelInfo {
	base := strings.TrimRight(brainURL, "/")
	return func(ctx context.Context) []systemskills.LoadedModelInfo {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, base+"/models", nil)
		if err != nil {
			return nil
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()

		var body struct {
			Models []struct {
				Name       string    `json:"name"`
				Size       int64     `json:"size"`
				SizeVRAM   int64     `json:"size_vram"`
				Processor  string    `json:"processor"`
				ContextLen int       `json:"context_length"`
				ExpiresAt  time.Time `json:"expires_at"`
			} `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Models) == 0 {
			return nil
		}

		out := make([]systemskills.LoadedModelInfo, 0, len(body.Models))
		for _, m := range body.Models {
			out = append(out, systemskills.LoadedModelInfo{
				Name:      strings.TrimSpace(m.Name),
				Size:      m.Size,
				SizeVRAM:  m.SizeVRAM,
				Processor: strings.TrimSpace(m.Processor),
				Context:   m.ContextLen,
				ExpiresAt: formatExpiry(m.ExpiresAt, time.Now()),
			})
		}
		return out
	}
}
