package core

import (
	"context"
	"errors"
	"os"
	"testing"

	"openlight/internal/skills"
	"openlight/internal/telegram"
)

type stubVisionSkill struct {
	called bool
	args   map[string]string
}

func (s *stubVisionSkill) Definition() skills.Definition {
	return skills.Definition{Name: "vision_analyze", Description: "vision"}
}

func (s *stubVisionSkill) Execute(_ context.Context, input skills.Input) (skills.Result, error) {
	s.called = true
	s.args = input.Args
	return skills.Result{Text: "vision response"}, nil
}

type stubOCRSkill struct {
	called bool
	args   map[string]string
}

func (s *stubOCRSkill) Definition() skills.Definition {
	return skills.Definition{Name: "ocr_extract", Description: "ocr"}
}

func (s *stubOCRSkill) Execute(_ context.Context, input skills.Input) (skills.Result, error) {
	s.called = true
	s.args = input.Args
	return skills.Result{Text: "ocr response"}, nil
}

type stubDownloader struct {
	path     string
	calls    int
	cleanups int
}

func (s *stubDownloader) DownloadFile(_ context.Context, _ string) (telegram.DownloadedFile, error) {
	s.calls++
	return telegram.DownloadedFile{
		Path: s.path,
		Cleanup: func() error {
			s.cleanups++
			return nil
		},
	}, nil
}

func writeTempPNG(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "image-*.png")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := file.WriteString("fake-png"); err != nil {
		t.Fatalf("write: %v", err)
	}
	file.Close()
	return file.Name()
}

func TestImageInboxRoutesPlainCaptionToVision(t *testing.T) {
	t.Parallel()
	registry := skills.NewRegistry()
	visionSkill := &stubVisionSkill{}
	ocrSkill := &stubOCRSkill{}
	if err := registry.Register(visionSkill); err != nil {
		t.Fatalf("register vision: %v", err)
	}
	if err := registry.Register(ocrSkill); err != nil {
		t.Fatalf("register ocr: %v", err)
	}

	inbox := NewImageInbox(registry, ImageInboxOptions{VisionEnabled: true, OCREnabled: true, DefaultPrompt: "describe"})
	if !inbox.Enabled() {
		t.Fatalf("inbox should be enabled when at least one consumer is wired")
	}

	downloader := &stubDownloader{path: writeTempPNG(t)}
	result, err := inbox.Process(context.Background(), telegram.IncomingMessage{
		Image: &telegram.ImageAttachment{FileID: "abc"},
		Text:  "what is on this screenshot?",
	}, downloader)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !visionSkill.called {
		t.Fatalf("expected vision skill to run, ocr.called=%v", ocrSkill.called)
	}
	if visionSkill.args["prompt"] == "" {
		t.Fatalf("expected prompt forwarded, got %v", visionSkill.args)
	}
	if visionSkill.args["path"] != downloader.path {
		t.Fatalf("expected file path forwarded, got %v", visionSkill.args)
	}
	if result.Text == "" {
		t.Fatalf("expected non-empty result text")
	}
	if downloader.cleanups != 1 {
		t.Fatalf("expected cleanup once, got %d", downloader.cleanups)
	}
}

func TestImageInboxRoutesOCRCaptionToOCR(t *testing.T) {
	t.Parallel()
	registry := skills.NewRegistry()
	visionSkill := &stubVisionSkill{}
	ocrSkill := &stubOCRSkill{}
	registry.MustRegister(visionSkill)
	registry.MustRegister(ocrSkill)

	inbox := NewImageInbox(registry, ImageInboxOptions{VisionEnabled: true, OCREnabled: true})
	downloader := &stubDownloader{path: writeTempPNG(t)}
	if _, err := inbox.Process(context.Background(), telegram.IncomingMessage{
		Image: &telegram.ImageAttachment{FileID: "abc", Caption: "извлеки текст с этого изображения"},
	}, downloader); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !ocrSkill.called {
		t.Fatalf("expected OCR skill to run when caption asks for text extraction")
	}
	if visionSkill.called {
		t.Fatalf("vision should not run when ocr is the better match")
	}
}

func TestImageInboxDisabled(t *testing.T) {
	t.Parallel()
	registry := skills.NewRegistry()
	inbox := NewImageInbox(registry, ImageInboxOptions{})
	if inbox.Enabled() {
		t.Fatalf("inbox should be disabled when neither vision nor ocr is enabled")
	}
	downloader := &stubDownloader{path: writeTempPNG(t)}
	_, err := inbox.Process(context.Background(), telegram.IncomingMessage{Image: &telegram.ImageAttachment{FileID: "abc"}}, downloader)
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}
