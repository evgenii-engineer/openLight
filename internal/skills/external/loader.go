package external

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"openlight/internal/skills"
)

// LoadResult is what the loader hands back for one root directory: the
// successfully parsed manifests in stable order, plus any per-directory
// errors so the caller can surface them without aborting discovery.
//
// A bad manifest in one directory must not prevent the rest from
// loading — operators usually want partial progress with audit lines
// rather than an all-or-nothing failure.
type LoadResult struct {
	Manifests []Manifest
	Errors    []LoadError
}

// LoadError pairs a directory with the error that prevented it from
// loading. The CLI's `skills validate` surfaces these verbatim.
type LoadError struct {
	Dir string
	Err error
}

// Error makes [LoadError] usable as a regular error in tests.
func (e LoadError) Error() string {
	return fmt.Sprintf("%s: %v", e.Dir, e.Err)
}

// Unwrap exposes the underlying error for errors.Is/As.
func (e LoadError) Unwrap() error {
	return e.Err
}

// DiscoverRoots scans each root, treating every immediate subdirectory
// that contains a [ManifestFileName] as a candidate skill. Roots that
// don't exist are silently skipped — operators should be able to point
// at `~/.openlight/skills` and `/etc/openlight/skills` without having
// to mkdir both up front.
//
// The result is sorted by manifest name so registration order — and
// therefore audit log ordering — is deterministic across runs.
func DiscoverRoots(roots []string, logger *slog.Logger) LoadResult {
	if logger == nil {
		logger = slog.Default()
	}
	var combined LoadResult
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		expanded, err := expandRoot(root)
		if err != nil {
			combined.Errors = append(combined.Errors, LoadError{Dir: root, Err: err})
			continue
		}
		info, err := os.Stat(expanded)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				logger.Debug("external skills root missing — skipping", "root", expanded)
				continue
			}
			combined.Errors = append(combined.Errors, LoadError{Dir: expanded, Err: err})
			continue
		}
		if !info.IsDir() {
			combined.Errors = append(combined.Errors, LoadError{Dir: expanded, Err: fmt.Errorf("not a directory")})
			continue
		}
		rootResult := loadRoot(expanded, logger)
		combined.Manifests = append(combined.Manifests, rootResult.Manifests...)
		combined.Errors = append(combined.Errors, rootResult.Errors...)
	}

	sort.SliceStable(combined.Manifests, func(i, j int) bool {
		return combined.Manifests[i].Name < combined.Manifests[j].Name
	})

	// Detect duplicate names early so the registry doesn't have to
	// reject one of them with a confusing "already registered" error.
	// Roots are scanned in declared order, so the first occurrence
	// wins; later duplicates land in Errors.
	seen := make(map[string]string, len(combined.Manifests))
	deduped := combined.Manifests[:0]
	for _, m := range combined.Manifests {
		if existing, ok := seen[m.Name]; ok {
			combined.Errors = append(combined.Errors, LoadError{
				Dir: m.Dir,
				Err: fmt.Errorf("duplicate skill name %q — first defined in %s", m.Name, existing),
			})
			continue
		}
		seen[m.Name] = m.Dir
		deduped = append(deduped, m)
	}
	combined.Manifests = deduped
	return combined
}

func loadRoot(root string, logger *slog.Logger) LoadResult {
	entries, err := os.ReadDir(root)
	if err != nil {
		return LoadResult{Errors: []LoadError{{Dir: root, Err: err}}}
	}
	var result LoadResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(dir, ManifestFileName)
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				// Directory without a manifest is not an error — the
				// user may have stashed unrelated assets here.
				continue
			}
			result.Errors = append(result.Errors, LoadError{Dir: dir, Err: statErr})
			continue
		}
		manifest, err := ParseManifestFile(manifestPath)
		if err != nil {
			result.Errors = append(result.Errors, LoadError{Dir: dir, Err: err})
			continue
		}
		result.Manifests = append(result.Manifests, manifest)
		logger.Info("external skill discovered",
			"name", manifest.Name,
			"version", manifest.Version,
			"dir", dir,
		)
	}
	return result
}

// expandRoot resolves `~` and environment placeholders so authors can
// configure roots in YAML without absolute paths. Anything fancier
// (globs, symlink chasing) is intentionally out of scope.
func expandRoot(root string) (string, error) {
	expanded := os.ExpandEnv(root)
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", root, err)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~"))
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", root, err)
	}
	return abs, nil
}

// resolveGroup maps the manifest's `group:` string onto a known
// registry [skills.Group]. Unknown names fall through to GroupOther so
// authors who pick something idiosyncratic still show up in /skills
// rather than vanishing.
func resolveGroup(name string) skills.Group {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "other":
		return skills.GroupOther
	case "chat":
		return skills.GroupChat
	case "notes":
		return skills.GroupNotes
	case "memory":
		return skills.GroupMemory
	case "files":
		return skills.GroupFiles
	case "browser":
		return skills.GroupBrowser
	case "vision":
		return skills.GroupVision
	case "ocr":
		return skills.GroupOCR
	case "visual_watch", "visual-watch":
		return skills.GroupVisualWatch
	case "workbench":
		return skills.GroupWorkbench
	case "services":
		return skills.GroupServices
	case "watch":
		return skills.GroupWatch
	case "accounts":
		return skills.GroupAccounts
	case "system":
		return skills.GroupSystem
	case "core":
		return skills.GroupCore
	case "network":
		return skills.GroupNetwork
	case "mcp":
		return skills.GroupMCP
	default:
		return skills.GroupOther
	}
}
