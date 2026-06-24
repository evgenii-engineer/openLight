package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"openlight/internal/auth"
	"openlight/internal/models"
	"openlight/internal/router"
	"openlight/internal/router/semantic"
	"openlight/internal/skills"
	"openlight/internal/storage"
	"openlight/internal/telegram"
	"openlight/internal/utils"
	"openlight/internal/voice"
	watchengine "openlight/internal/watch"
)

type Transport interface {
	Poll(ctx context.Context, handler func(context.Context, telegram.IncomingMessage) error) error
	SendText(ctx context.Context, chatID int64, text string) error
}

type buttonTransport interface {
	SendTextWithButtons(ctx context.Context, chatID int64, text string, buttons [][]telegram.Button) error
}

// photoTransport is implemented by transports that can deliver image and file
// attachments alongside the text reply (Telegram Bot does, the local CLI
// transport doesn't — it falls back to a textual marker).
type photoTransport interface {
	SendPhoto(ctx context.Context, chatID int64, path, caption string) error
	SendDocument(ctx context.Context, chatID int64, path, caption string) error
}

// typingTransport surfaces Telegram's chat_action="typing" indicator. CLI
// and other transports that have no equivalent simply don't implement it
// and the agent silently skips the call.
type typingTransport interface {
	SendChatAction(ctx context.Context, chatID int64, action string) error
}

type Agent struct {
	transport       Transport
	authorizer      *auth.Authorizer
	router          *router.Router
	registry        *skills.Registry
	repository      storage.Repository
	watchService    *watchengine.Service
	voiceProcessor  *voice.Processor
	imageInbox      *ImageInbox
	ui              UI
	replyTranscript bool
	logger          *slog.Logger
	requestTimeout  time.Duration
}

// UI is the optional Telegram button-driven layer. Implemented by
// internal/telegram/ui.UI; declared here as an interface so the core agent
// stays free of Telegram-specific dependencies in tests.
type UI interface {
	HandleCallback(ctx context.Context, msg telegram.IncomingMessage) error
	HandlePendingInput(ctx context.Context, msg telegram.IncomingMessage) (bool, error)
	HasPendingInput(chatID int64) bool
	StartSkillInput(ctx context.Context, chatID, userID int64, skill skills.Skill, args map[string]string) (bool, error)
	CancelPending(chatID int64)
	MapReplyKeyboard(text string) (string, bool)
	OpenScreen(ctx context.Context, chatID, userID int64, screen string) error
	SendHome(ctx context.Context, chatID int64) error
	IsFreeChat(chatID int64) bool
	SetFreeChat(chatID int64, enabled bool)
}

const maxLoggedMessageChars = 160
const pendingClarificationTTL = 10 * time.Minute

type pendingClarification struct {
	SourceText string `json:"source_text"`
	Question   string `json:"question"`
}

func NewAgent(
	transport Transport,
	authorizer *auth.Authorizer,
	router *router.Router,
	registry *skills.Registry,
	repository storage.Repository,
	watchService *watchengine.Service,
	logger *slog.Logger,
	requestTimeout time.Duration,
) *Agent {
	return &Agent{
		transport:      transport,
		authorizer:     authorizer,
		router:         router,
		registry:       registry,
		repository:     repository,
		watchService:   watchService,
		logger:         logger,
		requestTimeout: requestTimeout,
	}
}

func (a *Agent) SetVoiceProcessor(processor *voice.Processor, replyTranscript bool) {
	a.voiceProcessor = processor
	a.replyTranscript = replyTranscript
}

// SetImageInbox installs the optional inbound image processor. When enabled,
// photos sent to the bot are downloaded and routed to vision/ocr based on the
// caption.
func (a *Agent) SetImageInbox(inbox *ImageInbox) {
	a.imageInbox = inbox
}

// SetUI installs the optional Telegram button-driven UI layer.
func (a *Agent) SetUI(ui UI) {
	a.ui = ui
}

func (a *Agent) Run(ctx context.Context) error {
	return a.transport.Poll(ctx, a.HandleMessage)
}

