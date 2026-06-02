package voice

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"openlight/internal/skills"
	"openlight/internal/telegram"
)

type Downloader interface {
	DownloadFile(ctx context.Context, fileID string) (telegram.DownloadedFile, error)
}

type Converter interface {
	ConvertToWAV(ctx context.Context, inputPath string) (string, func() error, error)
}

type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) (string, error)
}

type Result struct {
	Transcript string
	RoutedText string
}

type Processor struct {
	enabled bool

	converter   Converter
	transcriber Transcriber
}

func NewProcessor(enabled bool, converter Converter, transcriber Transcriber) *Processor {
	return &Processor{
		enabled:     enabled,
		converter:   converter,
		transcriber: transcriber,
	}
}

func (p *Processor) Enabled() bool {
	return p != nil && p.enabled
}

func (p *Processor) Process(ctx context.Context, message telegram.IncomingMessage, downloader Downloader) (Result, error) {
	if p == nil || !p.enabled {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "voice is disabled")
	}
	if message.Audio == nil || strings.TrimSpace(message.Audio.FileID) == "" {
		return Result{}, fmt.Errorf("%w: voice file is required", skills.ErrInvalidArguments)
	}
	if downloader == nil {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "voice download is unavailable")
	}
	if p.transcriber == nil {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "voice transcription is unavailable")
	}

	downloaded, err := downloader.DownloadFile(ctx, message.Audio.FileID)
	if err != nil {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "failed to download voice message")
	}
	if downloaded.Cleanup != nil {
		defer downloaded.Cleanup()
	}

	audioPath := downloaded.Path
	if p.converter != nil {
		convertedPath, cleanup, err := p.converter.ConvertToWAV(ctx, downloaded.Path)
		if err != nil {
			return Result{}, err
		}
		audioPath = convertedPath
		if cleanup != nil {
			defer cleanup()
		}
	}

	transcript, err := p.transcriber.Transcribe(ctx, audioPath)
	if err != nil {
		return Result{}, err
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return Result{}, skills.NewUserError(skills.ErrUnavailable, "could not transcribe voice message")
	}

	return Result{
		Transcript: transcript,
		RoutedText: transcript,
	}, nil
}

type FFmpegConverter struct {
	BinaryPath string
}

func (c FFmpegConverter) ConvertToWAV(ctx context.Context, inputPath string) (string, func() error, error) {
	outputFile, err := os.CreateTemp("", "openlight-voice-*.wav")
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	outputPath := outputFile.Name()
	if err := outputFile.Close(); err != nil {
		_ = os.Remove(outputPath)
		return "", nil, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}

	cmd := exec.CommandContext(
		ctx,
		strings.TrimSpace(c.BinaryPath),
		"-y",
		"-i", inputPath,
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		outputPath,
	)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(outputPath)
		return "", nil, skills.NewUserError(skills.ErrUnavailable, "voice conversion failed")
	}

	return outputPath, func() error {
		return os.Remove(outputPath)
	}, nil
}

type WhisperCLITranscriber struct {
	BinaryPath string
	ModelPath  string
	// Language is the ISO code passed to whisper.cpp via -l (e.g. "ru"). When
	// empty whisper auto-detects, which is less reliable for Russian commands.
	Language string
}

func (t WhisperCLITranscriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	outputDir, err := os.MkdirTemp("", "openlight-whisper-*")
	if err != nil {
		return "", fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	defer os.RemoveAll(outputDir)

	outputBase := filepath.Join(outputDir, "transcript")
	args := []string{
		"-m", strings.TrimSpace(t.ModelPath),
		"-f", audioPath,
		"-of", outputBase,
		"-otxt",
		"-nt",
	}
	if lang := strings.TrimSpace(t.Language); lang != "" {
		args = append(args, "-l", lang)
	}
	cmd := exec.CommandContext(
		ctx,
		strings.TrimSpace(t.BinaryPath),
		args...,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(output.String())
		if detail == "" {
			detail = "no whisper output — check that the binary and model_path are correct and the model file exists"
		}
		wrapped := fmt.Errorf("%w: whisper-cli %q (model %q) failed: %v: %s",
			skills.ErrUnavailable, strings.TrimSpace(t.BinaryPath), strings.TrimSpace(t.ModelPath), err, detail)
		return "", skills.NewUserError(wrapped, "voice transcription failed")
	}

	content, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return "", fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	return strings.TrimSpace(string(content)), nil
}
