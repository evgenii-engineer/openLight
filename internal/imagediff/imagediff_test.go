package imagediff

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, dir, name string, fill color.RGBA, patch *image.Rectangle, patchColor color.RGBA) string {
	t.Helper()
	const w, h = 64, 64
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	if patch != nil {
		for y := patch.Min.Y; y < patch.Max.Y; y++ {
			for x := patch.Min.X; x < patch.Max.X; x++ {
				img.Set(x, y, patchColor)
			}
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

func TestCompareIdenticalImages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	white := color.RGBA{255, 255, 255, 255}
	a := writePNG(t, dir, "a.png", white, nil, white)
	b := writePNG(t, dir, "b.png", white, nil, white)

	result, err := Compare(a, b, Options{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if result.ChangedCells != 0 {
		t.Fatalf("expected zero changed cells, got %d", result.ChangedCells)
	}
	if result.Significantly(0.15) {
		t.Fatalf("identical images flagged as significantly different")
	}
}

func TestComparePatchTriggersChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	a := writePNG(t, dir, "a.png", white, nil, white)

	patch := image.Rect(0, 0, 32, 32)
	b := writePNG(t, dir, "b.png", white, &patch, black)

	result, err := Compare(a, b, Options{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if result.ChangedCells == 0 {
		t.Fatalf("expected changed cells, got zero")
	}
	if !result.Significantly(0.10) {
		t.Fatalf("a 25%% black patch should trigger change but did not (fraction=%.4f)", result.ChangedFraction())
	}
}

func TestSupportedExt(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"foo.png":      true,
		"foo.PNG":      true,
		"foo.jpg":      true,
		"foo.jpeg":     true,
		"foo.gif":      true,
		"foo.bmp":      false,
		"foo":          false,
	}
	for path, want := range cases {
		if got := SupportedExt(path); got != want {
			t.Fatalf("SupportedExt(%q) = %v, want %v", path, got, want)
		}
	}
}
