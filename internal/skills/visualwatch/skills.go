package visualwatch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
	"openlight/internal/storage"
	visualwatch "openlight/internal/visualwatch"
)

// Service is the surface visual-watch skills need from the engine.
type Service interface {
	Defaults() visualwatch.Options
	Evaluate(ctx context.Context, id int64) (visualwatch.EvaluateResult, error)
}

type addSkill struct {
	repository storage.Repository
	service    Service
}

func newAddSkill(repository storage.Repository, service Service) skills.Skill {
	return &addSkill{repository: repository, service: service}
}

func (s *addSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "visual_watch_add",
		Group:       skills.GroupVisualWatch,
		Description: "Watch a URL for visual changes (and optionally for keyword text) and notify when something happens.",
		Aliases:     []string{"visual watch add", "watch page", "watch url visually"},
		Usage:       "/visual_watch_add <url> [name=<name>] [interval=15m] [threshold=0.15] [cooldown=30m] [keywords=<a,b>] [notify=change|keywords|both]",
		Mutating:    true,
		Examples: []string{
			"visual_watch_add https://example.com/drop name=drop interval=10m keywords=available,size 42 notify=keywords",
		},
	}
}

func (s *addSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL?", Placeholder: "https://example.com"},
			{Name: "options", Prompt: "Options? (optional, e.g. interval=10m keywords=42,available notify=both)", Placeholder: "interval=10m"},
		},
	}
}

func (s *addSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	rawURL := strings.TrimSpace(input.Args["url"])
	options := strings.TrimSpace(input.Args["options"])
	if rawURL == "" {
		// Slash-command fallback: first whitespace-separated token is URL,
		// rest is options. Format:
		//   /visual_watch_add https://x name=foo interval=5m
		text := stripCommandPrefix(input.RawText, []string{
			"/visual_watch_add", "/visual watch add", "visual_watch_add",
			"visual watch add", "watch page", "watch url visually",
		})
		fields := strings.Fields(text)
		if len(fields) > 0 {
			rawURL = fields[0]
			if len(fields) > 1 && options == "" {
				options = strings.Join(fields[1:], " ")
			}
		}
	}
	if rawURL == "" {
		return skills.Result{}, fmt.Errorf("%w: url is required", skills.ErrInvalidArguments)
	}

	defaults := s.service.Defaults()
	watch := models.VisualWatch{
		TelegramUserID:   input.UserID,
		TelegramChatID:   input.ChatID,
		URL:              rawURL,
		Interval:         defaults.DefaultInterval,
		DiffThreshold:    defaults.DefaultThreshold,
		Cooldown:         defaults.DefaultCooldown,
		NotifyOnChange:   true,
		NotifyOnKeywords: false,
		Enabled:          true,
	}
	if name := strings.TrimSpace(input.Args["name"]); name != "" {
		watch.Name = name
	}
	if err := applyOptions(&watch, options); err != nil {
		return skills.Result{}, err
	}
	if watch.Name == "" {
		watch.Name = deriveName(watch.URL)
	}
	if watch.NotifyOnKeywords && len(watch.Keywords) == 0 {
		return skills.Result{}, fmt.Errorf("%w: notify=keywords requires keywords=<...>", skills.ErrInvalidArguments)
	}

	created, err := s.repository.CreateVisualWatch(ctx, watch)
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("Visual watch #%d added: %s", created.ID, created.Name),
		fmt.Sprintf("URL: %s", created.URL),
		fmt.Sprintf("Interval: %s, threshold: %.0f%%, cooldown: %s",
			formatDuration(created.Interval),
			created.DiffThreshold*100,
			formatDuration(created.Cooldown),
		),
		fmt.Sprintf("Notify: %s", notifyDescription(created)),
	}
	if len(created.Keywords) > 0 {
		lines = append(lines, "Keywords: "+strings.Join(created.Keywords, ", "))
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type listSkill struct {
	repository storage.Repository
}

func newListSkill(repository storage.Repository) skills.Skill {
	return &listSkill{repository: repository}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "visual_watch_list",
		Group:       skills.GroupVisualWatch,
		Description: "List visual watches configured for the current chat.",
		Aliases:     []string{"visual watch list", "list visual watches"},
		Usage:       "/visual_watch_list",
	}
}

