package vision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openlight/internal/imagediff"
	"openlight/internal/skills"
)

// Provider abstracts a vision-language backend. Implementations should accept
// an image on disk plus a free-form prompt and return a short textual answer.
type Provider interface {
	AnalyzeImage(ctx context.Context, imagePath, prompt string) (string, error)
}

// Manager exposes vision capabilities to skills with consistent path,
// extension, and size validation.
type Manager interface {
	Enabled() bool
	Analyze(ctx context.Context, imagePath, prompt string) (AnalyzeResult, error)
	Compare(ctx context.Context, baselinePath, candidatePath, prompt string) (CompareResult, error)
}

type AnalyzeResult struct {
	Path        string
	Description string
	Provider    string
	Model       string
}

type CompareResult struct {
	BaselinePath  string
	CandidatePath string
	Diff          imagediff.Result
	Description   string
	Provider      string
	Model         string
}

type Options struct {
	Enabled          bool
	Provider         Provider
	ProviderName     string
	ModelName        string
	DefaultPrompt    string
	MaxImageSizeMB   int
	Timeout          time.Duration
	MaxResponseChars int
}

type LocalManager struct {
	opts Options
}

func NewLocalManager(opts Options) *LocalManager {
	if opts.MaxResponseChars <= 0 {
		opts.MaxResponseChars = 1500
	}
	if opts.MaxImageSizeMB <= 0 {
		opts.MaxImageSizeMB = 10
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(opts.DefaultPrompt) == "" {
		opts.DefaultPrompt = "Describe this image in concise plain English."
	}
	return &LocalManager{opts: opts}
}

func (m *LocalManager) Enabled() bool {
	return m != nil && m.opts.Enabled
}

func (m *LocalManager) Analyze(ctx context.Context, imagePath, prompt string) (AnalyzeResult, error) {
	if err := m.assertReady(); err != nil {
		return AnalyzeResult{}, err
	}
	resolved, err := m.validateImagePath(imagePath)
	if err != nil {
		return AnalyzeResult{}, err
	}
	prompt = m.resolvePrompt(prompt)

	ctx, cancel := m.withTimeout(ctx)
	defer cancel()

	answer, err := m.opts.Provider.AnalyzeImage(ctx, resolved, prompt)
	if err != nil {
		return AnalyzeResult{}, mapProviderError(err)
	}
	return AnalyzeResult{
		Path:        resolved,
		Description: truncate(strings.TrimSpace(answer), m.opts.MaxResponseChars),
		Provider:    m.opts.ProviderName,
		Model:       m.opts.ModelName,
	}, nil
}

func (m *LocalManager) Compare(ctx context.Context, baselinePath, candidatePath, prompt string) (CompareResult, error) {
	if err := m.assertReady(); err != nil {
		return CompareResult{}, err
	}
	baseline, err := m.validateImagePath(baselinePath)
	if err != nil {
		return CompareResult{}, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := m.validateImagePath(candidatePath)
	if err != nil {
		return CompareResult{}, fmt.Errorf("candidate: %w", err)
	}

	diff, err := imagediff.Compare(baseline, candidate, imagediff.Options{})
	if err != nil {
		return CompareResult{}, skills.NewUserError(skills.ErrUnavailable, "image diff failed: "+err.Error())
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Compare these two images. Describe any meaningful differences in one short paragraph."
	}

	ctx, cancel := m.withTimeout(ctx)
	defer cancel()

	answer, err := m.opts.Provider.AnalyzeImage(ctx, candidate, prompt+" Focus on what changed compared to the prior version.")
	if err != nil {
		return CompareResult{}, mapProviderError(err)
	}
	return CompareResult{
		BaselinePath:  baseline,
		CandidatePath: candidate,
		Diff:          diff,
		Description:   truncate(strings.TrimSpace(answer), m.opts.MaxResponseChars),
		Provider:      m.opts.ProviderName,
		Model:         m.opts.ModelName,
	}, nil
}

func (m *LocalManager) assertReady() error {
	if !m.Enabled() {
		return skills.NewUserError(skills.ErrUnavailable, "vision is disabled")
	}
	if m.opts.Provider == nil {
		return skills.NewUserError(skills.ErrUnavailable, "vision provider is not configured")
	}
	return nil
}

func (m *LocalManager) resolvePrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return m.opts.DefaultPrompt
	}
	return prompt
}

func (m *LocalManager) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.opts.Timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= m.opts.Timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, m.opts.Timeout)
}

func (m *LocalManager) validateImagePath(rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("%w: image path is required", skills.ErrInvalidArguments)
	}
	expanded := expandHome(rawPath)
	if !imagediff.SupportedExt(expanded) {
		return "", fmt.Errorf("%w: unsupported image format (use png, jpg, jpeg, or gif)", skills.ErrInvalidArguments)
	}
	info, err := os.Stat(expanded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: image not found", skills.ErrNotFound)
		}
		return "", fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: path is a directory", skills.ErrInvalidArguments)
	}
	maxBytes := int64(m.opts.MaxImageSizeMB) * 1024 * 1024
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", fmt.Errorf("%w: image is larger than %d MB", skills.ErrInvalidArguments, m.opts.MaxImageSizeMB)
	}
	return expanded, nil
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
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

func mapProviderError(err error) error {
	if err == nil {
		return nil
	}
	var ufe skills.UserFacingError
	if errors.As(err, &ufe) {
		return err
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "vision provider failed"
	}
	const limit = 400
	if runes := []rune(msg); len(runes) > limit {
		msg = string(runes[:limit]) + "…"
	}
	return skills.NewUserError(skills.ErrUnavailable, "vision provider failed: "+msg)
}
