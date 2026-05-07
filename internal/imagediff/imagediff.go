// Package imagediff provides a small pure-Go pixel-difference utility used by
// vision-related skills and visual monitoring. It downsamples images to a
// fixed grid and compares average luminance per cell, which keeps detection
// stable across small rendering differences (font hinting, color jitter,
// minor anti-aliasing drift) while still flagging structural changes.
package imagediff

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultGridSize     = 32
	defaultDiffThreshold = 0.04
)

// Result describes the outcome of a single image comparison.
type Result struct {
	GridSize        int
	ChangedCells    int
	TotalCells      int
	AverageDelta    float64
	MaxDelta        float64
	Threshold       float64
	BaselineWidth   int
	BaselineHeight  int
	CandidateWidth  int
	CandidateHeight int
}

// ChangedFraction reports how many sampled cells differ above the per-cell
// threshold, normalized into [0, 1].
func (r Result) ChangedFraction() float64 {
	if r.TotalCells == 0 {
		return 0
	}
	return float64(r.ChangedCells) / float64(r.TotalCells)
}

// Significantly returns true when the changed fraction passes the supplied
// alert threshold.
func (r Result) Significantly(alertThreshold float64) bool {
	if alertThreshold <= 0 {
		alertThreshold = 0.15
	}
	return r.ChangedFraction() >= alertThreshold
}

// Options tweaks the diff sensitivity.
type Options struct {
	// GridSize is the number of cells along each axis the images are
	// downsampled into. Defaults to 32 when zero.
	GridSize int
	// CellThreshold is the per-cell luminance delta (0..1) at which a cell
	// counts as changed. Defaults to 0.04 when zero.
	CellThreshold float64
}

// Compare loads two image files and returns a Result describing how much they
// differ. The file extensions must be supported by the standard library
// image decoders (png, jpeg, gif).
func Compare(baselinePath, candidatePath string, opts Options) (Result, error) {
	baselinePath = strings.TrimSpace(baselinePath)
	candidatePath = strings.TrimSpace(candidatePath)
	if baselinePath == "" || candidatePath == "" {
		return Result{}, errors.New("imagediff: baseline and candidate paths are required")
	}
	baseline, err := loadImage(baselinePath)
	if err != nil {
		return Result{}, fmt.Errorf("load baseline: %w", err)
	}
	candidate, err := loadImage(candidatePath)
	if err != nil {
		return Result{}, fmt.Errorf("load candidate: %w", err)
	}

	gridSize := opts.GridSize
	if gridSize <= 0 {
		gridSize = defaultGridSize
	}
	cellThreshold := opts.CellThreshold
	if cellThreshold <= 0 {
		cellThreshold = defaultDiffThreshold
	}

	baselineGrid := luminanceGrid(baseline, gridSize)
	candidateGrid := luminanceGrid(candidate, gridSize)

	totalCells := gridSize * gridSize
	changed := 0
	var sum, max float64
	for i := range baselineGrid {
		delta := math.Abs(baselineGrid[i] - candidateGrid[i])
		sum += delta
		if delta > max {
			max = delta
		}
		if delta >= cellThreshold {
			changed++
		}
	}

	bb := baseline.Bounds()
	cb := candidate.Bounds()

	return Result{
		GridSize:        gridSize,
		ChangedCells:    changed,
		TotalCells:      totalCells,
		AverageDelta:    sum / float64(totalCells),
		MaxDelta:        max,
		Threshold:       cellThreshold,
		BaselineWidth:   bb.Dx(),
		BaselineHeight:  bb.Dy(),
		CandidateWidth:  cb.Dx(),
		CandidateHeight: cb.Dy(),
	}, nil
}

// SupportedExt reports whether the file extension corresponds to an image
// format the diff package can decode.
func SupportedExt(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}

func loadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode image %q: %w", path, err)
	}
	return img, nil
}

func luminanceGrid(img image.Image, gridSize int) []float64 {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	grid := make([]float64, gridSize*gridSize)
	if width == 0 || height == 0 {
		return grid
	}
	counts := make([]int, gridSize*gridSize)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		gy := ((y - bounds.Min.Y) * gridSize) / height
		if gy >= gridSize {
			gy = gridSize - 1
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			gx := ((x - bounds.Min.X) * gridSize) / width
			if gx >= gridSize {
				gx = gridSize - 1
			}
			r, g, b, _ := img.At(x, y).RGBA()
			// Standard Rec. 601 luma weighting, rgba returns 16-bit channels.
			lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
			idx := gy*gridSize + gx
			grid[idx] += lum
			counts[idx]++
		}
	}

	for i := range grid {
		if counts[i] > 0 {
			grid[i] /= float64(counts[i])
		}
	}
	return grid
}
