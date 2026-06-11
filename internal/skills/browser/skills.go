package browser

import (
	"context"
	"fmt"
	"regexp"
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

func (s *titleSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL?", Placeholder: "https://example.com"},
		},
	}
}

func (s *titleSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	url := resolveURLArg(input, []string{
		"/browser_title", "/browser title", "browser_title", "browser title", "page title",
	})
	response, err := s.manager.Title(ctx, url)
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

func (s *textSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL?", Placeholder: "https://example.com"},
		},
	}
}

func (s *textSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	url := resolveURLArg(input, []string{
		"/browser_text", "/browser text", "browser_text", "browser text", "page text",
	})
	response, err := s.manager.Text(ctx, url)
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

func (s *screenshotSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL?", Placeholder: "https://example.com"},
		},
	}
}

func (s *screenshotSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	url := resolveURLArg(input, []string{
		"/browser_screenshot", "/browser screenshot", "browser_screenshot",
		"browser screenshot", "page screenshot",
	})
	response, err := s.manager.Screenshot(ctx, url)
	if err != nil {
		return skills.Result{}, err
	}
	title := fallbackValue(response.Title, "(untitled)")
	lines := []string{
		fmt.Sprintf("Saved screenshot: %s", response.ScreenshotPath),
		fmt.Sprintf("Page title: %s", title),
	}
	result := skills.Result{Text: strings.Join(lines, "\n")}
	if path := strings.TrimSpace(response.ScreenshotPath); path != "" {
		result.Attachments = []skills.Attachment{{
			Path:    path,
			Caption: title,
			Kind:    skills.AttachmentPhoto,
		}}
	}
	return result, nil
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

func (s *checkSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL to check?", Placeholder: "https://example.com"},
			{Name: "expected_text", Prompt: "Which text should appear on the page?", Placeholder: "Welcome"},
		},
	}
}

func (s *checkSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	url := strings.TrimSpace(input.Args["url"])
	expected := strings.TrimSpace(input.Args["expected_text"])
	if url == "" {
		// Slash-command fallback. Format:
		//   /browser_check https://example.com :: expected text
		text := stripCommandPrefix(input.RawText, []string{
			"/browser_check", "/browser check", "browser_check", "browser check", "page check",
		})
		fallbackURL, fallbackText := splitURLExpected(text)
		url = fallbackURL
		if expected == "" {
			expected = fallbackText
		}
	}
	if url == "" {
		return skills.Result{}, fmt.Errorf("invalid arguments: url is required")
	}
	// expected_text is optional — if omitted, just verify the page loads
	if expected == "" {
		response, err := s.manager.Title(ctx, url)
		if err != nil {
			return skills.Result{}, err
		}
		return skills.Result{
			Text: fmt.Sprintf("Page loaded OK\nTitle: %s", fallbackValue(response.Title, "(untitled)")),
		}, nil
	}
	response, err := s.manager.Check(ctx, url, expected)
	if err != nil {
		return skills.Result{}, err
	}
	status := "missing"
	if response.ContainsText {
		status = "present"
	}
	return skills.Result{
		Text: fmt.Sprintf("Page title: %s\nExpected text (%q): %s", fallbackValue(response.Title, "(untitled)"), expected, status),
	}, nil
}

func fallbackValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// resolveURLArg returns the URL the user actually meant. Args["url"] wins
// when present *and* looks like a URL; otherwise we scan the raw text for
// a URL pattern. This keeps LLM-extraction noise from sinking requests:
// if the classifier hands us "сайта" or "the_site", we fall back to the
// `example.com` token sitting next to it.
func resolveURLArg(input skills.Input, prefixes []string) string {
	url := strings.TrimSpace(input.Args["url"])
	if url != "" && looksLikeURL(url) {
		return url
	}
	if found := findFirstURL(input.RawText); found != "" {
		return found
	}
	if url != "" {
		return url
	}
	body := stripCommandPrefix(input.RawText, prefixes)
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// looksLikeURL returns true for inputs that have at least one of the URL
// markers we care about: a scheme, a dot (TLD), a slash, or the special
// `localhost` token. Cyrillic words like "сайта" don't match — neither do
// stray English words like "the".
func looksLikeURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	if strings.Contains(value, "://") || strings.Contains(value, "/") {
		return true
	}
	if strings.Contains(value, ".") {
		return true
	}
	if value == "localhost" {
		return true
	}
	return false
}

// urlPattern matches typical bare and schemed URLs in free text. It only
// recognises ASCII hostnames so a Cyrillic word like "сайта" cannot win
// over an English domain that follows it.
var urlPattern = regexp.MustCompile(`(?i)\b(?:https?://)?(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}(?:/[^\s]*)?`)

// findFirstURL extracts the first URL-looking substring from text or "".
func findFirstURL(text string) string {
	return urlPattern.FindString(text)
}

// stripCommandPrefix strips a slash-command alias from the head of the raw
// text (longest match wins) and returns the remainder. Mirrors the helper
// in vision and ocr packages.
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

// splitURLExpected parses a `<url> :: <expected text>` body into its parts.
func splitURLExpected(text string) (url, expected string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if idx := strings.Index(text, "::"); idx >= 0 {
		return strings.TrimSpace(text[:idx]), strings.TrimSpace(text[idx+2:])
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	url = fields[0]
	if len(fields) > 1 {
		expected = strings.Join(fields[1:], " ")
	}
	return
}
