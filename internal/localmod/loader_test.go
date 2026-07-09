package localmod

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeModule records whether Register ran and can be told to fail or panic.
type fakeModule struct {
	name       string
	mu         sync.Mutex
	registered bool
	fail       bool
	boom       bool
}

func (m *fakeModule) Name() string { return m.name }

func (m *fakeModule) Register(ctx *AppContext) error {
	m.mu.Lock()
	m.registered = true
	m.mu.Unlock()
	if m.boom {
		panic("kaboom")
	}
	if m.fail {
		return context.Canceled
	}
	return nil
}

func (m *fakeModule) wasRegistered() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registered
}

func baseDeps(env EnvReader) Deps {
	return Deps{Logger: testLogger(), Env: env, StorageDir: "."}
}

func TestLoadDisabledIsNoOp(t *testing.T) {
	resetForTest()
	mod := &fakeModule{name: "alpha"}
	Register(mod)

	// Enabled unset entirely.
	Load(context.Background(), baseDeps(MapEnv{
		EnvModules: "alpha",
	}))
	if mod.wasRegistered() {
		t.Fatal("module registered even though local modules are disabled")
	}
}

func TestLoadActivatesListedModule(t *testing.T) {
	resetForTest()
	mod := &fakeModule{name: "alpha"}
	Register(mod)

	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: "alpha",
	}))
	if !mod.wasRegistered() {
		t.Fatal("listed module was not registered")
	}
}

func TestLoadSkipsUnlistedModule(t *testing.T) {
	resetForTest()
	listed := &fakeModule{name: "alpha"}
	unlisted := &fakeModule{name: "beta"}
	Register(listed)
	Register(unlisted)

	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: "alpha",
	}))
	if !listed.wasRegistered() {
		t.Fatal("alpha should have registered")
	}
	if unlisted.wasRegistered() {
		t.Fatal("beta should not have registered")
	}
}

func TestLoadCommaSeparatedList(t *testing.T) {
	resetForTest()
	a := &fakeModule{name: "alpha"}
	b := &fakeModule{name: "beta"}
	Register(a)
	Register(b)

	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: " alpha , beta ",
	}))
	if !a.wasRegistered() || !b.wasRegistered() {
		t.Fatalf("both modules should register: alpha=%v beta=%v", a.wasRegistered(), b.wasRegistered())
	}
}

func TestLoadMissingModuleDoesNotCrash(t *testing.T) {
	resetForTest()
	present := &fakeModule{name: "alpha"}
	Register(present)

	// "ghost" is not compiled in; loader must warn and keep going, still
	// loading alpha.
	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: "ghost,alpha",
	}))
	if !present.wasRegistered() {
		t.Fatal("alpha should still register even though ghost is missing")
	}
}

func TestLoadFailingModuleDoesNotStopOthers(t *testing.T) {
	resetForTest()
	bad := &fakeModule{name: "bad", fail: true}
	good := &fakeModule{name: "good"}
	Register(bad)
	Register(good)

	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: "bad,good",
	}))
	if !good.wasRegistered() {
		t.Fatal("good module should register despite bad module error")
	}
}

func TestLoadPanickingModuleIsContained(t *testing.T) {
	resetForTest()
	boom := &fakeModule{name: "boom", boom: true}
	good := &fakeModule{name: "good"}
	Register(boom)
	Register(good)

	// If the panic escaped, the test process would crash.
	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvModules: "boom,good",
	}))
	if !good.wasRegistered() {
		t.Fatal("good module should register despite boom module panic")
	}
}

func TestLoadMissingPathWarnsButContinues(t *testing.T) {
	resetForTest()
	mod := &fakeModule{name: "alpha"}
	Register(mod)

	Load(context.Background(), baseDeps(MapEnv{
		EnvEnabled: "true",
		EnvPath:    "/nonexistent/path/to/local_modules",
		EnvModules: "alpha",
	}))
	if !mod.wasRegistered() {
		t.Fatal("module should register even when the configured path is missing")
	}
}

func TestParseModuleList(t *testing.T) {
	cases := map[string][]string{
		"":                nil,
		"  ":              nil,
		"a":               {"a"},
		"a,b,c":           {"a", "b", "c"},
		" a , b ,, c ":    {"a", "b", "c"},
		"munch_monitor,x": {"munch_monitor", "x"},
	}
	for in, want := range cases {
		got := parseModuleList(in)
		if len(got) != len(want) {
			t.Fatalf("parseModuleList(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parseModuleList(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}
