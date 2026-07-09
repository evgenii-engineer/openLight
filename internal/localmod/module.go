// Package localmod is openLight's extension point for local, private modules.
//
// It is deliberately generic: core openLight knows nothing about any specific
// module. Private modules live under local_modules/ (gitignored) and register
// themselves with this package from an init() function. The host binary only
// compiles a local module in when a gitignored blank-import hook
// (cmd/openlight/localmodules_local.go, copied from the example scaffold)
// imports it. Fresh clones that have no local_modules/ therefore build and run
// exactly as before.
//
// Activation is separate from compilation: even a compiled-in module only runs
// when OPENLIGHT_LOCAL_MODULES_ENABLED=true and its name appears in
// OPENLIGHT_LOCAL_MODULES. See Load.
package localmod

import (
	"sort"
	"sync"
)

// Module is the minimal interface a local module implements. The function-based
// register(app_context) shape from the design doc maps to Register here; the
// class-based OpenLightModule shape maps to a struct implementing this
// interface. When a module offers both, this interface wins because it is the
// only thing the loader calls.
type Module interface {
	// Name is the identifier used in OPENLIGHT_LOCAL_MODULES.
	Name() string
	// Register wires the module into the host using the supplied context. It
	// runs once at startup. Returning an error (or panicking) is contained by
	// the loader and never crashes openLight.
	Register(ctx *AppContext) error
}

// RegisterFunc adapts a plain register(app_context) function into a Module.
type RegisterFunc struct {
	ModuleName string
	Fn         func(ctx *AppContext) error
}

func (f RegisterFunc) Name() string { return f.ModuleName }

func (f RegisterFunc) Register(ctx *AppContext) error {
	if f.Fn == nil {
		return nil
	}
	return f.Fn(ctx)
}

var (
	registryMu sync.Mutex
	registry   = map[string]Module{}
)

// Register records a module so the loader can activate it by name. Local
// modules call this from an init() function. Registering the same name twice
// keeps the first registration and is a no-op for the second, so a stray
// double blank-import cannot crash startup.
func Register(module Module) {
	if module == nil {
		return
	}
	name := module.Name()
	if name == "" {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		return
	}
	registry[name] = module
}

// registered returns a snapshot of the current registry keyed by name.
func registered() map[string]Module {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make(map[string]Module, len(registry))
	for name, mod := range registry {
		out[name] = mod
	}
	return out
}

// RegisteredNames lists the names of all compiled-in modules, sorted. Useful
// for diagnostics and tests.
func RegisteredNames() []string {
	snapshot := registered()
	names := make([]string, 0, len(snapshot))
	for name := range snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resetForTest clears the registry. Test-only helper.
func resetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Module{}
}