// HandleMessage is the agent's request lifecycle. It is intentionally
// kept short and linear so the order of stages is obvious from a single
// read: preprocess (voice/image, empty filter) → persist + auth → UI
// pipeline → watch action callbacks → route (with clarification reuse)
// → execute + reply. Each stage is a private method that returns a
// (handled, error) pair; HandleMessage stays the only place those
// stages compose so there is no implicit middleware ordering anywhere.
func (a *Agent) HandleMessage(ctx context.Context, message telegram.IncomingMessage) error {
	stopTyping := a.startTyping(ctx, message.ChatID)
	defer stopTyping()

	replyPrefix, handled, err := a.preprocessAttachments(ctx, &message)
	if handled || err != nil {
		return err
	}
	if strings.TrimSpace(message.Text) == "" {
		return nil
	}
	message.Source = normalizeIncomingSource(message.Source, message.Audio != nil)

	sanitizedMessageText := utils.RedactSensitiveText(message.Text)
	a.saveMessage(ctx, models.Message{
		TelegramUserID: message.UserID,
		TelegramChatID: message.ChatID,
		Role:           models.RoleUser,
		Text:           sanitizedMessageText,
	})

	if err := a.authorizer.Error(message.UserID, message.ChatID); err != nil {
		a.logWarn("blocked unauthorized message", "error", err)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "access denied"))
	}

	if handled, err := a.handleUIPipeline(ctx, message, replyPrefix); handled || err != nil {
		return err
	}

	if handled, err := a.handleWatchAction(ctx, message, replyPrefix); handled || err != nil {
		return err
	}

	decision, decisionText, err := a.routeWithClarification(ctx, message, sanitizedMessageText)
	if err != nil {
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
	}

	a.logRouteDecision("router decision", message, decision)

	if decision.ShouldClarify() {
		a.savePendingClarification(ctx, message.ChatID, decisionText, decision.ClarificationQuestion)
		a.logInfo(
			"requesting clarification",
			"message_text", shortTextForLog(sanitizedMessageText),
			"mode", decision.Mode,
			"skill", decision.SkillName,
			"args", sanitizedArgs(decision.Args),
			"confidence", decision.Confidence,
			"question", decision.ClarificationQuestion,
		)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, decision.ClarificationQuestion))
	}

	decision = a.applyChatFallback(message, decision)

	if !decision.Matched() {
		a.logWarn(
			"router did not match any skill",
			"message_text", shortTextForLog(sanitizedMessageText),
			"chat_id", message.ChatID,
			"user_id", message.UserID,
		)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "skill not found. Try /help or /skills."))
	}

	return a.executeSkillAndReply(ctx, message, decision, replyPrefix, sanitizedMessageText)
}

// preprocessAttachments handles voice transcription and image inbox
// dispatch. Voice mutates message.Text in place; image inbox is a
// terminal flow that replies and returns handled=true. Returns
// (replyPrefix, handled, err) where handled means the caller must
// stop processing (image inbox already replied or voice produced an
// error reply).
func (a *Agent) preprocessAttachments(ctx context.Context, message *telegram.IncomingMessage) (string, bool, error) {
	replyPrefix := ""

	if strings.TrimSpace(message.Text) == "" && message.Audio != nil {
		if a.voiceProcessor == nil {
			return "", true, a.reply(ctx, message.ChatID, message.UserID, "voice is disabled")
		}
		downloader, _ := a.transport.(voice.Downloader)
		result, err := a.voiceProcessor.Process(ctx, *message, downloader)
		if err != nil {
			a.logWarn("voice processing failed", "error", err)
			return "", true, a.reply(ctx, message.ChatID, message.UserID, userMessageForError(err))
		}
		message.Text = result.RoutedText
		if a.replyTranscript {
			replyPrefix = "Transcript: " + result.Transcript
		}
	}

	if message.Image != nil {
		// If the UI is waiting for an image path from the user, download the
		// photo to a temp file and inject its path as the text reply so the
		// conversational input flow can consume it normally.
		if a.ui != nil && a.ui.HasPendingInput(message.ChatID) {
			downloader, _ := a.transport.(voice.Downloader)
			if downloader != nil {
				if downloaded, dlErr := downloader.DownloadFile(ctx, message.Image.FileID); dlErr == nil {
					message.Text = downloaded.Path
					message.Image = nil
					return replyPrefix, false, nil
				}
			}
		}

		if a.imageInbox == nil || !a.imageInbox.Enabled() {
			return "", true, a.reply(ctx, message.ChatID, message.UserID, "image input is disabled (enable vision or ocr)")
		}
		downloader, _ := a.transport.(voice.Downloader)
		if err := a.authorizer.Error(message.UserID, message.ChatID); err != nil {
			a.logWarn("blocked unauthorized image", "error", err)
			return "", true, a.reply(ctx, message.ChatID, message.UserID, "access denied")
		}
		result, err := a.imageInbox.Process(ctx, *message, downloader)
		if err != nil {
			return "", true, a.reply(ctx, message.ChatID, message.UserID, userMessageForError(err))
		}
		return "", true, a.replyResult(ctx, message.ChatID, message.UserID, result)
	}

	return replyPrefix, false, nil
}

