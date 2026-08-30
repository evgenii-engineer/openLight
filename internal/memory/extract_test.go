package memory

import (
	"bytes"
	"compress/zlib"
	"context"
	"strings"
	"testing"
)

func TestTextExtractorFlattensJSONForRetrieval(t *testing.T) {
	payload := `{"host":"raspberry","disks":[{"size":"1 TB","kind":"ssd"}]}`
	extractor := TextExtractor{Reader: func(string) ([]byte, error) { return []byte(payload), nil }}

	documents, err := extractor.Extract(context.Background(), Source{
		Title: "inventory", MIMEType: "application/json", RawPath: "x.json",
	})
	requireNoError(t, err, "extract")

	if len(documents) != 1 {
		t.Fatalf("expected one document, got %d", len(documents))
	}
	text := documents[0].Text
	// Raw braces embed badly and retrieve worse; flattened paths keep
	// the values searchable.
	for _, expected := range []string{"host: raspberry", "disks[0].size: 1 TB", "disks[0].kind: ssd"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("flattened JSON is missing %q:\n%s", expected, text)
		}
	}
}

func TestTextExtractorRejectsBinaryContent(t *testing.T) {
	extractor := TextExtractor{Reader: func(string) ([]byte, error) {
		return []byte{0xff, 0xfe, 0x00, 0x01, 0xff}, nil
	}}

	_, err := extractor.Extract(context.Background(), Source{MIMEType: "text/plain", RawPath: "x"})
	requireErrorIs(t, err, ErrUnsupportedSource, "binary as text")
}

func TestTextExtractorTruncatesOversizedFiles(t *testing.T) {
	big := strings.Repeat("a", 5000)
	extractor := TextExtractor{
		Reader:   func(string) ([]byte, error) { return []byte(big), nil },
		MaxBytes: 100,
	}

	documents, err := extractor.Extract(context.Background(), Source{MIMEType: "text/plain", RawPath: "x"})
	requireNoError(t, err, "extract")
	if len(documents[0].Text) != 100 {
		t.Fatalf("text is %d bytes, want it capped at 100", len(documents[0].Text))
	}
}

func TestTextExtractorClaimsUnknownMIMEAsCatchAll(t *testing.T) {
	extractor := TextExtractor{}
	if !extractor.Supports("") {
		t.Fatal("an empty MIME type should fall through to the text extractor")
	}
	if extractor.Supports("application/pdf") {
		t.Fatal("the text extractor must not claim PDFs")
	}
}

func TestPDFExtractorReadsAnUncompressedContentStream(t *testing.T) {
	pdf := buildTestPDF(t, "(Raspberry Pi storage notes) Tj\n(A 1 TB SSD is attached over USB 3) Tj", false)
	extractor := PDFExtractor{Reader: func(string) ([]byte, error) { return pdf, nil }}

	documents, err := extractor.Extract(context.Background(), Source{
		Title: "notes.pdf", MIMEType: "application/pdf", RawPath: "notes.pdf",
	})
	requireNoError(t, err, "extract pdf")

	if !strings.Contains(documents[0].Text, "1 TB SSD") {
		t.Fatalf("pdf text was not extracted:\n%s", documents[0].Text)
	}
}

func TestPDFExtractorReadsAFlateCompressedStream(t *testing.T) {
	pdf := buildTestPDF(t, "(Compressed content about the Mac mini brain node) Tj\n(It serves embeddings over Ollama) Tj", true)
	extractor := PDFExtractor{Reader: func(string) ([]byte, error) { return pdf, nil }}

	documents, err := extractor.Extract(context.Background(), Source{
		Title: "brain.pdf", MIMEType: "application/pdf", RawPath: "brain.pdf",
	})
	requireNoError(t, err, "extract compressed pdf")

	if !strings.Contains(documents[0].Text, "Mac mini") {
		t.Fatalf("compressed pdf text was not extracted:\n%s", documents[0].Text)
	}
}

func TestPDFExtractorReportsUnsupportedForScannedDocuments(t *testing.T) {
	// No text operators at all — the signature of a scanned page.
	extractor := PDFExtractor{Reader: func(string) ([]byte, error) {
		return []byte("%PDF-1.7\nstream\n\x00\x01\x02\x03\nendstream\n"), nil
	}}

	_, err := extractor.Extract(context.Background(), Source{MIMEType: "application/pdf", RawPath: "scan.pdf"})
	requireErrorIs(t, err, ErrUnsupportedSource, "scanned pdf")
	if !strings.Contains(err.Error(), "poppler") {
		t.Fatalf("the error should name the fix: %v", err)
	}
}

