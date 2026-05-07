package vision

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

type analyzeSkill struct {
	manager Manager
}

// NewAnalyzeSkill returns a skill that asks the configured vision provider to
// describe a single image on disk.
func NewAnalyzeSkill(manager Manager) skills.Skill {
	return &analyzeSkill{manager: manager}
}

func (s *analyzeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "vision_analyze",
		Group:       skills.GroupVision,
		Description: "Describe or answer a question about a local image (screenshot, photo, page capture).",
		Aliases:     []string{"vision analyze", "describe image", "analyze image", "analyze screenshot"},
		Usage:       "/vision analyze <path> [:: <question>]",
		Examples:    []string{"vision_analyze ./data/browser-artifacts/example-com.png :: what error is on the page"},
	}
}

func (s *analyzeSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Image path?", Placeholder: "./data/browser-artifacts/page.png"},
			{Name: "prompt", Prompt: "What should I look at? (optional)", Placeholder: "Describe what is shown"},
		},
	}
}

func (s *analyzeSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	path := strings.TrimSpace(input.Args["path"])
	prompt := strings.TrimSpace(input.Args["prompt"])
	if path == "" {
		// Slash-command path doesn't fill named args. Recover from RawText:
		// `/vision_analyze ./img.png` → path
		// `/vision_analyze ./img.png :: focus on buttons` → path + prompt
		fallbackPath, fallbackPrompt := splitPathPrompt(stripCommandPrefix(input.RawText, []string{
			"/vision_analyze", "/vision analyze", "vision_analyze", "vision analyze",
			"describe image", "analyze image", "analyze screenshot",
		}))
		path = fallbackPath
		if prompt == "" {
			prompt = fallbackPrompt
		}
	}
	result, err := s.manager.Analyze(ctx, path, prompt)
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("Image: %s", result.Path),
	}
	if backend := backendLine(result.Provider, result.Model); backend != "" {
		lines = append(lines, backend)
	}
	if description := strings.TrimSpace(result.Description); description != "" {
		lines = append(lines, "", description)
	} else {
		lines = append(lines, "", "(provider returned no description)")
	}
	out := skills.Result{Text: strings.Join(lines, "\n")}
	out.Attachments = []skills.Attachment{{
		Path:    result.Path,
		Caption: strings.TrimSpace(result.Description),
		Kind:    skills.AttachmentPhoto,
	}}
	return out, nil
}

type compareSkill struct {
	manager Manager
}

// NewCompareSkill returns a skill that diffs two images and asks the provider
// to summarize the change.
func NewCompareSkill(manager Manager) skills.Skill {
	return &compareSkill{manager: manager}
}

func (s *compareSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "vision_compare",
		Group:       skills.GroupVision,
		Description: "Compare two local images, report a structural diff, and summarize the change.",
		Aliases:     []string{"vision compare", "compare images", "compare screenshots", "image diff"},
		Usage:       "/vision compare <baseline> <candidate> [:: <focus>]",
		Examples:    []string{"vision_compare ./snapshots/old.png ./snapshots/new.png"},
	}
}

func (s *compareSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "baseline", Prompt: "Baseline image path?", Placeholder: "./snapshots/old.png"},
			{Name: "candidate", Prompt: "Candidate image path?", Placeholder: "./snapshots/new.png"},
			{Name: "prompt", Prompt: "What should I focus on? (optional)", Placeholder: "Compare layouts and product cards"},
		},
	}
}

