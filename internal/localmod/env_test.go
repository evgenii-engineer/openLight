package localmod

import (
	"context"
	"testing"
)

func TestOverlayEnvFallsBackToOverlay(t *testing.T) {
	env := NewOverlayEnv(MapEnv{}, map[string]string{"FOO": "bar"})
	if got := env.Get("FOO"); got != "bar" {
		t.Fatalf("expected overlay value 'bar', got %q", got)
	}
}

func TestOverlayEnvBaseWins(t *testing.T) {
	base := MapEnv{"FOO": "from-env"}
	env := NewOverlayEnv(base, map[string]string{"FOO": "from-yaml"})
	if got := env.Get("FOO"); got != "from-env" {
		t.Fatalf("real env should win, got %q", got)
	}
}

func TestOverlayEnvBoolAndDefault(t *testing.T) {
	env := NewOverlayEnv(MapEnv{}, map[string]string{"ENABLED": "true"})
	if !env.Bool("ENABLED") {
		t.Fatal("overlay bool should parse true")
	}
	if got := env.GetDefault("MISSING", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

// TestLoadViaOverlayActivatesModule proves the yaml-config path: when the
// control keys come from the overlay (as agent.yaml's local_modules block
// would inject them), the module still activates.
func TestLoadViaOverlayActivatesModule(t *testing.T) {
	resetForTest()
	mod := &fakeModule{name: "alpha"}
	Register(mod)

	overlay := map[string]string{
		EnvEnabled: "true",
		EnvModules: "alpha",
	}
	env := NewOverlayEnv(MapEnv{}, overlay)
	Load(context.Background(), Deps{Logger: testLogger(), Env: env, StorageDir: "."})

	if !mod.wasRegistered() {
		t.Fatal("module should activate when enabled via config overlay")
	}
}
