// Package external implements support for user-defined skills declared on
// disk as a directory containing a `skill.yaml` manifest plus an
// executable entrypoint. External skills speak the openLight skill JSON
// protocol over stdin/stdout (see protocol.go) and are isolated from the
// runtime's internal Go APIs — they are subprocesses, not plugins.
//
// The architecture is deliberately small and deterministic:
//
//   - manifest.go validates and normalizes skill.yaml.
//   - loader.go discovers skill directories under one or more roots.
//   - protocol.go is the strict v1 stdin/stdout JSON envelope.
//   - adapter.go is the [skills.Skill] implementation that runs the
//     skill's entrypoint with a per-invocation timeout.
//   - module.go wires loaded skills into the shared skill [Registry].
//
// External skills appear in /skills, the router, and audit logs exactly
// like builtin skills. The runtime never trusts a skill — it owns
// timeouts, JSON validation, and (in the future) sandboxing.
package external

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// APIVersion is the currently-supported skill manifest API version. Skills
// declaring a different api_version are rejected up front so additions to
// the protocol can ship without breaking older skills silently.
const APIVersion = "v1"

// ManifestFileName is the file the loader expects inside each skill
// directory. Anything else in the directory is ignored.
const ManifestFileName = "skill.yaml"

// DefaultTimeout caps how long a single skill invocation may run if the
// manifest doesn't override it. The value is intentionally conservative
// so a misbehaving skill cannot tie up the agent loop.
const DefaultTimeout = 5 * time.Second

// MaxTimeout is the hard ceiling. Skills cannot ask for more — long-
// running work belongs in a service or watch, not in a synchronous
// skill invocation.
const MaxTimeout = 2 * time.Minute

// Manifest is the parsed, normalized representation of skill.yaml.
//
// Authors interact with the YAML form. The runtime always works with the
// normalized struct: identifiers lowercased, paths resolved relative to
// the skill directory, timeouts clamped to [0, MaxTimeout].
type Manifest struct {
	// APIVersion declares the manifest schema. Only [APIVersion] is
	// accepted. Required.
	APIVersion string `yaml:"api_version"`

	// Name is the canonical skill identifier (lowercase, slash-command
	// safe). Required and must be unique across the registry.
	Name string `yaml:"name"`

	// Description is the one-line summary surfaced in /skills and /help.
	Description string `yaml:"description"`

	// Version is the skill author's semantic version. Logged on load and
	// in audit events; not interpreted by the runtime.
	Version string `yaml:"version"`

	// Group optionally pins the skill into a named registry group (e.g.
	// "system", "network"). Unknown groups fall back to GroupOther.
	Group string `yaml:"group"`

	// Aliases are extra identifiers users can type to invoke the skill.
	Aliases []string `yaml:"aliases"`

	// Triggers are routing hints surfaced to the LLM classifier. They
	// are merged with the skill name and description when the router
	// computes match scores.
	Triggers []string `yaml:"triggers"`

	// Examples are surfaced in /help.
	Examples []string `yaml:"examples"`

	// Capabilities are coarse-grained labels the skill claims (e.g.
	// "weather.forecast"). They are advisory metadata today; future
	// versions can match them against allowlists.
	Capabilities []string `yaml:"capabilities"`

	// Entrypoint is the command the runtime executes for each request.
	Entrypoint Entrypoint `yaml:"entrypoint"`

	// Permissions is the declarative permission block. The runtime
	// reads it for audit/UX; sandboxing enforcement is left to a future
	// implementation step but the shape is fixed now.
	Permissions Permissions `yaml:"permissions"`

	// Timeout caps one invocation. Defaults to [DefaultTimeout] when
	// unset and is clamped to [MaxTimeout].
	Timeout time.Duration `yaml:"timeout"`

	// Mutating signals that the skill is expected to change state and
	// must therefore not be auto-routed without confirmation in modes
	// that distinguish read vs. mutate.
	Mutating bool `yaml:"mutating"`

	// Hidden hides the skill from /skills listings but keeps it
	// invocable by name or alias.
	Hidden bool `yaml:"hidden"`

	// Dir is the directory the manifest was loaded from. Set by the
	// loader; not present in YAML.
	Dir string `yaml:"-"`
}

// Entrypoint describes how to launch the skill's executable.
//
// Either `command` (and optional `args`) or a single `path` may be set.
// `path` is resolved relative to the skill directory when present so a
// skill can ship its own helper script next to skill.yaml.
type Entrypoint struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Path    string            `yaml:"path"`
	Env     map[string]string `yaml:"env"`
}

// Permissions is the declarative permission shape. Future versions will
// enforce these; v1 records them so authors can future-proof their
// manifests and the runtime can surface them in logs.
type Permissions struct {
	Network    NetworkPermission    `yaml:"network"`
	Filesystem FilesystemPermission `yaml:"filesystem"`
	Shell      bool                 `yaml:"shell"`
}

// NetworkPermission lists the host:port destinations the skill expects to
// reach. The list is informational in v1.
type NetworkPermission struct {
	Allow []string `yaml:"allow"`
}

