package localmod

import (
	"os"
	"strings"
)

// osEnv is the default EnvReader backed by the process environment.
type osEnv struct{}

// OSEnv returns an EnvReader backed by os.Getenv. Exposed so the host can pass
// it explicitly and tests can swap in a fake.
func OSEnv() EnvReader { return osEnv{} }

func (osEnv) Get(key string) string { return strings.TrimSpace(os.Getenv(key)) }

func (e osEnv) GetDefault(key, fallback string) string {
	if v := e.Get(key); v != "" {
		return v
	}
	return fallback
}

func (e osEnv) Bool(key string) bool { return parseBool(e.Get(key)) }

// parseBool treats common truthy spellings as true.
func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// overlayEnv reads from base first (e.g. the real OS environment); when a key
// is blank there it falls back to the overlay map (e.g. values from a config
// file). This lets real environment variables override config-file settings at
// runtime while keeping a single config file as the default source.
type overlayEnv struct {
	base    EnvReader
	overlay map[string]string
}

// NewOverlayEnv returns an EnvReader where base wins over overlay for any key
// that base has set. A nil base defaults to OSEnv().
func NewOverlayEnv(base EnvReader, overlay map[string]string) EnvReader {
	if base == nil {
		base = OSEnv()
	}
	return overlayEnv{base: base, overlay: overlay}
}

func (e overlayEnv) Get(key string) string {
	if v := e.base.Get(key); v != "" {
		return v
	}
	return strings.TrimSpace(e.overlay[key])
}

func (e overlayEnv) GetDefault(key, fallback string) string {
	if v := e.Get(key); v != "" {
		return v
	}
	return fallback
}

func (e overlayEnv) Bool(key string) bool { return parseBool(e.Get(key)) }

// MapEnv is an EnvReader backed by an in-memory map, for tests.
type MapEnv map[string]string

func (m MapEnv) Get(key string) string { return strings.TrimSpace(m[key]) }

func (m MapEnv) GetDefault(key, fallback string) string {
	if v := m.Get(key); v != "" {
		return v
	}
	return fallback
}

func (m MapEnv) Bool(key string) bool { return parseBool(m.Get(key)) }