func (s *compareSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	baseline := strings.TrimSpace(input.Args["baseline"])
	candidate := strings.TrimSpace(input.Args["candidate"])
	prompt := strings.TrimSpace(input.Args["prompt"])

	if baseline == "" || candidate == "" {
		// Slash-command fallback. Format:
		// `/vision_compare <baseline> <candidate>`
		// `/vision_compare <baseline> <candidate> :: <focus>`
		text := stripCommandPrefix(input.RawText, []string{
			"/vision_compare", "/vision compare", "vision_compare", "vision compare",
			"compare images", "compare screenshots", "image diff",
		})
		paths, focus := splitTwoPathsPrompt(text)
		if baseline == "" && len(paths) > 0 {
			baseline = paths[0]
		}
		if candidate == "" && len(paths) > 1 {
			candidate = paths[1]
		}
		if prompt == "" {
			prompt = focus
		}
	}

	if baseline == "" || candidate == "" {
		return skills.Result{}, fmt.Errorf("%w: baseline and candidate paths are required", skills.ErrInvalidArguments)
	}

	result, err := s.manager.Compare(ctx, baseline, candidate, prompt)
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("Baseline: %s", result.BaselinePath),
		fmt.Sprintf("Candidate: %s", result.CandidatePath),
		fmt.Sprintf("Pixel diff: %d/%d cells changed (%.1f%% above %.0f%% threshold)",
			result.Diff.ChangedCells,
			result.Diff.TotalCells,
			result.Diff.ChangedFraction()*100,
			result.Diff.Threshold*100,
		),
	}
	if backend := backendLine(result.Provider, result.Model); backend != "" {
		lines = append(lines, backend)
	}
	if description := strings.TrimSpace(result.Description); description != "" {
		lines = append(lines, "", description)
	}
	out := skills.Result{Text: strings.Join(lines, "\n")}
	out.Attachments = []skills.Attachment{
		{Path: result.BaselinePath, Caption: "before", Kind: skills.AttachmentPhoto},
		{Path: result.CandidatePath, Caption: "after", Kind: skills.AttachmentPhoto},
	}
	return out, nil
}

// stripCommandPrefix removes one of the supplied alias prefixes (longest
// match wins) from the raw text. Returns the unchanged input if no alias
// matched. Used by skills that need to recover their args from the trailing
// text of a slash command, since the deterministic router doesn't map
// trailing text into named arg slots.
func stripCommandPrefix(text string, prefixes []string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	// Try longest first so "/vision_compare" wins over "/vision".
	best := ""
	for _, prefix := range prefixes {
		p := strings.ToLower(strings.TrimSpace(prefix))
		if p == "" {
			continue
		}
		if strings.HasPrefix(lower, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return text
	}
	rest := strings.TrimSpace(text[len(best):])
	rest = strings.TrimLeft(rest, ":")
	return strings.TrimSpace(rest)
}

// splitPathPrompt splits a `<path> :: <prompt>` string into the two parts.
// When `::` is absent, the entire input is treated as the path and the
// prompt is empty. Mirrors the syntax shown in skill Usage strings.
func splitPathPrompt(text string) (path, prompt string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if idx := strings.Index(text, "::"); idx >= 0 {
		return strings.TrimSpace(text[:idx]), strings.TrimSpace(text[idx+2:])
	}
	return text, ""
}

// splitTwoPathsPrompt parses a `<baseline> <candidate> [:: <prompt>]` string
// where the two paths are separated by whitespace. Quoted paths aren't
// supported — the deterministic format covers the common case (no spaces
// in artifact filenames produced by browser_screenshot).
func splitTwoPathsPrompt(text string) (paths []string, prompt string) {
	body, prompt := splitPathPrompt(text)
	if body == "" {
		return nil, prompt
	}
	fields := strings.Fields(body)
	if len(fields) > 2 {
		// Recombine extra fields into the second path so the user sees a
		// "image not found" error instead of silent truncation.
		fields = append(fields[:1], strings.Join(fields[1:], " "))
	}
	return fields, prompt
}

func backendLine(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	switch {
	case provider != "" && model != "":
		return fmt.Sprintf("Provider: %s (%s)", provider, model)
	case provider != "":
		return fmt.Sprintf("Provider: %s", provider)
	case model != "":
		return fmt.Sprintf("Model: %s", model)
	default:
		return ""
	}
}
