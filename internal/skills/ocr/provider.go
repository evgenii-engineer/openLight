package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProviderConfig describes how to construct an OCR Provider implementation.
type ProviderConfig struct {
	Provider   string
	BinaryPath string
}

// BuildProvider returns a Provider for the configured backend, or nil when
// disabled.
func BuildProvider(cfg ProviderConfig) (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "", "none":
		return nil, nil
	case "tesseract":
		return newTesseractProvider(cfg)
	case "apple_vision", "apple", "macos_vision":
		return newAppleVisionProvider(cfg)
	default:
		return nil, fmt.Errorf("ocr: unsupported provider %q", provider)
	}
}

type tesseractProvider struct {
	binary string
}

func newTesseractProvider(cfg ProviderConfig) (Provider, error) {
	binary := strings.TrimSpace(cfg.BinaryPath)
	if binary == "" {
		binary = "tesseract"
	}
	return &tesseractProvider{binary: binary}, nil
}

func (p *tesseractProvider) Name() string {
	return "tesseract"
}

func (p *tesseractProvider) ExtractText(ctx context.Context, imagePath string, languages []string) (string, error) {
	args := []string{imagePath, "stdout"}
	if lang := joinLanguages(languages); lang != "" {
		args = append(args, "-l", lang)
	}
	cmd := exec.CommandContext(ctx, p.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("tesseract timed out: %w", ctx.Err())
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("tesseract not available at %q: %w", p.binary, err)
		}
		errOutput := strings.TrimSpace(stderr.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		return "", fmt.Errorf("tesseract failed: %s", errOutput)
	}
	return stdout.String(), nil
}

// appleVisionProvider runs an external helper that wraps the macOS Vision
// framework (the framework itself is not callable from pure Go). The helper
// receives a single image path as its first argument and prints recognized
// text to stdout. A common helper is `shortcuts run "OCR" --input-path <path>`,
// or a small Swift CLI installed at /usr/local/bin/openlight-vision-ocr.
type appleVisionProvider struct {
	binary string
}

func newAppleVisionProvider(cfg ProviderConfig) (Provider, error) {
	binary := strings.TrimSpace(cfg.BinaryPath)
	if binary == "" {
		return nil, errors.New("ocr.binary_path is required when ocr.provider is apple_vision")
	}
	return &appleVisionProvider{binary: binary}, nil
}

func (p *appleVisionProvider) Name() string {
	return "apple_vision"
}

func (p *appleVisionProvider) ExtractText(ctx context.Context, imagePath string, _ []string) (string, error) {
	cmd := exec.CommandContext(ctx, p.binary, imagePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("apple vision helper timed out: %w", ctx.Err())
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("apple vision helper not available at %q: %w", p.binary, err)
		}
		errOutput := strings.TrimSpace(stderr.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		return "", fmt.Errorf("apple vision helper failed: %s", errOutput)
	}
	return stdout.String(), nil
}

func joinLanguages(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, "+")
}

// helperBinaryHint returns a friendly description for missing-binary errors.
func helperBinaryHint(binary string) string {
	if binary == "" {
		return ""
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return binary
	}
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return binary
}

var _ = helperBinaryHint // reserved for future error formatting
