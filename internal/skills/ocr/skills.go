package ocr

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

const maxOCRPreviewChars = 3000

type extractSkill struct {
	manager Manager
}

// NewExtractSkill returns a skill that extracts text from a local image.
func NewExtractSkill(manager Manager) skills.Skill {
	return &extractSkill{manager: manager}
}

func (s *extractSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "ocr_extract",
		Group:       skills.GroupOCR,
		Description: "Extract text from a local image (screenshot, photo, receipt) using the configured OCR backend.",
		Aliases:     []string{"ocr extract", "read text", "extract text", "ocr screenshot"},
		Usage:       "/ocr extract <path>",
		Examples:    []string{"ocr_extract ./data/browser-artifacts/page.png"},
	}
}

func (s *extractSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Image path?", Placeholder: "./data/browser-artifacts/page.png"},
		},
	}
}

func (s *extractSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	path := strings.TrimSpace(input.Args["path"])
	if path == "" {
		// Slash-command fallback: `/ocr_extract ./img.png` etc.
		path = stripCommandPrefix(input.RawText, []string{
			"/ocr_extract", "/ocr extract", "ocr_extract", "ocr extract",
			"read text", "extract text", "ocr screenshot",
		})
	}
	result, err := s.manager.Extract(ctx, path)
	if err != nil {
		return skills.Result{}, err
	}

	body := strings.TrimSpace(result.Text)
	lines := []string{
		fmt.Sprintf("Image: %s", result.Path),
	}
	if provider := strings.TrimSpace(result.Provider); provider != "" {
		lines = append(lines, fmt.Sprintf("Provider: %s", provider))
	}
	if body == "" {
		lines = append(lines, "", "(no text detected)")
		return skills.Result{Text: strings.Join(lines, "\n")}, nil
	}
	lines = append(lines, "", truncate(body, maxOCRPreviewChars))
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

func truncate(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// stripCommandPrefix strips a slash-command alias from the head of the raw
// text and returns the body. Used to recover args from trailing slash text,
// which the deterministic router doesn't map into named arg slots.
func stripCommandPrefix(text string, prefixes []string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
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
