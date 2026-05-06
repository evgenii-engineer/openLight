package browser

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

type titleSkill struct {
	manager Manager
}

func NewTitleSkill(manager Manager) skills.Skill {
	return &titleSkill{manager: manager}
}

func (s *titleSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "browser_title",
		Group:       skills.GroupBrowser,
		Description: "Open a whitelisted URL and return its page title.",
		Aliases:     []string{"page title", "browser title"},
		Usage:       "/browse title <url>",
	}
}

func (s *titleSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	response, err := s.manager.Title(ctx, input.Args["url"])
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: fmt.Sprintf("Page title: %s", fallbackValue(response.Title, "(untitled)"))}, nil
}

type textSkill struct {
	manager Manager
}

func NewTextSkill(manager Manager) skills.Skill {
	return &textSkill{manager: manager}
}

func (s *textSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "browser_text",
		Group:       skills.GroupBrowser,
		Description: "Open a whitelisted URL and extract visible text.",
		Aliases:     []string{"browser text", "page text"},
		Usage:       "/browse text <url>",
	}
}

func (s *textSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	response, err := s.manager.Text(ctx, input.Args["url"])
	if err != nil {
		return skills.Result{}, err
	}
	lines := []string{fmt.Sprintf("Page title: %s", fallbackValue(response.Title, "(untitled)"))}
	if preview := strings.TrimSpace(response.TextPreview); preview != "" {
		lines = append(lines, "", preview)
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type screenshotSkill struct {
	manager Manager
}

func NewScreenshotSkill(manager Manager) skills.Skill {
	return &screenshotSkill{manager: manager}
}

func (s *screenshotSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "browser_screenshot",
		Group:       skills.GroupBrowser,
		Description: "Take a screenshot of a whitelisted URL.",
		Aliases:     []string{"page screenshot", "browser screenshot"},
		Usage:       "/browse screenshot <url>",
	}
}

func (s *screenshotSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	response, err := s.manager.Screenshot(ctx, input.Args["url"])
	if err != nil {
		return skills.Result{}, err
	}
	lines := []string{
		fmt.Sprintf("Saved screenshot: %s", response.ScreenshotPath),
		fmt.Sprintf("Page title: %s", fallbackValue(response.Title, "(untitled)")),
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type checkSkill struct {
	manager Manager
}

func NewCheckSkill(manager Manager) skills.Skill {
	return &checkSkill{manager: manager}
}

func (s *checkSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "browser_check",
		Group:       skills.GroupBrowser,
		Description: "Load a whitelisted URL and check whether expected text appears.",
		Aliases:     []string{"browser check", "page check"},
		Usage:       "/browse check <url> :: <expected text>",
	}
}

func (s *checkSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	response, err := s.manager.Check(ctx, input.Args["url"], input.Args["expected_text"])
	if err != nil {
		return skills.Result{}, err
	}
	status := "missing"
	if response.ContainsText {
		status = "present"
	}
	return skills.Result{
		Text: fmt.Sprintf("Page title: %s\nExpected text: %s", fallbackValue(response.Title, "(untitled)"), status),
	}, nil
}

func fallbackValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
