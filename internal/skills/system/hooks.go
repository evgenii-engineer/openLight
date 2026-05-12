package system

import (
	"context"
	"sync"
	"time"
)

// Hooks aggregates the runtime callbacks the /status skill needs to render
// signals that don't live on the cross-platform OS Provider: Ollama
// liveness, Telegram connectivity, agent self-status, and recent LLM
// latency. Every callback is optional — a nil hook causes the
// corresponding section to be omitted (when "skip" is the natural choice)
// or rendered as "unknown".
type Hooks struct {
	// LoadedModels returns the snapshot rendered by the "Ollama loaded"
	// section. Returning nil yields "Ollama loaded: none"; returning the
	// sentinel ErrLoadedModelsUnavailable from a lookup is not supported —
	// implementations should encode unavailability by returning nil and
	// surfacing the error via OllamaAvailable.
	LoadedModels func(ctx context.Context) []LoadedModelInfo

	// OllamaAvailable reports whether the Ollama endpoint responded at all
	// to the most recent lookup. When false the section becomes
	// "Ollama loaded: unavailable" instead of "none".
	OllamaAvailable func(ctx context.Context) bool

	// Telegram returns one of TelegramConnected / TelegramUnavailable /
	// TelegramUnknown. When nil the line is omitted.
	Telegram func(ctx context.Context) string

	// Agent reports openLight's self-status: running state plus pid. When
	// nil the section is omitted.
	Agent func(ctx context.Context) AgentInfo

	// Latency returns the most recent successful LLM latency per profile
	// ("fast", "smart"). Profiles with no observation are absent. When the
	// map is empty the section renders as "Last LLM latency: unknown".
	Latency func() map[string]time.Duration
}

// AgentInfo is a minimal snapshot of the openLight process itself.
type AgentInfo struct {
	Running bool
	PID     int
}

// Telegram state labels surfaced by the Hooks.Telegram callback.
const (
	TelegramConnected   = "connected"
	TelegramUnavailable = "unavailable"
	TelegramUnknown     = "unknown"
)

// LatencyStore is a tiny thread-safe last-success-per-profile recorder.
// It is intentionally minimal: no histograms, no rolling windows. The
// status skill only renders the latest observed value per profile.
type LatencyStore struct {
	mu     sync.RWMutex
	values map[string]time.Duration
}

func NewLatencyStore() *LatencyStore {
	return &LatencyStore{values: make(map[string]time.Duration)}
}

func (s *LatencyStore) Record(profile string, d time.Duration) {
	if s == nil || profile == "" || d < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[profile] = d
}

func (s *LatencyStore) Snapshot() map[string]time.Duration {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.values) == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out
}