func (s *listSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	watches, err := s.repository.ListVisualWatches(ctx, storage.VisualWatchListOptions{ChatID: input.ChatID})
	if err != nil {
		return skills.Result{}, err
	}
	if len(watches) == 0 {
		return skills.Result{Text: "No visual watches configured. Use /visual_watch_add to create one."}, nil
	}

	lines := []string{fmt.Sprintf("Visual watches (%d):", len(watches))}
	for _, watch := range watches {
		status := "enabled"
		if !watch.Enabled {
			status = "disabled"
		}
		summary := fmt.Sprintf("#%d %s — %s [%s] every %s, diff>=%.0f%%",
			watch.ID,
			watch.Name,
			watch.URL,
			status,
			formatDuration(watch.Interval),
			watch.DiffThreshold*100,
		)
		if len(watch.Keywords) > 0 {
			summary += " · keywords: " + strings.Join(watch.Keywords, ", ")
		}
		if !watch.LastCheckedAt.IsZero() {
			summary += fmt.Sprintf(" · last checked %s ago", time.Since(watch.LastCheckedAt).Round(time.Second))
		}
		lines = append(lines, summary)
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type removeSkill struct {
	repository storage.Repository
}

func newRemoveSkill(repository storage.Repository) skills.Skill {
	return &removeSkill{repository: repository}
}

func (s *removeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "visual_watch_remove",
		Group:       skills.GroupVisualWatch,
		Description: "Delete a visual watch by id.",
		Aliases:     []string{"visual watch remove", "delete visual watch"},
		Usage:       "/visual_watch_remove <id>",
		Mutating:    true,
	}
}

func (s *removeSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "id", Prompt: "Visual watch id?", Placeholder: "1"},
		},
	}
}

func (s *removeSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	idText := strings.TrimSpace(input.Args["id"])
	if idText == "" {
		idText = stripCommandPrefix(input.RawText, []string{
			"/visual_watch_remove", "/visual watch remove", "visual_watch_remove",
			"visual watch remove", "delete visual watch",
		})
	}
	id, err := parseID(idText)
	if err != nil {
		return skills.Result{}, err
	}
	watch, ok, err := s.repository.GetVisualWatch(ctx, id)
	if err != nil {
		return skills.Result{}, err
	}
	if !ok || watch.TelegramChatID != input.ChatID {
		return skills.Result{}, fmt.Errorf("%w: visual_watch #%d", skills.ErrNotFound, id)
	}
	if err := s.repository.DeleteVisualWatch(ctx, id); err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: fmt.Sprintf("Removed visual watch #%d (%s)", watch.ID, watch.Name)}, nil
}

type testSkill struct {
	repository storage.Repository
	service    Service
}

func newTestSkill(repository storage.Repository, service Service) skills.Skill {
	return &testSkill{repository: repository, service: service}
}

func (s *testSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "visual_watch_test",
		Group:       skills.GroupVisualWatch,
		Description: "Run a visual watch immediately and reply with the diff result.",
		Aliases:     []string{"visual watch test", "run visual watch"},
		Usage:       "/visual_watch_test <id>",
		Mutating:    true,
	}
}

func (s *testSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "id", Prompt: "Visual watch id?", Placeholder: "1"},
		},
	}
}

func (s *testSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	idText := strings.TrimSpace(input.Args["id"])
	if idText == "" {
		idText = stripCommandPrefix(input.RawText, []string{
			"/visual_watch_test", "/visual watch test", "visual_watch_test",
			"visual watch test", "run visual watch",
		})
	}
	id, err := parseID(idText)
	if err != nil {
		return skills.Result{}, err
	}
	watch, ok, err := s.repository.GetVisualWatch(ctx, id)
	if err != nil {
		return skills.Result{}, err
	}
	if !ok || watch.TelegramChatID != input.ChatID {
		return skills.Result{}, fmt.Errorf("%w: visual_watch #%d", skills.ErrNotFound, id)
	}

	result, err := s.service.Evaluate(ctx, id)
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("Visual watch #%d (%s)", result.WatchID, watch.Name),
		fmt.Sprintf("URL: %s", result.URL),
	}
	if result.NewBaseline {
		lines = append(lines, "Baseline initialized — no diff this run.")
	} else if result.Diff.TotalCells > 0 {
		lines = append(lines, fmt.Sprintf("Diff: %.1f%% changed (threshold %.0f%%)",
			result.Diff.ChangedFraction()*100,
			watch.DiffThreshold*100,
		))
	}
	if len(result.KeywordsMatched) > 0 {
		lines = append(lines, "Keywords seen: "+strings.Join(result.KeywordsMatched, ", "))
	}
	if result.Notified {
		lines = append(lines, "Alert was sent.")
	}
	if result.OCRError != "" {
		lines = append(lines, "OCR warning: "+result.OCRError)
	}

	out := skills.Result{Text: strings.Join(lines, "\n")}
	if path := strings.TrimSpace(result.ScreenshotPath); path != "" {
		out.Attachments = []skills.Attachment{{
			Path:    path,
			Caption: watch.Name,
			Kind:    skills.AttachmentPhoto,
		}}
	}
	return out, nil
}

