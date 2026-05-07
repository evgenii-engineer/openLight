package ocr

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

// Provider extracts text from an image on disk.
type Provider interface {
	ExtractText(ctx context.Context, imagePath string, languages []string) (string, error)
	Name() string
}

// Manager exposes OCR to skills with consistent path and size validation.
type Manager interface {
	Enabled() bool
	Extract(ctx context.Context, imagePath string) (Result, error)
}

type Result struct {
	Path     string
	Text     string
	Provider string
}

type Options struct {
	Enabled        bool
	Provider       Provider
	Languages      []string
	Timeout        time.Duration
	MaxImageSizeMB int
}

type LocalManager struct {
	opts Options
}

func NewLocalManager(opts Options) *LocalManager {
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.MaxImageSizeMB <= 0 {
		opts.MaxImageSizeMB = 10
	}
	return &LocalManager{opts: opts}
}

func (m *LocalManager) Enabled() bool {
	return m != nil && m.opts.Enabled
}

func (m *LocalManager) Extract(ctx context.Context, imagePath string) (Result, error) {
	if !m.Enabled() {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "ocr is disabled")
	}
	if m.opts.Provider == nil {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "ocr provider is not configured")
	}
	resolved, err := m.validateImagePath(imagePath)
	if err != nil {
		return Result{}, err
	}

	ctx, cancel := m.withTimeout(ctx)
	defer cancel()

	text, err := m.opts.Provider.ExtractText(ctx, resolved, m.opts.Languages)
	if err != nil {
		return Result{}, mapProviderError(err)
	}
	return Result{
		Path:     resolved,
		Text:     strings.TrimSpace(text),
		Provider: m.opts.Provider.Name(),
	}, nil
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
		msg = "ocr provider failed"
	}
	const limit = 400
	if runes := []rune(msg); len(runes) > limit {
		msg = string(runes[:limit]) + "…"
	}
	return skills.NewUserError(skills.ErrUnavailable, "ocr provider failed: "+msg)
}
