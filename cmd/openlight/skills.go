package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"openlight/internal/config"
	"openlight/internal/logging"
	"openlight/internal/runtime"
	"openlight/internal/skills"
	"openlight/internal/skills/external"
)

// runSkills is the entrypoint for `openlight skills <subcommand>`. It
// is a thin dispatcher over read-only inspection helpers — none of the
// subcommands mutate state or talk to Telegram, so it is safe to run
// against a live config from any host.
func runSkills(args []string) error {
	if len(args) == 0 {
		printSkillsUsage(os.Stderr)
		return fmt.Errorf("skills: missing subcommand")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runSkillsList(rest)
	case "validate":
		return runSkillsValidate(rest)
	case "reload":
		return runSkillsReload(rest)
	case "help", "-h", "--help":
		printSkillsUsage(os.Stdout)
		return nil
	default:
		printSkillsUsage(os.Stderr)
		return fmt.Errorf("skills: unknown subcommand %q", sub)
	}
}

func printSkillsUsage(w io.Writer) {
	fmt.Fprint(w, `openlight skills — inspect builtin and external skills

Usage:
  openlight skills <command> [flags]

Commands:
  list       List all registered skills (builtin + external)
  validate   Validate a single skill directory (or all configured roots)
  reload     Re-scan configured roots and report what would change

Examples:
  openlight skills list
  openlight skills list --external-only
  openlight skills validate ~/.openlight/skills/weather
  openlight skills reload
`)
}

// runSkillsList builds the full registry from the live config and
// prints one row per skill. The view distinguishes builtin from
// external skills so operators can tell at a glance which is which —
// useful when triaging unexpected behaviour.
func runSkillsList(args []string) error {
	fs := flag.NewFlagSet("skills list", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	externalOnly := fs.Bool("external-only", false, "Only show external (user-defined) skills")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Log.Level)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := runtime.BuildRuntime(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer runtime.CloseRuntime(rt)

	externalNames := make(map[string]struct{})
	if cfg.External.Enabled && len(cfg.External.Roots) > 0 {
		for _, m := range external.DiscoverRoots(cfg.External.Roots, logger).Manifests {
			externalNames[m.Name] = struct{}{}
		}
	}

	defs := rt.Registry.List()
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tGROUP\tDESCRIPTION")
	for _, def := range defs {
		source := "builtin"
		if _, isExternal := externalNames[def.Name]; isExternal {
			source = "external"
		}
		if *externalOnly && source != "external" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", def.Name, source, def.Group.Key, def.Description)
	}
	return tw.Flush()
}

// runSkillsValidate parses one or more skill directories without
// touching the registry. It is the fast loop for skill authors: edit
// skill.yaml, run `openlight skills validate ./mydir`, get a clear
// pass/fail line and the canonical command line that will run.
//
// With no arguments, it falls back to all configured roots so an
// operator can sweep an entire deployment before restarting the
// service.
func runSkillsValidate(args []string) error {
	fs := flag.NewFlagSet("skills validate", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets := fs.Args()
	if len(targets) == 0 {
		cfg, err := config.Load(resolveConfigPath(*configPath))
		if err != nil {
			return err
		}
		targets = append(targets, cfg.External.Roots...)
		if len(targets) == 0 {
			return fmt.Errorf("skills validate: no directories given and no external_skills.roots configured")
		}
		return validateRoots(targets)
	}
	return validateSpecificTargets(targets)
}

func validateRoots(roots []string) error {
	logger := logging.New("warn")
	result := external.DiscoverRoots(roots, logger)
	failures := 0
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tSKILL\tDIR\tDETAIL")
	for _, m := range result.Manifests {
		fmt.Fprintf(tw, "OK\t%s\t%s\t%s\n", m.Name, m.Dir, strings.Join(m.CommandLine(), " "))
	}
	for _, e := range result.Errors {
		failures++
		fmt.Fprintf(tw, "FAIL\t-\t%s\t%v\n", e.Dir, e.Err)
	}
	_ = tw.Flush()
	if failures > 0 {
		return fmt.Errorf("%d skill(s) failed validation", failures)
	}
	return nil
}

func validateSpecificTargets(targets []string) error {
	failures := 0
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tSKILL\tDIR\tDETAIL")
	for _, target := range targets {
		manifestPath := target
		// Accept either a directory containing skill.yaml or the
		// manifest path itself so editor "open file" workflows feel
		// natural.
		info, err := os.Stat(target)
		if err != nil {
			failures++
			fmt.Fprintf(tw, "FAIL\t-\t%s\t%v\n", target, err)
			continue
		}
		if info.IsDir() {
			manifestPath = filepath.Join(target, external.ManifestFileName)
		}
		manifest, err := external.ParseManifestFile(manifestPath)
		if err != nil {
			failures++
			fmt.Fprintf(tw, "FAIL\t-\t%s\t%v\n", target, err)
			continue
		}
		fmt.Fprintf(tw, "OK\t%s\t%s\t%s\n", manifest.Name, manifest.Dir, strings.Join(manifest.CommandLine(), " "))
	}
	_ = tw.Flush()
	if failures > 0 {
		return fmt.Errorf("%d skill(s) failed validation", failures)
	}
	return nil
}

// runSkillsReload re-scans the configured roots and prints what a fresh
// runtime would register. It does not signal a live agent — process
// reload is a separate concern (SIGHUP / supervisor restart). The
// command exists so operators can preview the effect of an edit before
// triggering a real restart.
func runSkillsReload(args []string) error {
	fs := flag.NewFlagSet("skills reload", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		return err
	}
	if !cfg.External.Enabled || len(cfg.External.Roots) == 0 {
		fmt.Fprintln(os.Stdout, "external skills are disabled (no roots configured)")
		return nil
	}
	logger := logging.New(cfg.Log.Level)
	result := external.DiscoverRoots(cfg.External.Roots, logger)
	fmt.Fprintf(os.Stdout, "scanned %d root(s): %d skill(s), %d error(s)\n",
		len(cfg.External.Roots), len(result.Manifests), len(result.Errors))
	for _, m := range result.Manifests {
		fmt.Fprintf(os.Stdout, "  ok    %s (%s) — %s\n", m.Name, m.Version, m.Dir)
	}
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stdout, "  fail  %s — %v\n", e.Dir, e.Err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("%d skill(s) failed to load", len(result.Errors))
	}
	return nil
}

// guard against unused-import lint when no helpers reference skills
// directly — the package is imported for type names in future
// extensions of `skills list`.
var _ = skills.Definition{}