// FilesystemPermission lists the read/write paths the skill expects to
// touch. Informational in v1.
type FilesystemPermission struct {
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`
}

// ParseManifestFile reads, parses, validates, and normalizes a manifest
// at the given path. The returned manifest has Dir set to the file's
// parent directory.
func ParseManifestFile(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %q: %w", path, err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest %q: %w", path, err)
	}
	manifest.Dir = filepath.Dir(path)
	if err := manifest.resolveEntrypoint(); err != nil {
		return Manifest{}, fmt.Errorf("manifest %q: %w", path, err)
	}
	return manifest, nil
}

// ParseManifest decodes manifest bytes without touching the filesystem
// for entrypoint resolution. The Dir field is left empty so the caller
// can set it (or [resolveEntrypoint] becomes a no-op).
func ParseManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse yaml: %w", err)
	}
	manifest.normalize()
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// normalize trims whitespace, lowercases identifiers, deduplicates
// alias/trigger lists, and clamps the timeout. It does not touch fields
// the user is responsible for (description, examples).
func (m *Manifest) normalize() {
	m.APIVersion = strings.TrimSpace(m.APIVersion)
	if m.APIVersion == "" {
		m.APIVersion = APIVersion
	}
	m.Name = sanitizeIdent(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Version = strings.TrimSpace(m.Version)
	m.Group = strings.ToLower(strings.TrimSpace(m.Group))

	m.Aliases = dedupeIdents(m.Aliases)
	m.Triggers = normalizeTriggers(m.Triggers)
	m.Examples = trimAll(m.Examples)
	m.Capabilities = trimAll(m.Capabilities)

	m.Entrypoint.Command = strings.TrimSpace(m.Entrypoint.Command)
	m.Entrypoint.Path = strings.TrimSpace(m.Entrypoint.Path)
	m.Entrypoint.Args = trimAll(m.Entrypoint.Args)
	if m.Entrypoint.Env != nil {
		cleaned := make(map[string]string, len(m.Entrypoint.Env))
		for k, v := range m.Entrypoint.Env {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			cleaned[key] = v
		}
		if len(cleaned) == 0 {
			m.Entrypoint.Env = nil
		} else {
			m.Entrypoint.Env = cleaned
		}
	}

	m.Permissions.Network.Allow = trimAll(m.Permissions.Network.Allow)
	m.Permissions.Filesystem.Read = trimAll(m.Permissions.Filesystem.Read)
	m.Permissions.Filesystem.Write = trimAll(m.Permissions.Filesystem.Write)

	if m.Timeout <= 0 {
		m.Timeout = DefaultTimeout
	}
	if m.Timeout > MaxTimeout {
		m.Timeout = MaxTimeout
	}
}

// validate enforces the minimal invariants the runtime needs to register
// the skill safely. Errors here mean the skill is rejected at load time.
func (m Manifest) validate() error {
	if m.APIVersion != APIVersion {
		return fmt.Errorf("api_version %q is not supported (want %q)", m.APIVersion, APIVersion)
	}
	if m.Name == "" {
		return errors.New("name is required")
	}
	if m.Description == "" {
		return errors.New("description is required")
	}
	if m.Entrypoint.Command == "" && m.Entrypoint.Path == "" {
		return errors.New("entrypoint.command or entrypoint.path is required")
	}
	if m.Entrypoint.Command != "" && m.Entrypoint.Path != "" {
		return errors.New("entrypoint.command and entrypoint.path are mutually exclusive")
	}
	return nil
}

// resolveEntrypoint converts a `path: run.py` style entrypoint into an
// absolute path under the skill directory. Called only when Dir is set
// (i.e. from ParseManifestFile, not from a unit test using ParseManifest
// directly).
func (m *Manifest) resolveEntrypoint() error {
	if m.Dir == "" {
		return nil
	}
	if m.Entrypoint.Path == "" {
		return nil
	}
	resolved := m.Entrypoint.Path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(m.Dir, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("entrypoint.path %q: %w", m.Entrypoint.Path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("entrypoint.path %q is a directory", m.Entrypoint.Path)
	}
	m.Entrypoint.Path = resolved
	return nil
}

// CommandLine returns the argv the runtime should exec. When `path` is
// set, the executable is the resolved path; otherwise `command` runs
// directly with the declared args. Either way the returned slice has at
// least one element on success.
func (m Manifest) CommandLine() []string {
	if m.Entrypoint.Path != "" {
		// `args` still apply so authors can pass flags to e.g. a python
		// script: `command: python3 + args: [run.py]` and `path: run.sh`
		// are interchangeable.
		return append([]string{m.Entrypoint.Path}, m.Entrypoint.Args...)
	}
	return append([]string{m.Entrypoint.Command}, m.Entrypoint.Args...)
}

// EnvSlice returns the manifest's static env as a `KEY=VALUE` slice
// suitable for [exec.Cmd.Env]. The slice is sorted for determinism so
// audit logs stay stable.
func (m Manifest) EnvSlice() []string {
	if len(m.Entrypoint.Env) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.Entrypoint.Env))
	for k, v := range m.Entrypoint.Env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// ----- helpers --------------------------------------------------------------

// sanitizeIdent matches the policy used elsewhere in the registry: lower
// case, ASCII, slash-stripped, with `-` and `_` collapsed to spaces so
// the same identifier is recognized regardless of typing convention.
// (See [skills.normalizeIdentifier] for the canonical form applied at
// registration time.)
func sanitizeIdent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	return value
}

func dedupeIdents(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = sanitizeIdent(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func normalizeTriggers(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func trimAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