// handleUIPipeline runs the optional Telegram UI layer: callback
// dispatch, reply-keyboard taps, /menu, pending-input flows, and free
// chat. Returns handled=true if the UI consumed the message.
func (a *Agent) handleUIPipeline(ctx context.Context, message telegram.IncomingMessage, replyPrefix string) (bool, error) {
	if a.ui == nil {
		return false, nil
	}

	if message.IsCallback {
		if err := a.ui.HandleCallback(ctx, message); err != nil {
			a.logError("handle callback", "error", err)
			return true, a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
		}
		return true, nil
	}

	// Reply-keyboard taps and /menu always take precedence over a pending
	// input flow — they let the user escape if they got stuck.
	if screen, ok := a.ui.MapReplyKeyboard(message.Text); ok {
		a.ui.CancelPending(message.ChatID)
		if err := a.ui.OpenScreen(ctx, message.ChatID, message.UserID, screen); err != nil {
			a.logError("open ui screen", "screen", screen, "error", err)
			return true, a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
		}
		return true, nil
	}
	if isMenuCommand(message.Text) {
		a.ui.CancelPending(message.ChatID)
		a.ui.SetFreeChat(message.ChatID, false)
		if err := a.ui.SendHome(ctx, message.ChatID); err != nil {
			a.logError("send ui home", "error", err)
			return true, a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
		}
		return true, nil
	}
	if a.ui.HasPendingInput(message.ChatID) {
		handled, err := a.ui.HandlePendingInput(ctx, message)
		if err != nil {
			a.logError("handle pending input", "error", err)
			return true, a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
		}
		if handled {
			return true, nil
		}
	}
	if a.ui.IsFreeChat(message.ChatID) && !strings.HasPrefix(strings.TrimSpace(message.Text), "/") {
		if _, ok := a.registry.Get("chat"); ok {
			return true, a.runFreeChat(ctx, message, replyPrefix)
		}
	}
	return false, nil
}

// handleWatchAction lets the watch service consume action callbacks
// (e.g. yes/no confirmations on an open incident) before routing.
func (a *Agent) handleWatchAction(ctx context.Context, message telegram.IncomingMessage, replyPrefix string) (bool, error) {
	if a.watchService == nil {
		return false, nil
	}
	handled, err := a.watchService.HandleAction(ctx, message.ChatID, message.UserID, message.Text)
	if err != nil {
		a.logError("handle watch action", "error", err)
		return true, a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "internal error"))
	}
	return handled, nil
}

