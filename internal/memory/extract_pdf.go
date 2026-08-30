package memory

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// PDFExtractor pulls text out of PDFs. It prefers poppler's `pdftotext`
// when that binary is present — same pattern the project already uses
// for whisper, ffmpeg, and tesseract — and falls back to a small
// built-in parser so a Pi without poppler still indexes ordinary text
// PDFs instead of dropping them.
//
// The built-in parser handles uncompressed and FlateDecode content
// streams with WinAnsi/UTF-16 text operators. It does not attempt CID
// font mapping: for a scanned or exotically-encoded PDF it reports
// failure rather than indexing mojibake, and the job is parked with a
// clear reason so `openlight memory pending` shows what to install.
type PDFExtractor struct {
	// BinaryPath is the pdftotext executable. Empty disables the
	// external path and uses the built-in parser only.
	BinaryPath string

	// Reader loads the RAW bytes (used by the built-in parser).
	Reader func(path string) ([]byte, error)
}

func (e PDFExtractor) Supports(mime string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(mime)), "pdf")
}

func (e PDFExtractor) Extract(ctx context.Context, source Source) ([]Document, error) {
	title := strings.TrimSpace(source.Title)
	if title == "" {
		title = "PDF"
	}

	if binary := strings.TrimSpace(e.BinaryPath); binary != "" {
		if text, err := e.runPDFToText(ctx, binary, source.RawPath); err == nil && looksLikeText(text) {
			return []Document{{Title: title, Text: text}}, nil
		}
	}

	if e.Reader == nil {
		return nil, fmt.Errorf("%w: pdf extraction needs pdftotext", ErrUnsupportedSource)
	}
	content, err := e.Reader(source.RawPath)
	if err != nil {
		return nil, err
	}
	text := extractPDFText(content)
	if !looksLikeText(text) {
		return nil, fmt.Errorf(
			"%w: could not extract text from pdf (scanned or CID-encoded); install poppler-utils for pdftotext",
			ErrUnsupportedSource,
		)
	}
	return []Document{{Title: title, Text: text}}, nil
}

func (e PDFExtractor) runPDFToText(ctx context.Context, binary, path string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, "-layout", "-q", "-enc", "UTF-8", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// looksLikeText rejects output that is mostly control bytes or
// replacement characters — the signature of a CID-encoded PDF decoded
// with the wrong tables. Indexing that would poison retrieval with
// unsearchable noise.
func looksLikeText(text string) bool {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) < 16 {
		return false
	}
	var good, total int
	for _, r := range text {
		total++
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r') {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			good++
		}
	}
	if total == 0 {
		return false
	}
	return float64(good)/float64(total) > 0.85
}

// extractPDFText is the built-in fallback parser. It walks every
// `stream`/`endstream` pair, inflates the FlateDecode ones, and pulls
// the string arguments of the text-showing operators (Tj, TJ, ', ").
func extractPDFText(content []byte) string {
	var builder strings.Builder
	for _, stream := range pdfStreams(content) {
		decoded := stream
		if inflated, err := inflate(stream); err == nil {
			decoded = inflated
		}
		if text := pdfContentStreamText(decoded); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	}
	return normalizeWhitespace(builder.String())
}

func pdfStreams(content []byte) [][]byte {
	var streams [][]byte
	cursor := 0
	for {
		start := bytes.Index(content[cursor:], []byte("stream"))
		if start < 0 {
			return streams
		}
		start += cursor + len("stream")
		// Skip the EOL that must follow the `stream` keyword.
		if start < len(content) && content[start] == '\r' {
			start++
		}
		if start < len(content) && content[start] == '\n' {
			start++
		}
		end := bytes.Index(content[start:], []byte("endstream"))
		if end < 0 {
			return streams
		}
		streams = append(streams, content[start:start+end])
		cursor = start + end + len("endstream")
		if cursor >= len(content) {
			return streams
		}
	}
}

func inflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	// Cap the inflated size: a malformed or hostile PDF must not be able
	// to exhaust memory on a Pi.
	out, err := io.ReadAll(io.LimitReader(reader, 16<<20))
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

