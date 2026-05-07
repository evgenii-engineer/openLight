package ocr

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"openlight/internal/skills"
)

type stubProvider struct {
	called   bool
	imgPath  string
	langs    []string
	response string
	err      error
}

func (p *stubProvider) Name() string { return "stub" }

func (p *stubProvider) ExtractText(_ context.Context, imagePath string, languages []string) (string, error) {
	p.called = true
	p.imgPath = imagePath
	p.langs = languages
	return p.response, p.err
}

func writeSolidPNG(t *testing.T, path string) {
	t.Helper()
	const w, h = 8, 8
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestManagerExtractRejectsWhenDisabled(t *testing.T) {
	t.Parallel()
	manager := NewLocalManager(Options{})
	if manager.Enabled() {
		t.Fatalf("manager unexpectedly enabled")
	}
	_, err := manager.Extract(context.Background(), "missing.png")
	if err == nil || !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestManagerExtractRunsProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.png")
	writeSolidPNG(t, path)

	provider := &stubProvider{response: "hello world\n"}
	manager := NewLocalManager(Options{
		Enabled:        true,
		Provider:       provider,
		Languages:      []string{"eng"},
		MaxImageSizeMB: 1,
	})
	result, err := manager.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if !provider.called {
		t.Fatalf("provider not called")
	}
	if result.Text != "hello world" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Provider != "stub" {
		t.Fatalf("unexpected provider: %q", result.Provider)
	}
	if len(provider.langs) != 1 || provider.langs[0] != "eng" {
		t.Fatalf("languages not forwarded, got %v", provider.langs)
	}
}

func TestManagerExtractRejectsMissingFile(t *testing.T) {
	t.Parallel()
	manager := NewLocalManager(Options{Enabled: true, Provider: &stubProvider{}, MaxImageSizeMB: 1})
	_, err := manager.Extract(context.Background(), "/nope.png")
	if err == nil || !errors.Is(err, skills.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