// routeWithClarification routes the message and, when a pending
// clarification exists for the chat, re-routes the stitched
// (original + answer) text. The pending state is always cleared on the
// way out so a stale prompt doesn't poison the next message.
func (a *Agent) routeWithClarification(ctx context.Context, message telegram.IncomingMessage, sanitizedMessageText string) (router.Decision, string, error) {
	decisionText := message.Text
	decision, err := a.router.Route(ctx, decisionText)
	if err != nil {
		a.logError("route message", "error", err)
		return router.Decision{}, decisionText, err
	}

	pending, hasPending := a.loadPendingClarification(ctx, message.ChatID)
	if hasPending && shouldUsePendingClarification(message.Text, decision) {
		clarifiedText := composeClarifiedText(pending, message.Text)
		a.logDebug(
			"routing with pending clarification context",
			"message_text", shortTextForLog(sanitizedMessageText),
			"pending_source_text", shortTextForLog(pending.SourceText),
			"pending_question", pending.Question,
			"clarified_text", shortTextForLog(utils.RedactSensitiveText(clarifiedText)),
		)

		clarifiedDecision, clarifiedErr := a.router.Route(ctx, clarifiedText)
		if clarifiedErr != nil {
			a.logError("route message with pending clarification", "error", clarifiedErr)
		} else {
			decisionText = clarifiedText
			decision = clarifiedDecision
		}
	}

	if hasPending {
		a.clearPendingClarification(ctx, message.ChatID)
	}
	return decision, decisionText, nil
}

// applyChatFallback turns an unmatched non-slash message into a chat
// skill invocation when the chat module is registered. Pure function
// over the decision; no I/O.
func (a *Agent) applyChatFallback(message telegram.IncomingMessage, decision router.Decision) router.Decision {
	if decision.Matched() {
		return decision
	}
	if strings.HasPrefix(strings.TrimSpace(message.Text), "/") {
		return decision
	}
	if _, ok := a.registry.Get("chat"); !ok {
		return decision
	}
	chatDecision := router.Decision{
		Mode:      router.ModeUnknown,
		SkillName: "chat",
		Args:      map[string]string{"text": message.Text},
	}
	a.logRouteDecision("router chat fallback", message, chatDecision)
	return chatDecision
}

// executeSkillAndReply resolves the decision to a Skill, optionally
// hands off to the UI's pending-input flow for bare slash commands,
// runs the skill under requestTimeout, persists the skill_call, and
// sends the reply.
func (a *Agent) executeSkillAndReply(
	ctx context.Context,
	message telegram.IncomingMessage,
	decision router.Decision,
	replyPrefix string,
	sanitizedMessageText string,
) error {
	skill, ok := a.registry.Get(decision.SkillName)
	if !ok {
		a.logWarn("router returned unknown skill", "skill", decision.SkillName)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "skill not found"))
	}

	// If a bare slash command resolved to a skill that needs input the
	// router didn't fill, hand off to the UI's pending-input flow so the
	// user gets the same conversational prompt they'd see when tapping
	// the skill in the menu — instead of a terse "argument required"
	// error. Only triggers when the message is just `/skill_name` (no
	// trailing args), so `/vision_analyze ./img.png` and friends still
	// hit the skill's own RawText fallback.
	if a.ui != nil && isBareSlashCommand(message.Text) {
		handed, err := a.ui.StartSkillInput(ctx, message.ChatID, message.UserID, skill, decision.Args)
		if err != nil {
			a.logError("start skill input flow", "error", err, "skill", decision.SkillName)
		}
		if handed {
			return nil
		}
	}

	startedAt := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()

	a.logDebug(
		"skill execution started",
		"skill", skill.Definition().Name,
		"mode", decision.Mode,
		"args", sanitizedArgs(decision.Args),
		"message_text", shortTextForLog(sanitizedMessageText),
		"chat_id", message.ChatID,
		"user_id", message.UserID,
	)

	result, execErr := skill.Execute(execCtx, skills.Input{
		RawText: message.Text,
		Args:    decision.Args,
		UserID:  message.UserID,
		ChatID:  message.ChatID,
		Source:  message.Source,
	})

	durationMS := time.Since(startedAt).Milliseconds()

	a.saveSkillCall(ctx, models.SkillCall{
		SkillName:  skill.Definition().Name,
		InputText:  sanitizedMessageText,
		ArgsJSON:   marshalArgs(sanitizedArgs(decision.Args)),
		Status:     callStatus(execErr),
		ErrorText:  errorText(execErr),
		DurationMS: durationMS,
	})

	if execErr != nil {
		a.logError(
			"execute skill",
			"skill", skill.Definition().Name,
			"mode", decision.Mode,
			"args", sanitizedArgs(decision.Args),
			"duration_ms", durationMS,
			"error", execErr,
		)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, userMessageForError(execErr)))
	}

	if replyPrefix != "" {
		result.Text = withReplyPrefix(replyPrefix, result.Text)
	}

	sendStart := time.Now()
	sendErr := a.replyResult(ctx, message.ChatID, message.UserID, result)
	sendMS := time.Since(sendStart).Milliseconds()

	a.logDebug(
		"skill execution completed",
		"skill", skill.Definition().Name,
		"mode", decision.Mode,
		"args", sanitizedArgs(decision.Args),
		"duration_ms", durationMS,
		"send_ms", sendMS,
	)
	return sendErr
}

