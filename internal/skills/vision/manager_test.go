package vision

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

type stubProvider struct {
	calls    int
	prompt   string
	imgPath  string
	response string
	err      error
}

func (p *stubProvider) AnalyzeImage(_ context.Context, imagePath, prompt string) (string, error) {
	p.calls++
	p.imgPath = imagePath
	p.prompt = prompt
	return p.response, p.err
}

func writeSolidPNG(t *testing.T, dir, name string, c color.RGBA) string {
	t.Helper()
	const w, h = 32, 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	return path
}

func TestManagerAnalyzeRejectsWhenDisabled(t *testing.T) {
	t.Parallel()
	manager := NewLocalManager(Options{})
	if manager.Enabled() {
		t.Fatalf("manager unexpectedly enabled")
	}
	_, err := manager.Analyze(context.Background(), "missing.png", "")
	if err == nil || !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestManagerAnalyzeRejectsUnsupportedExt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.bmp")
	if err := os.WriteFile(path, []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	manager := NewLocalManager(Options{Enabled: true, Provider: &stubProvider{response: "ok"}, MaxImageSizeMB: 1})
	_, err := manager.Analyze(context.Background(), path, "describe")
	if err == nil || !errors.Is(err, skills.ErrInvalidArguments) {
		t.Fatalf("expected invalid arguments, got %v", err)
	}
}

func TestManagerAnalyzeUsesDefaultPrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSolidPNG(t, dir, "frame.png", color.RGBA{200, 200, 200, 255})

	provider := &stubProvider{response: "  A grey square.  "}
	manager := NewLocalManager(Options{
		Enabled:        true,
		Provider:       provider,
		ProviderName:   "stub",
		ModelName:      "test-model",
		DefaultPrompt:  "Describe the image.",
		MaxImageSizeMB: 1,
	})

	result, err := manager.Analyze(context.Background(), path, "")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider called once, got %d", provider.calls)
	}
	if provider.prompt != "Describe the image." {
		t.Fatalf("default prompt not used, got %q", provider.prompt)
	}
	if result.Description != "A grey square." {
		t.Fatalf("expected trimmed description, got %q", result.Description)
	}
	if !strings.HasSuffix(result.Path, "frame.png") {
		t.Fatalf("unexpected path: %s", result.Path)
	}
}

func TestStripCommandPrefix(t *testing.T) {
	t.Parallel()
	prefixes := []string{
		"/vision_analyze", "/vision analyze", "vision_analyze", "vision analyze",
		"describe image", "analyze image", "analyze screenshot",
	}
	cases := map[string]string{
		"/vision_analyze ./img.png":                 "./img.png",
		"/vision analyze ./img.png":                 "./img.png",
		"vision_analyze ./img.png":                  "./img.png",
		"analyze image ./img.png":                   "./img.png",
		"/vision_analyze :: ./img.png":              "./img.png",
		"  /vision_analyze   spaced.png  ":         "spaced.png",
		"/vision_analyze ./img.png :: focus":        "./img.png :: focus",
		"unrelated text":                            "unrelated text",
		"":                                          "",
	}
	for input, want := range cases {
		if got := stripCommandPrefix(input, prefixes); got != want {
			t.Fatalf("stripCommandPrefix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSplitPathPrompt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in         string
		path       string
		prompt     string
	}{
		{"./img.png", "./img.png", ""},
		{"./img.png :: focus on logo", "./img.png", "focus on logo"},
		{"::leading separator", "", "leading separator"},
		{"  ", "", ""},
	}
	for _, tc := range cases {
		path, prompt := splitPathPrompt(tc.in)
		if path != tc.path || prompt != tc.prompt {
			t.Fatalf("splitPathPrompt(%q) = (%q, %q), want (%q, %q)", tc.in, path, prompt, tc.path, tc.prompt)
		}
	}
}

func TestSplitTwoPathsPrompt(t *testing.T) {
	t.Parallel()
	paths, prompt := splitTwoPathsPrompt("./a.png ./b.png")
	if len(paths) != 2 || paths[0] != "./a.png" || paths[1] != "./b.png" || prompt != "" {
		t.Fatalf("two-paths: paths=%v prompt=%q", paths, prompt)
	}
	paths, prompt = splitTwoPathsPrompt("./a.png ./b.png :: focus on diff")
	if len(paths) != 2 || paths[0] != "./a.png" || paths[1] != "./b.png" || prompt != "focus on diff" {
		t.Fatalf("with-prompt: paths=%v prompt=%q", paths, prompt)
	}
}

func TestManagerCompareRunsDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	baseline := writeSolidPNG(t, dir, "base.png", white)
	candidate := writeSolidPNG(t, dir, "cand.png", black)

	provider := &stubProvider{response: "Image is now dark."}
	manager := NewLocalManager(Options{
		Enabled:        true,
		Provider:       provider,
		MaxImageSizeMB: 1,
	})

	result, err := manager.Compare(context.Background(), baseline, candidate, "")
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if result.Diff.ChangedCells == 0 {
		t.Fatalf("expected changed cells from black-vs-white comparison")
	}
	if result.Description != "Image is now dark." {
		t.Fatalf("unexpected description: %q", result.Description)
	}
}