func TestImageExtractorReusesPrecomputedText(t *testing.T) {
	visionCalls := 0
	extractor := ImageExtractor{
		Describe: func(context.Context, string, string) (string, error) {
			visionCalls++
			return "should not be called", nil
		},
	}

	documents, err := extractor.Extract(context.Background(), Source{
		Title:    "screenshot",
		MIMEType: "image/png",
		Metadata: map[string]string{"description": "a dashboard showing 82% disk usage", "ocr_text": "Disk 82%"},
	})
	requireNoError(t, err, "extract image")

	// The image inbox already ran vision for the user's reply; paying
	// the Mac mini a second time for the same picture is pure waste.
	if visionCalls != 0 {
		t.Fatalf("vision was called %d times despite a cached description", visionCalls)
	}
	if len(documents) != 2 {
		t.Fatalf("expected description and OCR documents, got %d", len(documents))
	}
}

func TestImageExtractorWithoutBackendsIsUnsupportedNotRetried(t *testing.T) {
	_, err := ImageExtractor{}.Extract(context.Background(), Source{MIMEType: "image/png"})
	requireErrorIs(t, err, ErrUnsupportedSource, "image with no backends")
}

func TestImageExtractorTreatsBackendFailureAsRetryable(t *testing.T) {
	extractor := ImageExtractor{
		Describe: func(context.Context, string, string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}

	_, err := extractor.Extract(context.Background(), Source{MIMEType: "image/png", RawPath: "x.png"})
	if err == nil {
		t.Fatal("expected an error")
	}
	// A timed-out vision call must be retried, not parked forever.
	if strings.Contains(err.Error(), ErrUnsupportedSource.Error()) {
		t.Fatalf("a transient failure was classified as unsupported: %v", err)
	}
}

func TestAudioExtractorReusesTheArrivalTranscript(t *testing.T) {
	calls := 0
	extractor := AudioExtractor{Transcribe: func(context.Context, string) (string, error) {
		calls++
		return "should not be called", nil
	}}

	documents, err := extractor.Extract(context.Background(), Source{
		Title:    "Voice note",
		MIMEType: "audio/ogg",
		Metadata: map[string]string{"transcript": "у raspberry теперь ssd на 1 tb"},
	})
	requireNoError(t, err, "extract audio")

	if calls != 0 {
		t.Fatalf("whisper ran %d extra times", calls)
	}
	if !strings.Contains(documents[0].Text, "1 tb") {
		t.Fatalf("transcript missing: %+v", documents)
	}
}

func TestExtractorsDispatchToTheFirstMatch(t *testing.T) {
	extractors := Extractors{
		PDFExtractor{Reader: func(string) ([]byte, error) { return nil, nil }},
		TextExtractor{Reader: func(string) ([]byte, error) { return []byte("plain text body"), nil }},
	}

	documents, err := extractors.Extract(context.Background(), Source{MIMEType: "text/plain", RawPath: "x"})
	requireNoError(t, err, "dispatch")
	if len(documents) != 1 || documents[0].Text != "plain text body" {
		t.Fatalf("dispatched to the wrong extractor: %+v", documents)
	}
}

func TestExtractorsReportUnsupportedWhenNothingClaims(t *testing.T) {
	_, err := Extractors{}.Extract(context.Background(), Source{MIMEType: "application/zip"})
	requireErrorIs(t, err, ErrUnsupportedSource, "no extractor")
}

// buildTestPDF assembles a minimal PDF carrying one content stream.
// Enough structure for the built-in parser; not a valid document for a
// real reader, which is fine — the parser only walks streams.
func buildTestPDF(t *testing.T, operators string, compress bool) []byte {
	t.Helper()

	body := "BT\n" + operators + "\nET\n"
	payload := []byte(body)
	if compress {
		var buffer bytes.Buffer
		writer := zlib.NewWriter(&buffer)
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("compress: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close zlib: %v", err)
		}
		payload = buffer.Bytes()
	}

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.7\n1 0 obj\n<< /Length ")
	pdf.WriteString(itoa(len(payload)))
	pdf.WriteString(" >>\nstream\n")
	pdf.Write(payload)
	pdf.WriteString("\nendstream\nendobj\n%%EOF\n")
	return pdf.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