// runFreeChat executes the chat skill directly with the user's text, skipping
// the router. Used when the chat is in free-chat mode (toggled by tapping AI).
func (a *Agent) runFreeChat(ctx context.Context, message telegram.IncomingMessage, replyPrefix string) error {
	skill, ok := a.registry.Get("chat")
	if !ok {
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, "chat skill not registered"))
	}

	execCtx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()

	result, execErr := skill.Execute(execCtx, skills.Input{
		RawText: message.Text,
		Args:    map[string]string{"text": message.Text},
		UserID:  message.UserID,
		ChatID:  message.ChatID,
		Source:  message.Source,
	})
	if execErr != nil {
		a.logError("execute free chat", "error", execErr)
		return a.reply(ctx, message.ChatID, message.UserID, withReplyPrefix(replyPrefix, userMessageForError(execErr)))
	}

	if replyPrefix != "" {
		result.Text = withReplyPrefix(replyPrefix, result.Text)
	}
	return a.replyResult(ctx, message.ChatID, message.UserID, result)
}

func (a *Agent) reply(ctx context.Context, chatID, userID int64, text string) error {
	return a.replyResult(ctx, chatID, userID, skills.Result{Text: text})
}

// startTyping fires Telegram's "user is typing..." indicator immediately
// and refreshes it every 4 seconds until the returned cancel func is
// called (the indicator auto-clears after ~5s of silence). Transports
// without typing support (CLI) get a no-op. Errors are swallowed — a
// missed typing ping never blocks the actual reply.
func (a *Agent) startTyping(ctx context.Context, chatID int64) func() {
	typer, ok := a.transport.(typingTransport)
	if !ok {
		return func() {}
	}

	typingCtx, cancel := context.WithCancel(ctx)
	go func() {
		// Fire once immediately so the indicator shows up before the
		// first LLM prefill starts; ignore errors.
		_ = typer.SendChatAction(typingCtx, chatID, "typing")

		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				_ = typer.SendChatAction(typingCtx, chatID, "typing")
			}
		}
	}()
	return cancel
}