func parseID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: id is required", skills.ErrInvalidArguments)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: invalid id %q", skills.ErrInvalidArguments, value)
	}
	return id, nil
}

func applyOptions(watch *models.VisualWatch, options string) error {
	options = strings.TrimSpace(options)
	if options == "" {
		return nil
	}
	tokens, err := tokenizeOptions(options)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		key := strings.ToLower(strings.TrimSpace(token.key))
		value := strings.TrimSpace(token.value)
		switch key {
		case "name":
			watch.Name = value
		case "interval":
			interval, err := time.ParseDuration(value)
			if err != nil || interval <= 0 {
				return fmt.Errorf("%w: invalid interval %q", skills.ErrInvalidArguments, value)
			}
			watch.Interval = interval
		case "threshold":
			threshold, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
			if err != nil {
				return fmt.Errorf("%w: invalid threshold %q", skills.ErrInvalidArguments, value)
			}
			if threshold > 1 {
				threshold /= 100
			}
			if threshold <= 0 || threshold >= 1 {
				return fmt.Errorf("%w: threshold must be between 0 and 1", skills.ErrInvalidArguments)
			}
			watch.DiffThreshold = threshold
		case "cooldown":
			cooldown, err := time.ParseDuration(value)
			if err != nil || cooldown < 0 {
				return fmt.Errorf("%w: invalid cooldown %q", skills.ErrInvalidArguments, value)
			}
			watch.Cooldown = cooldown
		case "keywords":
			watch.Keywords = parseKeywords(value)
		case "notify":
			switch strings.ToLower(value) {
			case "change":
				watch.NotifyOnChange = true
				watch.NotifyOnKeywords = false
			case "keywords":
				watch.NotifyOnChange = false
				watch.NotifyOnKeywords = true
			case "both":
				watch.NotifyOnChange = true
				watch.NotifyOnKeywords = true
			default:
				return fmt.Errorf("%w: invalid notify mode %q (change|keywords|both)", skills.ErrInvalidArguments, value)
			}
		default:
			return fmt.Errorf("%w: unsupported option %q", skills.ErrInvalidArguments, key)
		}
	}
	return nil
}

type optionToken struct {
	key   string
	value string
}

// tokenizeOptions parses a "key=value key=value" string where values may
// contain spaces or commas (e.g. `keywords=42, in stock`). Tokens start at
// each whitespace-separated <word>= prefix; everything after the `=` and up
// to the next prefix is treated as the value.
func tokenizeOptions(options string) ([]optionToken, error) {
	options = strings.TrimSpace(options)
	if options == "" {
		return nil, nil
	}
	fields := strings.Fields(options)
	var tokens []optionToken
	for _, field := range fields {
		key, value, hasEquals := strings.Cut(field, "=")
		if hasEquals {
			tokens = append(tokens, optionToken{
				key:   key,
				value: value,
			})
			continue
		}
		if len(tokens) == 0 {
			return nil, fmt.Errorf("%w: option %q must start with key=value", skills.ErrInvalidArguments, field)
		}
		tokens[len(tokens)-1].value = strings.TrimSpace(tokens[len(tokens)-1].value + " " + field)
	}
	return tokens, nil
}

func parseKeywords(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}
	return result
}

func deriveName(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "watch"
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "watch"
	}
	if path := strings.Trim(parsed.Path, "/"); path != "" {
		return host + "/" + path
	}
	return host
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	return d.String()
}

func notifyDescription(watch models.VisualWatch) string {
	switch {
	case watch.NotifyOnChange && watch.NotifyOnKeywords:
		return "visual change + keywords"
	case watch.NotifyOnChange:
		return "visual change"
	case watch.NotifyOnKeywords:
		return "keywords"
	default:
		return "disabled"
	}
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

// Compile-time guard that errors.Is/As paths still compile against skills
// errors after option parsing.
var _ = errors.Is
