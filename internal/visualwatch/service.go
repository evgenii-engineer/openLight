// Package visualwatch periodically captures screenshots of allowlisted URLs,
// compares each capture against the stored baseline, and notifies the
// owning chat when meaningful visual changes appear or when configured
// keywords show up in the page (via OCR or HTML text fallback). It runs
// alongside the metric/service watch engine but stays independent: the
// targets, polling cadence, baselines, and alerts have their own model.
package visualwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"openlight/internal/imagediff"
	"openlight/internal/models"
	"openlight/internal/skills"
	"openlight/internal/skills/browser"
	"openlight/internal/skills/ocr"
	"openlight/internal/storage"
)

// Notifier delivers a visual-watch alert to the user. Implementations should
// send the supplied screenshot as a photo when possible.
type Notifier interface {
	SendVisualAlert(ctx context.Context, chatID int64, text, screenshotPath string) error
}

// BrowserManager is the subset of the browser skill manager that
// visual-watch needs. Exposed so tests can stub it.
type BrowserManager interface {
	Enabled() bool
	Screenshot(ctx context.Context, rawURL string) (browser.Response, error)
	Text(ctx context.Context, rawURL string) (browser.Response, error)
}

// OCRManager is the subset of the OCR skill manager visual-watch may use to
// match keywords against rendered text. Optional.
type OCRManager interface {
	Enabled() bool
	Extract(ctx context.Context, imagePath string) (ocr.Result, error)
}

// Options tunes the polling loop.
type Options struct {
	PollInterval     time.Duration
	BaselinesDir     string
	DefaultInterval  time.Duration
	DefaultThreshold float64
	DefaultCooldown  time.Duration
	RequestTimeout   time.Duration
}

// Service is the visual-watch background poller.
type Service struct {
	repository storage.Repository
	browser    BrowserManager
	ocr        OCRManager
	notifier   Notifier
	logger     *slog.Logger
	opts       Options
}

// NewService wires a new poller. The notifier may be nil at construction time
// and set later via SetNotifier (the agent does this so it can pass the
// Telegram bot only after both objects exist).
func NewService(
	repository storage.Repository,
	browserManager BrowserManager,
	ocrManager OCRManager,
	notifier Notifier,
	logger *slog.Logger,
	opts Options,
) *Service {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}
	if opts.DefaultInterval <= 0 {
		opts.DefaultInterval = 15 * time.Minute
	}
	if opts.DefaultThreshold <= 0 {
		opts.DefaultThreshold = 0.15
	}
	if opts.DefaultCooldown <= 0 {
		opts.DefaultCooldown = 30 * time.Minute
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 60 * time.Second
	}
	return &Service{
		repository: repository,
		browser:    browserManager,
		ocr:        ocrManager,
		notifier:   notifier,
		logger:     logger,
		opts:       opts,
	}
}

// SetNotifier swaps the notifier used for outbound alerts. Called by the
// agent runtime after the Telegram bot is constructed.
func (s *Service) SetNotifier(notifier Notifier) {
	s.notifier = notifier
}

// Defaults returns the per-watch defaults used by the add skill.
func (s *Service) Defaults() Options {
	return s.opts
}

// BaselinesDir returns the directory where baselines are stored. Created on
// demand by Capture.
func (s *Service) BaselinesDir() string {
	if strings.TrimSpace(s.opts.BaselinesDir) == "" {
		return "./data/visual-watch"
	}
	return s.opts.BaselinesDir
}

