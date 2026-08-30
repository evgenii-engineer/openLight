package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// TextExtractor handles everything that is already text on disk: plain
// text, Markdown, JSON, YAML, CSV, source code, Telegram messages, voice
// transcripts, and episode summaries. JSON is flattened to
// "path: value" lines because embedding raw braces wastes tokens and
// retrieves badly.
type TextExtractor struct {
	// Reader loads the RAW bytes for a source. Injected so tests do not
	// need a filesystem.
	Reader func(path string) ([]byte, error)

	// MaxBytes caps how much of a large file is indexed. Zero means the
	// default (2 MiB) — enough for any document a human sends to a chat
	// bot, small enough that a stray 500 MiB log cannot fill the queue.
	MaxBytes int
}

func (e TextExtractor) Supports(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case mime == "":
		return true
	case strings.HasPrefix(mime, "text/"):
		return true
	case strings.Contains(mime, "json"),
		strings.Contains(mime, "yaml"),
		strings.Contains(mime, "xml"),
		strings.Contains(mime, "javascript"),
		strings.Contains(mime, "csv"):
		return true
	default:
		return false
	}
}

func (e TextExtractor) Extract(_ context.Context, source Source) ([]Document, error) {
	content, err := e.read(source.RawPath)
	if err != nil {
		return nil, err
	}

	limit := e.MaxBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	if len(content) > limit {
		content = content[:limit]
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("%w: %s is not valid UTF-8 text", ErrUnsupportedSource, source.MIMEType)
	}

	text := string(content)
	mime := strings.ToLower(source.MIMEType)
	if strings.Contains(mime, "json") {
		if flattened, ok := flattenJSON(content); ok {
			text = flattened
		}
	}

	title := strings.TrimSpace(source.Title)
	if title == "" {
		title = source.Type
	}
	return []Document{{Title: title, Text: text}}, nil
}

func (e TextExtractor) read(path string) ([]byte, error) {
	if e.Reader == nil {
		return nil, fmt.Errorf("memory: text extractor has no reader")
	}
	return e.Reader(path)
}

// flattenJSON renders a JSON document as sorted "a.b[0].c: value" lines.
// Returns ok=false for input that is not JSON so the caller can fall
// back to indexing the raw text.
func flattenJSON(content []byte) (string, bool) {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}

	lines := map[string]string{}
	flattenValue("", decoded, lines)
	if len(lines) == 0 {
		return "", false
	}

	keys := make([]string, 0, len(lines))
	for key := range lines {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(lines[key])
		builder.WriteByte('\n')
	}
	return builder.String(), true
}

func flattenValue(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			out[orRoot(prefix)] = "{}"
			return
		}
		for key, nested := range typed {
			child := key
			if prefix != "" {
				child = prefix + "." + key
			}
			flattenValue(child, nested, out)
		}
	case []any:
		if len(typed) == 0 {
			out[orRoot(prefix)] = "[]"
			return
		}
		for i, nested := range typed {
			flattenValue(fmt.Sprintf("%s[%d]", prefix, i), nested, out)
		}
	case nil:
		out[orRoot(prefix)] = "null"
	default:
		out[orRoot(prefix)] = fmt.Sprintf("%v", typed)
	}
}

func orRoot(prefix string) string {
	if prefix == "" {
		return "value"
	}
	return prefix
}
