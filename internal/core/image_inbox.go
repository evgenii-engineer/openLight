package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"openlight/internal/skills"
	"openlight/internal/telegram"
	"openlight/internal/voice"
)

// ImageInbox processes images uploaded to the bot. It downloads the file
// using the transport's Downloader, then dispatches it to vision_analyze or
// ocr_extract depending on the caption. The caller registers the relevant
// skills in the registry; this type stays free of provider details.
type ImageInbox struct {
	registry           *skills.Registry
	visionAvailable    bool
	ocrAvailable       bool
	defaultPrompt      string
	persistArtifacts   bool
	artifactsDir       string
}

// ImageInboxOptions describes how the inbound image router should behave.
type ImageInboxOptions struct {
	VisionEnabled    bool
	OCREnabled       bool
	DefaultPrompt    string
	ArtifactsDir     string // Optional: when set, downloaded files are copied here for persistence.
	PersistArtifacts bool
}

// NewImageInbox returns a new processor wired to the registry. When neither
// vision nor OCR is enabled, Enabled() reports false and Process refuses
// requests with a friendly error.
func NewImageInbox(registry *skills.Registry, opts ImageInboxOptions) *ImageInbox {
	prompt := strings.TrimSpace(opts.DefaultPrompt)
	if prompt == "" {
		prompt = "Describe this image in concise plain English."
	}
	return &ImageInbox{
		registry:         registry,
		visionAvailable:  opts.VisionEnabled,
		ocrAvailable:     opts.OCREnabled,
		defaultPrompt:    prompt,
		persistArtifacts: opts.PersistArtifacts,
		artifactsDir:     strings.TrimSpace(opts.ArtifactsDir),
	}
}

// Enabled reports whether at least one inbound-image consumer is registered.
func (i *ImageInbox) Enabled() bool {
	return i != nil && (i.visionAvailable || i.ocrAvailable)
}

// Process downloads the image attached to the message and routes it to the
// appropriate skill based on the caption (text). Returns a fully-formed
// skill Result so the caller can deliver it through the normal reply path.
func (i *ImageInbox) Process(ctx context.Context, message telegram.IncomingMessage, downloader voice.Downloader) (skills.Result, error) {
	if !i.Enabled() {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "image input is disabled")
	}
	if message.Image == nil || strings.TrimSpace(message.Image.FileID) == "" {
		return skills.Result{}, fmt.Errorf("%w: image attachment is required", skills.ErrInvalidArguments)
	}
	if downloader == nil {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "image download is unavailable")
	}

	downloaded, err := downloader.DownloadFile(ctx, message.Image.FileID)
	if err != nil {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "failed to download image")
	}
	cleanup := downloaded.Cleanup
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	caption := strings.TrimSpace(message.Image.Caption)
	if caption == "" {
		caption = strings.TrimSpace(message.Text)
	}

	skillName, prompt := i.routeCaption(caption)

	skill, ok := i.registry.Get(skillName)
	if !ok {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "image skill is not registered")
	}

	args := map[string]string{"path": downloaded.Path}
	switch skillName {
	case "vision_analyze":
		args["prompt"] = prompt
	}

	return skill.Execute(ctx, skills.Input{
		RawText: message.Text,
		Args:    args,
		UserID:  message.UserID,
		ChatID:  message.ChatID,
		Source:  message.Source,
	})
}

// routeCaption picks a skill and prompt for the supplied caption. Captions
// containing OCR-style words ("read", "extract text", "ocr", "распознай")
// route to ocr_extract when available; everything else goes to vision_analyze.
func (i *ImageInbox) routeCaption(caption string) (skillName, prompt string) {
	lower := strings.ToLower(strings.TrimSpace(caption))
	if i.ocrAvailable && captionLooksLikeOCR(lower) {
		return "ocr_extract", ""
	}
	if i.visionAvailable {
		if lower == "" {
			return "vision_analyze", i.defaultPrompt
		}
		return "vision_analyze", caption
	}
	if i.ocrAvailable {
		return "ocr_extract", ""
	}
	return "vision_analyze", i.defaultPrompt
}

func captionLooksLikeOCR(caption string) bool {
	if caption == "" {
		return false
	}
	for _, marker := range []string{
		"ocr", "read text", "read the text", "extract text",
		"распознай", "распознать текст", "извлеки текст", "прочитай",
	} {
		if strings.Contains(caption, marker) {
			return true
		}
	}
	return false
}

// ArtifactPath returns a deterministic on-disk path for persisting an
// inbound image when ArtifactsDir is configured. Currently unused but
// reserved for future visual-watch baselines uploaded from chat.
func (i *ImageInbox) ArtifactPath(name string) string {
	if !i.persistArtifacts || i.artifactsDir == "" {
		return ""
	}
	return filepath.Join(i.artifactsDir, name)
}