// Run drives the poller until the context is cancelled.
func (s *Service) Run(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()

	// First pass after start so newly-added watches don't sit idle for
	// the whole interval.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	watches, err := s.repository.ListVisualWatches(ctx, storage.VisualWatchListOptions{EnabledOnly: true})
	if err != nil {
		s.logError("list visual watches", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, watch := range watches {
		if !shouldCheck(watch, now) {
			continue
		}
		s.evaluate(ctx, watch)
	}
}

func shouldCheck(watch models.VisualWatch, now time.Time) bool {
	if !watch.Enabled {
		return false
	}
	if watch.LastCheckedAt.IsZero() {
		return true
	}
	if watch.Interval <= 0 {
		return true
	}
	return now.Sub(watch.LastCheckedAt) >= watch.Interval
}

// Evaluate runs a single watch immediately (used by the test/run-now skills).
func (s *Service) Evaluate(ctx context.Context, id int64) (EvaluateResult, error) {
	watch, ok, err := s.repository.GetVisualWatch(ctx, id)
	if err != nil {
		return EvaluateResult{}, err
	}
	if !ok {
		return EvaluateResult{}, fmt.Errorf("%w: visual_watch #%d", skills.ErrNotFound, id)
	}
	return s.evaluate(ctx, watch), nil
}

// EvaluateResult exposes what an evaluation observed so callers (skills)
// can show a one-shot status in the chat.
type EvaluateResult struct {
	WatchID         int64
	URL             string
	ScreenshotPath  string
	BaselinePath    string
	Diff            imagediff.Result
	Changed         bool
	NewBaseline     bool
	KeywordsMatched []string
	Notified        bool
	OCRError        string
}

func (s *Service) evaluate(ctx context.Context, watch models.VisualWatch) EvaluateResult {
	if s.browser == nil || !s.browser.Enabled() {
		s.logWarn("visual watch skipped: browser disabled", "watch_id", watch.ID)
		return EvaluateResult{WatchID: watch.ID, URL: watch.URL}
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, s.opts.RequestTimeout)
	defer cancel()

	captured, err := s.browser.Screenshot(ctxWithTimeout, watch.URL)
	if err != nil {
		s.logError("visual watch screenshot failed", "watch_id", watch.ID, "url", watch.URL, "error", err)
		return EvaluateResult{WatchID: watch.ID, URL: watch.URL}
	}

	now := time.Now().UTC()
	result := EvaluateResult{
		WatchID:        watch.ID,
		URL:            watch.URL,
		ScreenshotPath: captured.ScreenshotPath,
		BaselinePath:   watch.BaselinePath,
	}

	previousBaseline := watch.BaselinePath
	if previousBaseline == "" {
		// First run: promote this capture to the baseline and keep a copy
		// in the baselines dir so cleanup of the artifacts dir doesn't
		// invalidate it.
		stored, err := s.persistBaseline(watch, captured.ScreenshotPath)
		if err != nil {
			s.logError("persist visual watch baseline", "watch_id", watch.ID, "error", err)
		} else {
			watch.BaselinePath = stored
			result.BaselinePath = stored
			result.NewBaseline = true
		}
	} else if imagediff.SupportedExt(previousBaseline) && imagediff.SupportedExt(captured.ScreenshotPath) {
		diff, err := imagediff.Compare(previousBaseline, captured.ScreenshotPath, imagediff.Options{})
		if err != nil {
			s.logError("visual watch diff", "watch_id", watch.ID, "error", err)
		} else {
			result.Diff = diff
			watch.LastChangedFraction = diff.ChangedFraction()
			if diff.Significantly(watch.DiffThreshold) {
				result.Changed = true
				watch.LastChangedAt = now
				stored, err := s.persistBaseline(watch, captured.ScreenshotPath)
				if err != nil {
					s.logError("persist visual watch baseline", "watch_id", watch.ID, "error", err)
				} else {
					watch.BaselinePath = stored
					result.BaselinePath = stored
				}
			}
		}
	}

	matchedKeywords, ocrErr := s.matchKeywords(ctxWithTimeout, watch, captured.ScreenshotPath)
	if ocrErr != nil {
		result.OCRError = ocrErr.Error()
	}
	result.KeywordsMatched = matchedKeywords
	watch.LastKeywordsSeen = matchedKeywords
	watch.LastCheckedAt = now
	watch.LastScreenshotPath = captured.ScreenshotPath

	if s.shouldNotify(watch, result, now) {
		result.Notified = true
		watch.LastAlertedAt = now
		s.sendAlert(ctxWithTimeout, watch, result, captured.Title)
	}

	if err := s.repository.UpdateVisualWatch(ctx, watch); err != nil {
		s.logError("update visual watch state", "watch_id", watch.ID, "error", err)
	}
	return result
}

func (s *Service) shouldNotify(watch models.VisualWatch, result EvaluateResult, now time.Time) bool {
	if s.notifier == nil {
		return false
	}
	if !watch.LastAlertedAt.IsZero() && watch.Cooldown > 0 && now.Sub(watch.LastAlertedAt) < watch.Cooldown {
		return false
	}
	if watch.NotifyOnChange && result.Changed {
		return true
	}
	if watch.NotifyOnKeywords && len(result.KeywordsMatched) > 0 {
		// Require freshly-seen keywords (i.e. they weren't seen on the previous
		// successful check) so we don't spam the chat for the same listing.
		if !sameKeywordSet(watch.LastKeywordsSeen, result.KeywordsMatched) || result.NewBaseline {
			return true
		}
	}
	return false
}

func (s *Service) sendAlert(ctx context.Context, watch models.VisualWatch, result EvaluateResult, title string) {
	if s.notifier == nil {
		return
	}
	lines := []string{
		fmt.Sprintf("Visual watch %q triggered", watch.Name),
		fmt.Sprintf("URL: %s", watch.URL),
	}
	if title := strings.TrimSpace(title); title != "" {
		lines = append(lines, fmt.Sprintf("Title: %s", title))
	}
	if result.Changed {
		lines = append(lines, fmt.Sprintf("Pixel diff: %.1f%% (threshold %.0f%%)",
			result.Diff.ChangedFraction()*100,
			watch.DiffThreshold*100,
		))
	}
	if len(result.KeywordsMatched) > 0 {
		lines = append(lines, "Keywords seen: "+strings.Join(result.KeywordsMatched, ", "))
	}
	if err := s.notifier.SendVisualAlert(ctx, watch.TelegramChatID, strings.Join(lines, "\n"), result.ScreenshotPath); err != nil {
		s.logError("send visual alert", "watch_id", watch.ID, "error", err)
	}
}

func (s *Service) matchKeywords(ctx context.Context, watch models.VisualWatch, screenshotPath string) ([]string, error) {
	if len(watch.Keywords) == 0 {
		return nil, nil
	}
	var haystack strings.Builder

	if s.ocr != nil && s.ocr.Enabled() && screenshotPath != "" {
		ocrResult, err := s.ocr.Extract(ctx, screenshotPath)
		if err != nil {
			// Fall back to HTML text below; report the error but don't fail
			// the whole watch.
			s.logWarn("visual watch ocr failed, falling back to text", "watch_id", watch.ID, "error", err)
		} else {
			haystack.WriteString(ocrResult.Text)
		}
	}

	if haystack.Len() == 0 && s.browser != nil {
		text, err := s.browser.Text(ctx, watch.URL)
		if err == nil {
			haystack.WriteString(text.TextPreview)
		}
	}

	if haystack.Len() == 0 {
		return nil, nil
	}

	lower := strings.ToLower(haystack.String())
	matched := make([]string, 0, len(watch.Keywords))
	seen := make(map[string]struct{}, len(watch.Keywords))
	for _, keyword := range watch.Keywords {
		needle := strings.ToLower(strings.TrimSpace(keyword))
		if needle == "" {
			continue
		}
		if _, ok := seen[needle]; ok {
			continue
		}
		seen[needle] = struct{}{}
		if strings.Contains(lower, needle) {
			matched = append(matched, keyword)
		}
	}
	sort.Strings(matched)
	return matched, nil
}

func (s *Service) persistBaseline(watch models.VisualWatch, screenshotPath string) (string, error) {
	if strings.TrimSpace(screenshotPath) == "" {
		return "", errors.New("screenshot path is empty")
	}
	dir := s.BaselinesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create baselines dir: %w", err)
	}
	host := "page"
	if parsed, err := url.Parse(watch.URL); err == nil {
		if h := strings.TrimSpace(parsed.Hostname()); h != "" {
			host = h
		}
	}
	target := filepath.Join(dir, fmt.Sprintf("watch-%d-%s%s", watch.ID, sanitize(host), filepath.Ext(screenshotPath)))
	if err := copyFile(screenshotPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy bytes: %w", err)
	}
	return nil
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func sameKeywordSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := make(map[string]struct{}, len(a))
	for _, value := range a {
		left[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range b {
		if _, ok := left[strings.ToLower(strings.TrimSpace(value))]; !ok {
			return false
		}
	}
	return true
}

func (s *Service) logError(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Error(msg, args...)
	}
}

func (s *Service) logWarn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}
