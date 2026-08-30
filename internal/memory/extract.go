package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedSource is returned by the extractor registry when no
// extractor claims a source. The queue parks such a job rather than
// retrying it forever — a .zip will not become readable on attempt 12.
var ErrUnsupportedSource = errors.New("memory: unsupported source type")

// Extractor turns an archived source into indexable plain text. One
// source can produce several documents (a PDF's pages, an image's vision
// description plus its OCR text).
type Extractor interface {
	// Supports reports whether this extractor handles the MIME type.
	Supports(mime string) bool

	// Extract reads the source's RAW file and returns its text.
	Extract(ctx context.Context, source Source) ([]Document, error)
}

// Extractors is an ordered registry. First match wins, so register
// specific extractors before generic fallbacks.
type Extractors []Extractor

// Extract dispatches to the first extractor that supports the source.
func (e Extractors) Extract(ctx context.Context, source Source) ([]Document, error) {
	mime := strings.ToLower(strings.TrimSpace(source.MIMEType))
	for _, extractor := range e {
		if extractor == nil || !extractor.Supports(mime) {
			continue
		}
		documents, err := extractor.Extract(ctx, source)
		if err != nil {
			return nil, err
		}
		return cleanDocuments(documents), nil
	}
	return nil, fmt.Errorf("%w: %s (%s)", ErrUnsupportedSource, source.Type, source.MIMEType)
}

// cleanDocuments drops empty documents and normalises whitespace so the
// chunker always sees well-formed input.
func cleanDocuments(documents []Document) []Document {
	out := make([]Document, 0, len(documents))
	for _, document := range documents {
		document.Text = normalizeWhitespace(document.Text)
		if strings.TrimSpace(document.Text) == "" {
			continue
		}
		out = append(out, document)
	}
	return out
}
