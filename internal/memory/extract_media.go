package memory

import (
	"context"
	"fmt"
	"strings"
)

// Describer produces a natural-language description of an image. Wired
// to the existing vision manager by the runtime.
type Describer func(ctx context.Context, path, prompt string) (string, error)

// OCRReader extracts embedded text from an image. Wired to the existing
// OCR manager by the runtime.
type OCRReader func(ctx context.Context, path string) (string, error)

// Transcriber converts an audio file to text. Wired to the existing
// whisper pipeline by the runtime.
type Transcriber func(ctx context.Context, path string) (string, error)

// ImageExtractor indexes the textual representation of an image while
// the original stays on the SSD: a vision description plus, when the
// picture contains text, its OCR output. Both are optional — an image
// with neither available is parked rather than indexed empty.
//
// This deliberately reuses the agent's existing vision/OCR managers
// instead of standing up a second pipeline, so model choice, timeouts,
// and size limits stay configured in one place.
type ImageExtractor struct {
	Describe Describer
	OCR      OCRReader

	// Prompt overrides the vision prompt. Empty uses a memory-specific
	// default that asks for retrieval-friendly detail rather than prose.
	Prompt string
}

func (e ImageExtractor) Supports(mime string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "image/")
}

func (e ImageExtractor) Extract(ctx context.Context, source Source) ([]Document, error) {
	// The image inbox has usually already run vision and OCR on this
	// exact file to answer the user. Reusing that output instead of
	// asking the Mac mini again is the difference between one vision
	// call per image and two.
	if reused := reuseFromMetadata(source, [][2]string{
		{"description", "vision"},
		{"ocr_text", "ocr"},
	}); len(reused) > 0 {
		return reused, nil
	}

	prompt := strings.TrimSpace(e.Prompt)
	if prompt == "" {
		prompt = "Describe this image factually and in detail: what it shows, any visible " +
			"text, names, numbers, devices, and UI elements. Answer in the language of the " +
			"text in the image, or English if there is none."
	}

	var (
		documents []Document
		failures  []string
	)

	if e.Describe != nil {
		description, err := e.Describe(ctx, source.RawPath, prompt)
		if err != nil {
			failures = append(failures, "vision: "+err.Error())
		} else if strings.TrimSpace(description) != "" {
			documents = append(documents, Document{
				Title:    joinTitle(source.Title, "description"),
				Text:     description,
				Metadata: map[string]string{"extractor": "vision"},
			})
		}
	}

	if e.OCR != nil {
		text, err := e.OCR(ctx, source.RawPath)
		if err != nil {
			failures = append(failures, "ocr: "+err.Error())
		} else if strings.TrimSpace(text) != "" {
			documents = append(documents, Document{
				Title:    joinTitle(source.Title, "text"),
				Text:     text,
				Metadata: map[string]string{"extractor": "ocr"},
			})
		}
	}

	if len(documents) == 0 {
		if len(failures) > 0 {
			// A transient vision failure must be retried, not parked, so
			// this is a plain error rather than ErrUnsupportedSource.
			return nil, fmt.Errorf("image extraction failed (%s)", strings.Join(failures, "; "))
		}
		return nil, fmt.Errorf("%w: no vision or ocr backend available for images", ErrUnsupportedSource)
	}
	return documents, nil
}

// AudioExtractor indexes a voice note's transcript. The original audio
// stays in RAW storage so a better model can re-transcribe it later via
// reindex.
type AudioExtractor struct {
	Transcribe Transcriber
}

func (e AudioExtractor) Supports(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	return strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/ogg")
}

func (e AudioExtractor) Extract(ctx context.Context, source Source) ([]Document, error) {
	// Voice notes are transcribed once on arrival so the agent can route
	// them. Reuse that transcript rather than paying for whisper twice.
	if reused := reuseFromMetadata(source, [][2]string{{"transcript", "whisper"}}); len(reused) > 0 {
		return reused, nil
	}
	if e.Transcribe == nil {
		return nil, fmt.Errorf("%w: no transcriber available for audio", ErrUnsupportedSource)
	}
	transcript, err := e.Transcribe(ctx, source.RawPath)
	if err != nil {
		return nil, fmt.Errorf("voice transcription failed: %w", err)
	}
	if strings.TrimSpace(transcript) == "" {
		return nil, fmt.Errorf("%w: empty transcript", ErrUnsupportedSource)
	}
	return []Document{{
		Title:    joinTitle(source.Title, "transcript"),
		Text:     transcript,
		Metadata: map[string]string{"extractor": "whisper"},
	}}, nil
}

// reuseFromMetadata turns pre-computed text carried on the source's
// metadata into extraction output. Keys are tried in order; each
// produces one document tagged with the extractor that made it.
func reuseFromMetadata(source Source, keys [][2]string) []Document {
	var documents []Document
	for _, pair := range keys {
		key, extractor := pair[0], pair[1]
		text := strings.TrimSpace(source.Metadata[key])
		if text == "" {
			continue
		}
		documents = append(documents, Document{
			Title:    joinTitle(source.Title, extractor),
			Text:     text,
			Metadata: map[string]string{"extractor": extractor, "reused": "true"},
		})
	}
	return documents
}

func joinTitle(base, suffix string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return suffix
	}
	return base + " (" + suffix + ")"
}