func (a *Agent) replyResult(ctx context.Context, chatID, userID int64, result skills.Result) error {
	textForLog := result.Text
	textForUser := result.Text
	photoSender, supportsPhotos := a.transport.(photoTransport)
	// When the transport can't render attachments, append their paths so the
	// user still has a pointer to the artifact on disk.
	if !supportsPhotos && len(result.Attachments) > 0 {
		textForUser = appendAttachmentNote(textForUser, result.Attachments)
	}

	var err error
	if len(result.Buttons) > 0 {
		if sender, ok := a.transport.(buttonTransport); ok {
			err = sender.SendTextWithButtons(ctx, chatID, textForUser, result.Buttons)
		} else {
			err = a.transport.SendText(ctx, chatID, textForUser)
		}
	} else {
		err = a.transport.SendText(ctx, chatID, textForUser)
	}
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}

	if supportsPhotos {
		for _, attachment := range result.Attachments {
			path := strings.TrimSpace(attachment.Path)
			if path == "" {
				continue
			}
			caption := strings.TrimSpace(attachment.Caption)
			var sendErr error
			switch attachment.Kind {
			case skills.AttachmentDocument:
				sendErr = photoSender.SendDocument(ctx, chatID, path, caption)
			default:
				sendErr = photoSender.SendPhoto(ctx, chatID, path, caption)
			}
			if sendErr != nil {
				a.logError("send attachment", "error", sendErr, "path", path, "kind", attachment.Kind)
			}
		}
	}

	a.saveMessage(ctx, models.Message{
		TelegramUserID: userID,
		TelegramChatID: chatID,
		Role:           models.RoleAssistant,
		Text:           textForLog,
	})

	return nil
}

func appendAttachmentNote(text string, attachments []skills.Attachment) string {
	if len(attachments) == 0 {
		return text
	}
	parts := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		path := strings.TrimSpace(attachment.Path)
		if path == "" {
			continue
		}
		parts = append(parts, "• "+path)
	}
	if len(parts) == 0 {
		return text
	}
	prefix := strings.TrimSpace(text)
	if prefix == "" {
		return "Files:\n" + strings.Join(parts, "\n")
	}
	return prefix + "\n\nFiles:\n" + strings.Join(parts, "\n")
}

func (a *Agent) saveMessage(ctx context.Context, message models.Message) {
	if err := a.repository.SaveMessage(ctx, message); err != nil {
		a.logError("save message", "error", err)
	}
}

func (a *Agent) saveSkillCall(ctx context.Context, call models.SkillCall) {
	if err := a.repository.SaveSkillCall(ctx, call); err != nil {
		a.logError("save skill call", "error", err)
	}
}

func (a *Agent) savePendingClarification(ctx context.Context, chatID int64, sourceText, question string) {
	payload, err := json.Marshal(pendingClarification{
		SourceText: strings.TrimSpace(utils.RedactSensitiveText(sourceText)),
		Question:   strings.TrimSpace(question),
	})
	if err != nil {
		a.logError("marshal pending clarification", "error", err, "chat_id", chatID)
		return
	}

	if err := a.repository.SetSetting(ctx, pendingClarificationKey(chatID), string(payload)); err != nil {
		a.logError("save pending clarification", "error", err, "chat_id", chatID)
	}
}

func (a *Agent) loadPendingClarification(ctx context.Context, chatID int64) (pendingClarification, bool) {
	setting, ok, err := a.repository.GetSetting(ctx, pendingClarificationKey(chatID))
	if err != nil {
		a.logError("load pending clarification", "error", err, "chat_id", chatID)
		return pendingClarification{}, false
	}
	if !ok || strings.TrimSpace(setting.Value) == "" {
		return pendingClarification{}, false
	}

	if time.Since(setting.UpdatedAt) > pendingClarificationTTL {
		a.clearPendingClarification(ctx, chatID)
		return pendingClarification{}, false
	}

	var pending pendingClarification
	if err := json.Unmarshal([]byte(setting.Value), &pending); err != nil {
		a.logWarn("invalid pending clarification payload", "chat_id", chatID, "error", err)
		a.clearPendingClarification(ctx, chatID)
		return pendingClarification{}, false
	}

	pending.SourceText = strings.TrimSpace(pending.SourceText)
	pending.Question = strings.TrimSpace(pending.Question)
	if pending.SourceText == "" || pending.Question == "" {
		a.clearPendingClarification(ctx, chatID)
		return pendingClarification{}, false
	}

	return pending, true
}

func (a *Agent) clearPendingClarification(ctx context.Context, chatID int64) {
	if err := a.repository.SetSetting(ctx, pendingClarificationKey(chatID), ""); err != nil {
		a.logError("clear pending clarification", "error", err, "chat_id", chatID)
	}
}

func (a *Agent) logWarn(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}

