package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"openlight/internal/telegram"
	"openlight/internal/utils"
	"openlight/internal/voice"
)

// maxInboundDocumentBytes caps what the agent will pull down from
// Telegram into the archive. 32 MiB is Telegram's own bot download
// limit; refusing above it produces a clear message instead of a slow
// failure.
const maxInboundDocumentBytes = 32 << 20

// handleInboundDocument archives a non-image file attachment and queues
// it for indexing.
//
// Before memory existed, a PDF sent to the bot was dropped without a
// reply. That behaviour is preserved exactly when memory is off: the
// message is consumed silently. With memory on, the original is written
// to durable storage before the user gets an answer, so an offline brain
// node can only ever delay indexing — never lose the file.
func (a *Agent) handleInboundDocument(ctx context.Context, message telegram.IncomingMessage) error {
	document := message.Document
	if document == nil {
		return nil
	}

	if a.memory == nil {
		a.logDebug("ignoring document attachment: memory is disabled",
			"chat_id", message.ChatID, "file_name", document.FileName)
		return nil
	}

	if err := a.authorizer.Error(message.UserID, message.ChatID); err != nil {
		a.logWarn("blocked unauthorized document", "error", err)
		return a.reply(ctx, message.ChatID, message.UserID, "access denied")
	}

	if document.FileSize > maxInboundDocumentBytes {
		return a.reply(ctx, message.ChatID, message.UserID,
			fmt.Sprintf("file is too large (%s); the limit is %s",
				utils.FormatBytes(uint64(document.FileSize)), utils.FormatBytes(maxInboundDocumentBytes)))
	}

	downloader, _ := a.transport.(voice.Downloader)
	if downloader == nil {
		a.logWarn("document received but transport cannot download files")
		return a.reply(ctx, message.ChatID, message.UserID, "file download is unavailable")
	}

	downloaded, err := downloader.DownloadFile(ctx, document.FileID)
	if err != nil {
		a.logError("download document", "error", err, "file_name", document.FileName)
		return a.reply(ctx, message.ChatID, message.UserID, "failed to download the file")
	}
	if downloaded.Cleanup != nil {
		defer func() { _ = downloaded.Cleanup() }()
	}

	title := strings.TrimSpace(document.FileName)
	if title == "" {
		title = filepath.Base(downloaded.Path)
	}

	metadata := map[string]string{}
	if caption := strings.TrimSpace(document.Caption); caption != "" {
		metadata["caption"] = utils.RedactSensitiveText(caption)
	}

	receipt, err := a.memory.IngestFile(ctx, MemoryFile{
		Path:       downloaded.Path,
		Kind:       "documents",
		FileName:   title,
		MIMEType:   document.MimeType,
		Title:      title,
		Source:     "telegram:chat:" + strconv.FormatInt(message.ChatID, 10),
		ExternalID: document.FileID,
		ChatID:     message.ChatID,
		UserID:     message.UserID,
		Metadata:   metadata,
	})
	if err != nil {
		a.logError("archive document", "error", err, "file_name", title)
		return a.reply(ctx, message.ChatID, message.UserID, "could not save the file to memory")
	}

	if receipt.Duplicate {
		return a.reply(ctx, message.ChatID, message.UserID,
			fmt.Sprintf("%s is already in memory — nothing to re-index.", title))
	}
	return a.reply(ctx, message.ChatID, message.UserID,
		fmt.Sprintf("Saved %s. Indexing in the background; ask me about it in a moment.", title))
}

// archiveVoiceNote stores the original audio alongside the transcript
// the agent already produced. Passing the transcript through as metadata
// means the ingestion worker does not run whisper a second time.
func (a *Agent) archiveVoiceNote(ctx context.Context, message telegram.IncomingMessage, audioPath, transcript string) {
	if a.memory == nil || strings.TrimSpace(audioPath) == "" {
		return
	}
	audio := message.Audio
	if audio == nil {
		return
	}
	name := strings.TrimSpace(audio.FileName)
	if name == "" {
		name = "voice.ogg"
	}
	metadata := map[string]string{}
	if trimmed := strings.TrimSpace(transcript); trimmed != "" {
		metadata["transcript"] = utils.RedactSensitiveText(trimmed)
	}

	if _, err := a.memory.IngestFile(ctx, MemoryFile{
		Path:       audioPath,
		Kind:       "voice",
		FileName:   name,
		MIMEType:   audio.MimeType,
		Title:      "Voice note",
		Source:     "telegram:chat:" + strconv.FormatInt(message.ChatID, 10),
		ExternalID: audio.FileID,
		ChatID:     message.ChatID,
		UserID:     message.UserID,
		Metadata:   metadata,
	}); err != nil {
		a.logWarn("archive voice note", "error", err)
	}
}

// archiveImage stores an inbound photo together with whatever the image
// inbox already derived from it (a vision description, OCR text), so the
// worker never re-runs a model on a file the agent just analysed.
func (a *Agent) archiveImage(ctx context.Context, message telegram.IncomingMessage, imagePath, description, ocrText string) {
	if a.memory == nil || strings.TrimSpace(imagePath) == "" {
		return
	}
	image := message.Image
	if image == nil {
		return
	}
	name := strings.TrimSpace(image.FileName)
	if name == "" {
		name = "photo.jpg"
	}

	metadata := map[string]string{}
	if trimmed := strings.TrimSpace(description); trimmed != "" {
		metadata["description"] = trimmed
	}
	if trimmed := strings.TrimSpace(ocrText); trimmed != "" {
		metadata["ocr_text"] = trimmed
	}
	if caption := strings.TrimSpace(image.Caption); caption != "" {
		metadata["caption"] = utils.RedactSensitiveText(caption)
	}

	title := strings.TrimSpace(image.Caption)
	if title == "" {
		title = "Image"
	}

	if _, err := a.memory.IngestFile(ctx, MemoryFile{
		Path:       imagePath,
		Kind:       "images",
		FileName:   name,
		MIMEType:   image.MimeType,
		Title:      title,
		Source:     "telegram:chat:" + strconv.FormatInt(message.ChatID, 10),
		ExternalID: image.FileID,
		ChatID:     message.ChatID,
		UserID:     message.UserID,
		Metadata:   metadata,
	}); err != nil {
		a.logWarn("archive image", "error", err)
	}
}
