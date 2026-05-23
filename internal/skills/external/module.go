package external

import (
	"log/slog"

	"openlight/internal/skills"
)

// Options is the configuration external skill loading needs from the
// runtime. Roots are scanned in declared order, with earlier roots
// shadowing later ones if two manifests share a name.
//
// Enabled defaults to true at the runtime layer when at least one root
// is configured; the field exists so operators can disable loading
// without deleting their roots from the YAML file.
type Options struct {
	Enabled bool
	Roots   []string
}

// NewModule returns a [skills.Module] that discovers external skills
// from the configured roots and registers each one as an adapter. The
// module returns nil if `Enabled` is false so the runtime can wire it
// unconditionally.
//
// Discovery errors are logged but do NOT abort module registration —
// a single broken skill should not prevent the rest of the agent from
// starting. The CLI's `skills validate` command exposes the same errors
// in a foreground-friendly form.
func NewModule(opts Options, logger *slog.Logger) skills.Module {
	if logger == nil {
		logger = slog.Default()
	}
	return skills.NewModule("external", func(registry *skills.Registry) error {
		if !opts.Enabled {
			return nil
		}
		result := DiscoverRoots(opts.Roots, logger)
		for _, loadErr := range result.Errors {
			logger.Warn("external skill load failed",
				"dir", loadErr.Dir,
				"error", loadErr.Err,
			)
		}
		for _, manifest := range result.Manifests {
			skill := newAdapter(manifest, logger, nil)
			if err := registry.Register(skill); err != nil {
				logger.Warn("external skill registration failed",
					"name", manifest.Name,
					"dir", manifest.Dir,
					"error", err,
				)
				continue
			}
			logger.Info("external skill registered",
				"name", manifest.Name,
				"version", manifest.Version,
				"dir", manifest.Dir,
				"timeout", manifest.Timeout.String(),
			)
		}
		return nil
	})
}