func (a *Agent) logInfo(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

func (a *Agent) logDebug(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Debug(msg, args...)
	}
}

func (a *Agent) logError(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Error(msg, args...)
	}
}

func (a *Agent) logRouteDecision(msg string, message telegram.IncomingMessage, decision router.Decision) {
	a.logDebug(
		msg,
		"message_text", shortTextForLog(utils.RedactSensitiveText(message.Text)),
		"chat_id", message.ChatID,
		"user_id", message.UserID,
		"mode", decision.Mode,
		"skill", decision.SkillName,
		"args", sanitizedArgs(decision.Args),
		"confidence", decision.Confidence,
		"needs_clarification", decision.NeedsClarification,
		"clarification_question", decision.ClarificationQuestion,
	)
}

func marshalArgs(args map[string]string) string {
	payload, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func sanitizedArgs(args map[string]string) map[string]string {
	return utils.RedactSensitiveArgs(args)
}

func callStatus(err error) string {
	if err != nil {
		return models.SkillCallFailed
	}
	return models.SkillCallSuccess
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func userMessageForError(err error) string {
	var userFacing skills.UserFacingError
	if errors.As(err, &userFacing) {
		return userFacing.UserMessage()
	}

	switch {
	case errors.Is(err, skills.ErrInvalidArguments),
		errors.Is(err, skills.ErrSkillNotFound),
		errors.Is(err, skills.ErrNotFound),
		errors.Is(err, skills.ErrAccessDenied),
		errors.Is(err, skills.ErrUnavailable):
		return err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	default:
		return "internal error"
	}
}

func shortTextForLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}

	runes := []rune(value)
	if len(runes) <= maxLoggedMessageChars {
		return value
	}

	return strings.TrimSpace(string(runes[:maxLoggedMessageChars])) + fmt.Sprintf("...(%d chars)", utf8.RuneCountInString(value))
}

func pendingClarificationKey(chatID int64) string {
	return "pending_clarification:" + strconv.FormatInt(chatID, 10)
}

func composeClarifiedText(pending pendingClarification, answer string) string {
	return fmt.Sprintf(
		"Previous request: %q\nClarification question: %q\nUser answer: %q",
		strings.TrimSpace(pending.SourceText),
		strings.TrimSpace(pending.Question),
		strings.TrimSpace(answer),
	)
}

func shouldUsePendingClarification(text string, decision router.Decision) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return false
	}

	if decision.ShouldClarify() || !decision.Matched() {
		return true
	}

	if decision.SkillName != "chat" {
		return false
	}

	return looksLikeClarificationAnswer(trimmed)
}

func withReplyPrefix(prefix, text string) string {
	prefix = strings.TrimSpace(prefix)
	text = strings.TrimSpace(text)
	switch {
	case prefix == "":
		return text
	case text == "":
		return prefix
	default:
		return prefix + "\n\n" + text
	}
}

func normalizeIncomingSource(source string, fromAudio bool) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source != "" {
		return source
	}
	if fromAudio {
		return "telegram_voice"
	}
	return "telegram"
}

func isMenuCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/menu", "menu", "/home", "home":
		return true
	}
	return false
}

// isBareSlashCommand reports whether the message is just `/skill_name`
// without any trailing arguments. Used to decide whether to hand off to
// the UI's pending-input flow instead of running the skill with empty args.
func isBareSlashCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	return len(strings.Fields(trimmed)) == 1
}

func looksLikeClarificationAnswer(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	for _, phrase := range []string{"thanks", "thank you", "спасибо", "благодарю"} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}

	normalized := semantic.Normalize(text)
	if normalized == "" {
		return false
	}

	for _, token := range strings.Fields(normalized) {
		if _, err := strconv.Atoi(token); err == nil {
			return true
		}
		switch token {
		case "yes", "да", "ага", "угу", "ok", "okay", "ок", "ладно", "sure", "confirm", "confirmed", "подтверждаю", "сделай", "давай", "continue", "go", "ahead":
			return true
		}
	}

	return false
}