// pdfContentStreamText scans a decoded content stream for text-showing
// operators and concatenates their string operands. `Td`/`TD`/`T*`/`ET`
// are treated as line breaks so paragraph structure survives well enough
// for the chunker to work with.
func pdfContentStreamText(stream []byte) string {
	var (
		builder strings.Builder
		pending []string
		i       int
	)

	flushLine := func() {
		if len(pending) == 0 {
			return
		}
		builder.WriteString(strings.Join(pending, ""))
		builder.WriteByte('\n')
		pending = nil
	}

	for i < len(stream) {
		switch stream[i] {
		case '(':
			literal, next := readPDFLiteralString(stream, i)
			pending = append(pending, decodePDFString(literal))
			i = next
		case '<':
			// `<<` starts a dictionary, a single `<` a hex string.
			if i+1 < len(stream) && stream[i+1] == '<' {
				i += 2
				continue
			}
			hexString, next := readPDFHexString(stream, i)
			pending = append(pending, decodePDFString(hexString))
			i = next
		case 'T':
			if i+1 < len(stream) {
				switch stream[i+1] {
				case 'd', 'D', '*':
					flushLine()
				}
			}
			i++
		case 'E':
			if bytes.HasPrefix(stream[i:], []byte("ET")) {
				flushLine()
				i += 2
				continue
			}
			i++
		default:
			i++
		}
	}
	flushLine()
	return builder.String()
}

func readPDFLiteralString(stream []byte, start int) ([]byte, int) {
	depth := 0
	var out []byte
	for i := start; i < len(stream); i++ {
		c := stream[i]
		switch c {
		case '\\':
			if i+1 < len(stream) {
				out = append(out, c, stream[i+1])
				i++
			}
		case '(':
			depth++
			if depth > 1 {
				out = append(out, c)
			}
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out, len(stream)
}

func readPDFHexString(stream []byte, start int) ([]byte, int) {
	end := bytes.IndexByte(stream[start:], '>')
	if end < 0 {
		return nil, len(stream)
	}
	hexBody := stream[start+1 : start+end]
	var out []byte
	var nibbles []byte
	for _, c := range hexBody {
		if isHexDigit(c) {
			nibbles = append(nibbles, c)
		}
	}
	if len(nibbles)%2 == 1 {
		nibbles = append(nibbles, '0')
	}
	for i := 0; i+1 < len(nibbles); i += 2 {
		value, err := strconv.ParseUint(string(nibbles[i:i+2]), 16, 8)
		if err != nil {
			continue
		}
		out = append(out, byte(value))
	}
	return out, start + end + 1
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// decodePDFString resolves PDF escape sequences and converts the result
// to UTF-8. Strings starting with the UTF-16BE byte-order mark are
// decoded as UTF-16; everything else is treated as PDFDocEncoding, which
// coincides with Latin-1 for the printable range.
func decodePDFString(raw []byte) string {
	var out []byte
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' || i+1 >= len(raw) {
			out = append(out, c)
			continue
		}
		i++
		switch raw[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '(', ')', '\\':
			out = append(out, raw[i])
		case '\n':
			// Line continuation: emits nothing.
		default:
			if raw[i] >= '0' && raw[i] <= '7' {
				octal := []byte{raw[i]}
				for len(octal) < 3 && i+1 < len(raw) && raw[i+1] >= '0' && raw[i+1] <= '7' {
					i++
					octal = append(octal, raw[i])
				}
				if value, err := strconv.ParseUint(string(octal), 8, 16); err == nil {
					out = append(out, byte(value))
				}
				continue
			}
			out = append(out, raw[i])
		}
	}

	if len(out) >= 2 && out[0] == 0xFE && out[1] == 0xFF {
		units := make([]uint16, 0, (len(out)-2)/2)
		for i := 2; i+1 < len(out); i += 2 {
			units = append(units, uint16(out[i])<<8|uint16(out[i+1]))
		}
		return string(utf16.Decode(units))
	}

	var builder strings.Builder
	for _, b := range out {
		builder.WriteRune(rune(b))
	}
	return builder.String()
}
