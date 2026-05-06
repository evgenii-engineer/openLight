package voice

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"openlight/internal/telegram"
)

type stubDownloader struct {
	path string
}

func (d stubDownloader) DownloadFile(context.Context, string) (telegram.DownloadedFile, error) {
	return telegram.DownloadedFile{
		Path:     d.path,
		FileName: "voice.ogg",
		Cleanup:  func() error { return nil },
	}, nil
}

type stubConverter struct {
	outputPath string
}

func (c stubConverter) ConvertToWAV(context.Context, string) (string, func() error, error) {
	return c.outputPath, func() error { return nil }, nil
}

type stubTranscriber struct {
	transcript string
}

func (t stubTranscriber) Transcribe(context.Context, string) (string, error) {
	return t.transcript, nil
}

func TestProcessorReturnsTranscript(t *testing.T) {
	t.Parallel()

	input := filepath.Join(t.TempDir(), "voice.ogg")
	output := filepath.Join(t.TempDir(), "voice.wav")
	if err := os.WriteFile(input, []byte("voice"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.WriteFile(output, []byte("voice"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	processor := NewProcessor(true, stubConverter{outputPath: output}, stubTranscriber{transcript: "remember that the mac mini is primary"})
	result, err := processor.Process(context.Background(), telegram.IncomingMessage{
		Audio: &telegram.AudioAttachment{FileID: "voice-file"},
	}, stubDownloader{path: input})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if result.RoutedText != "remember that the mac mini is primary" {
		t.Fatalf("unexpected routed text: %#v", result)
	}
}
