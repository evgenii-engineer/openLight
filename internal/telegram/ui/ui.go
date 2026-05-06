package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"openlight/internal/skills"
	"openlight/internal/telegram"
	"openlight/internal/telegram/ui/callback"
	"openlight/internal/telegram/ui/keyboards"
	"openlight/internal/telegram/ui/render"
	"openlight/internal/telegram/ui/sessions"
)

// Transport is the slice of telegram.Bot the UI layer needs.
type Transport interface {
	SendText(ctx context.Context, chatID int64, text string) error
	SendTextWithButtons(ctx context.Context, chatID int64, text string, buttons [][]telegram.Button) error
	SendTextWithReplyKeyboard(ctx context.Context, chatID int64, text string, rows [][]string, persistent bool) error
	EditMessageText(ctx context.Context, chatID, messageID int64, text string, buttons [][]telegram.Button) error
}

// QuickAction binds a Telegram label to a pre-configured registered skill call.
type QuickAction struct {
	ID        string
	Label     string
	SkillName string
	Args      map[string]string
	Confirm   bool
	Prompt    string
}

// Config wires the UI layer.
type Config struct {
	Registry     *skills.Registry
	Transport    Transport
	Sessions     *sessions.Store
	QuickActions []QuickAction
	Logger       *slog.Logger
}

// UI orchestrates the button-driven Telegram surface.
type UI struct {
	reg          *skills.Registry
	transport    Transport
	sessions     *sessions.Store
	router       *callback.Router
	logger       *slog.Logger
	quickActions map[string]QuickAction
	quickOrder   []string

	freeChatMu sync.RWMutex
	freeChat   map[int64]struct{}
}

func New(cfg Config) *UI {
	if cfg.Sessions == nil {
		cfg.Sessions = sessions.NewStore(0)
	}
	u := &UI{
		reg:          cfg.Registry,
		transport:    cfg.Transport,
		sessions:     cfg.Sessions,
		logger:       cfg.Logger,
		quickActions: make(map[string]QuickAction, len(cfg.QuickActions)),
		freeChat:     make(map[int64]struct{}),
	}
	for _, qa := range cfg.QuickActions {
		id := strings.TrimSpace(qa.ID)
		if id == "" {
			continue
		}
		if _, exists := u.quickActions[id]; exists {
			continue
		}
		u.quickActions[id] = qa
		u.quickOrder = append(u.quickOrder, id)
	}

	r := callback.NewRouter()
	r.On(callback.KindHome, u.openHome)
	r.On(callback.KindGroup, u.openGroup)
	r.On(callback.KindSkill, u.execSkillCallback)
	r.On(callback.KindAction, u.contextualAction)
	r.On(callback.KindBack, u.navigateBack)
	r.On(callback.KindPage, u.paginate)
	r.On(callback.KindConfirm, u.confirmMutation)
	r.On(callback.KindCancel, u.cancelMutation)
	r.On(callback.KindQuick, u.runQuickAction)
	r.Fallback(u.fallback)
	u.router = r
	return u
}

// HandleCallback dispatches a callback_query through the UI router.
func (u *UI) HandleCallback(ctx context.Context, msg telegram.IncomingMessage) error {
	cm := callback.Message{
		ChatID:    msg.ChatID,
		UserID:    msg.UserID,
		MessageID: msg.MessageID,
		Raw:       msg.Text,
		Source:    msg.Source,
	}
	return u.router.Dispatch(ctx, cm)
}

// HasPendingInput reports whether a conversational input flow is active.
func (u *UI) HasPendingInput(chatID int64) bool {
	_, ok := u.sessions.Pending(chatID)
	return ok
}

// CancelPending clears any in-progress conversational input flow for the chat.
// Used when the user taps a reply-keyboard label or sends /menu — those should
// always escape an active flow, not be consumed as field values.
func (u *UI) CancelPending(chatID int64) {
	u.sessions.ClearInput(chatID)
}

// HandlePendingInput consumes one user message as the next input field of an
// active flow. Returns true when the message was consumed by the UI.
func (u *UI) HandlePendingInput(ctx context.Context, msg telegram.IncomingMessage) (bool, error) {
	flow, ok := u.sessions.Pending(msg.ChatID)
	if !ok {
		return false, nil
	}
	skill, ok := u.reg.Get(flow.SkillName)
	if !ok {
		u.sessions.ClearInput(msg.ChatID)
		return true, u.transport.SendText(ctx, msg.ChatID, "session expired")
	}
	hints := skills.DescribeUI(skill)
	if flow.StepIndex >= len(hints.Inputs) {
		u.sessions.ClearInput(msg.ChatID)
		return true, nil
	}
	field := hints.Inputs[flow.StepIndex]
	value := strings.TrimSpace(msg.Text)
	if field.Validate != nil {
		if err := field.Validate(value); err != nil {
			return true, u.transport.SendText(ctx, msg.ChatID,
				fmt.Sprintf("invalid input for %s: %s", field.Name, err.Error()))
		}
	}
	advanced, ok := u.sessions.AdvanceInput(msg.ChatID, field.Name, value)
	if !ok {
		return true, nil
	}
	if advanced.StepIndex < len(hints.Inputs) {
		next := hints.Inputs[advanced.StepIndex]
		return true, u.transport.SendText(ctx, msg.ChatID, prompt(next))
	}
	args := advanced.Collected
	u.sessions.ClearInput(msg.ChatID)
	cm := callback.Message{ChatID: msg.ChatID, UserID: msg.UserID}
	return true, u.runSkill(ctx, cm, skill, args, false)
}

// MapReplyKeyboard maps a tap on the persistent reply keyboard to a screen
// open. Returns true if the message should be intercepted.
func (u *UI) MapReplyKeyboard(text string) (string, bool) {
	switch strings.TrimSpace(text) {
	case "Skills":
		return "groups", true
	case "Quick Actions":
		return "quick", true
	case "System":
		return "g:system", true
	case "Watches":
		return "g:watch", true
	case "Services":
		return "g:services", true
	case "AI":
		return "chat", true
	}
	return "", false
}

// IsFreeChat reports whether the chat is currently in free-chat mode (any
// inbound text routes straight to the chat skill, bypassing the router).
func (u *UI) IsFreeChat(chatID int64) bool {
	u.freeChatMu.RLock()
	_, ok := u.freeChat[chatID]
	u.freeChatMu.RUnlock()
	return ok
}

// SetFreeChat toggles free-chat mode for the given chat.
func (u *UI) SetFreeChat(chatID int64, enabled bool) {
	u.freeChatMu.Lock()
	if enabled {
		u.freeChat[chatID] = struct{}{}
	} else {
		delete(u.freeChat, chatID)
	}
	u.freeChatMu.Unlock()
}

// OpenScreen sends a fresh message for one of the named screens. Used when
// the user taps a reply-keyboard label or runs /menu.
func (u *UI) OpenScreen(ctx context.Context, chatID int64, screen string) error {
	switch {
	case screen == "groups":
		u.SetFreeChat(chatID, false)
		return u.transport.SendTextWithButtons(ctx, chatID,
			"Available skill groups:", keyboards.GroupsMenu(u.reg))
	case screen == "quick":
		u.SetFreeChat(chatID, false)
		return u.transport.SendTextWithButtons(ctx, chatID,
			"Quick Actions:", keyboards.QuickActionsMenu(u.quickActionMetas()))
	case screen == "chat":
		u.SetFreeChat(chatID, true)
		return u.transport.SendText(ctx, chatID,
			"💬 Chat mode on. Just type — I'll reply via LLM. Tap any other section or send /menu to exit.")
	case strings.HasPrefix(screen, "g:"):
		u.SetFreeChat(chatID, false)
		key := strings.TrimPrefix(screen, "g:")
		group, _, ok := u.findGroup(key)
		if !ok {
			return u.transport.SendTextWithButtons(ctx, chatID,
				"Unknown group.", keyboards.GroupsMenu(u.reg))
		}
		return u.transport.SendTextWithButtons(ctx, chatID,
			render.GroupCard(group), keyboards.GroupMenu(u.reg, key, 0))
	case screen == "home":
		u.SetFreeChat(chatID, false)
		return u.SendHome(ctx, chatID)
	default:
		u.SetFreeChat(chatID, false)
		return u.transport.SendTextWithButtons(ctx, chatID,
			"Available skill groups:", keyboards.GroupsMenu(u.reg))
	}
}

// SendHome installs the root reply keyboard with a short welcome.
func (u *UI) SendHome(ctx context.Context, chatID int64) error {
	return u.transport.SendTextWithReplyKeyboard(ctx, chatID,
		"openLight ready. Tap a section below.",
		keyboards.RootReply(),
		true,
	)
}

// ----- callback handlers -------------------------------------------------

func (u *UI) openHome(ctx context.Context, m callback.Message, _ callback.Action) error {
	return u.editText(ctx, m, "Available skill groups:", keyboards.GroupsMenu(u.reg))
}

func (u *UI) openGroup(ctx context.Context, m callback.Message, a callback.Action) error {
	group, _, ok := u.findGroup(a.Target)
	if !ok {
		return u.editText(ctx, m, "Unknown group.", keyboards.GroupsMenu(u.reg))
	}
	return u.editText(ctx, m, render.GroupCard(group),
		keyboards.GroupMenu(u.reg, group.Key, 0))
}

func (u *UI) execSkillCallback(ctx context.Context, m callback.Message, a callback.Action) error {
	skill, ok := u.reg.Get(a.Target)
	if !ok {
		return u.editText(ctx, m, "Skill not found.", keyboards.GroupsMenu(u.reg))
	}

	args := map[string]string{}
	if a.Extra != "" {
		if loaded, ok := u.sessions.LoadArgs(a.Extra); ok {
			args = loaded
		}
	}
	return u.startSkill(ctx, m, skill, args)
}

func (u *UI) contextualAction(ctx context.Context, m callback.Message, a callback.Action) error {
	switch a.Target {
	case "refresh":
		skill, ok := u.reg.Get(a.Extra)
		if !ok {
			return u.editText(ctx, m, "Skill not found.", keyboards.GroupsMenu(u.reg))
		}
		return u.runSkill(ctx, m, skill, map[string]string{}, true)
	case "logs":
		skill, ok := u.reg.Get("service_logs")
		if !ok {
			return u.editText(ctx, m, "Logs are not available.", nil)
		}
		return u.runSkill(ctx, m, skill, map[string]string{"service": a.Extra}, true)
	case "restart":
		skill, ok := u.reg.Get("service_restart")
		if !ok {
			return u.editText(ctx, m, "Restart is not available.", nil)
		}
		return u.confirmAndRun(ctx, m, skill,
			map[string]string{"service": a.Extra},
			fmt.Sprintf("Restart service %q?", a.Extra),
		)
	case "watch":
		skill, ok := u.reg.Get("watch_add")
		if !ok {
			return u.editText(ctx, m, "Watch is not available.", nil)
		}
		return u.confirmAndRun(ctx, m, skill,
			map[string]string{"spec": a.Extra},
			fmt.Sprintf("Add watch %q?", a.Extra),
		)
	default:
		return u.editText(ctx, m, "Unsupported action.", keyboards.GroupsMenu(u.reg))
	}
}

func (u *UI) navigateBack(ctx context.Context, m callback.Message, a callback.Action) error {
	switch a.Target {
	case "", "groups":
		return u.editText(ctx, m, "Available skill groups:", keyboards.GroupsMenu(u.reg))
	case "g":
		key := strings.TrimSpace(a.Extra)
		if key == "" {
			return u.editText(ctx, m, "Available skill groups:", keyboards.GroupsMenu(u.reg))
		}
		return u.openGroup(ctx, m, callback.Action{Kind: callback.KindGroup, Target: key})
	default:
		return u.openHome(ctx, m, callback.Action{Kind: callback.KindHome})
	}
}

func (u *UI) paginate(ctx context.Context, m callback.Message, a callback.Action) error {
	groupKey := strings.TrimSpace(a.Target)
	group, _, ok := u.findGroup(groupKey)
	if !ok {
		return u.editText(ctx, m, "Unknown group.", keyboards.GroupsMenu(u.reg))
	}
	return u.editText(ctx, m, render.GroupCard(group),
		keyboards.GroupMenu(u.reg, groupKey, a.PageNumber()))
}

func (u *UI) confirmMutation(ctx context.Context, m callback.Message, a callback.Action) error {
	pending, ok := u.sessions.ClaimMutation(a.Target, m.UserID)
	if !ok {
		return u.editText(ctx, m, "Confirmation expired.", keyboards.GroupsMenu(u.reg))
	}
	skill, ok := u.reg.Get(pending.SkillName)
	if !ok {
		return u.editText(ctx, m, "Skill no longer available.", keyboards.GroupsMenu(u.reg))
	}
	return u.runSkill(ctx, m, skill, pending.Args, true)
}

func (u *UI) cancelMutation(ctx context.Context, m callback.Message, a callback.Action) error {
	u.sessions.CancelMutation(a.Target, m.UserID)
	return u.editText(ctx, m, "Cancelled.", keyboards.GroupsMenu(u.reg))
}

func (u *UI) runQuickAction(ctx context.Context, m callback.Message, a callback.Action) error {
	qa, ok := u.quickActions[a.Target]
	if !ok {
		return u.editText(ctx, m, "Unknown quick action.",
			keyboards.QuickActionsMenu(u.quickActionMetas()))
	}
	skill, ok := u.reg.Get(qa.SkillName)
	if !ok {
		return u.editText(ctx, m, "Quick action skill not registered.",
			keyboards.QuickActionsMenu(u.quickActionMetas()))
	}
	args := copyArgs(qa.Args)
	if qa.Confirm {
		prompt := strings.TrimSpace(qa.Prompt)
		if prompt == "" {
			prompt = fmt.Sprintf("Run quick action %q?", qa.Label)
		}
		return u.confirmAndRun(ctx, m, skill, args, prompt)
	}
	return u.runSkill(ctx, m, skill, args, true)
}

func (u *UI) fallback(ctx context.Context, m callback.Message, a callback.Action) error {
	if u.logger != nil {
		u.logger.Warn("unhandled callback", "kind", a.Kind, "target", a.Target, "extra", a.Extra)
	}
	return u.editText(ctx, m, "That action is no longer available.",
		keyboards.GroupsMenu(u.reg))
}

// ----- skill execution helpers -------------------------------------------

// startSkill chooses between confirm prompt, conversational input flow, and
// direct execution based on the skill's metadata.
func (u *UI) startSkill(ctx context.Context, m callback.Message, skill skills.Skill, args map[string]string) error {
	def := skill.Definition()
	hints := skills.DescribeUI(skill)

	missing := missingInputs(hints, args)
	if len(missing) > 0 {
		flow := u.sessions.StartInput(m.ChatID, def.Name, "g:"+def.Group.Key)
		flow.Collected = copyArgs(args)
		flow.StepIndex = 0
		// pre-fill the step index past any args already provided
		for _, field := range hints.Inputs {
			if _, ok := flow.Collected[field.Name]; ok {
				flow.StepIndex++
				continue
			}
			break
		}
		field := hints.Inputs[flow.StepIndex]
		return u.editText(ctx, m, prompt(field), keyboards.CancelOnly(callback.Action{
			Kind:   callback.KindBack,
			Target: "g",
			Extra:  def.Group.Key,
		}))
	}

	if def.Mutating || strings.TrimSpace(hints.Confirm) != "" {
		message := strings.TrimSpace(hints.Confirm)
		if message == "" {
			message = fmt.Sprintf("Run %s?", humanReadable(def.Name))
		}
		return u.confirmAndRun(ctx, m, skill, args, message)
	}
	return u.runSkill(ctx, m, skill, args, true)
}

func (u *UI) confirmAndRun(ctx context.Context, m callback.Message, skill skills.Skill, args map[string]string, prompt string) error {
	token := u.sessions.StoreMutation(sessions.PendingMutation{
		SkillName: skill.Definition().Name,
		Args:      copyArgs(args),
		UserID:    m.UserID,
	})
	return u.editText(ctx, m, prompt, keyboards.ConfirmKeyboard(token))
}

// runSkill executes a skill and renders the result. When edit is true, the
// existing message is updated; otherwise a new message is sent.
func (u *UI) runSkill(ctx context.Context, m callback.Message, skill skills.Skill, args map[string]string, edit bool) error {
	def := skill.Definition()
	hints := skills.DescribeUI(skill)

	if edit && m.MessageID != 0 {
		_ = u.transport.EditMessageText(ctx, m.ChatID, m.MessageID,
			"⏳ "+humanReadable(def.Name)+"…", nil)
	}

	res, err := skill.Execute(ctx, skills.Input{
		Args:   args,
		UserID: m.UserID,
		ChatID: m.ChatID,
		Source: "telegram_callback",
	})
	if err != nil {
		text := render.ErrorCard(def, userErrorMessage(err))
		return u.deliver(ctx, m, text, keyboards.SkillFollowUps(def, hints), edit)
	}

	body := strings.TrimSpace(res.Text)
	rows := mergeRows(res.Buttons, keyboards.SkillFollowUps(def, hints))
	return u.deliver(ctx, m, render.SkillCard(def, body), rows, edit)
}

func (u *UI) deliver(ctx context.Context, m callback.Message, text string, rows [][]telegram.Button, edit bool) error {
	if edit && m.MessageID != 0 {
		return u.transport.EditMessageText(ctx, m.ChatID, m.MessageID, text, rows)
	}
	if len(rows) == 0 {
		return u.transport.SendText(ctx, m.ChatID, text)
	}
	return u.transport.SendTextWithButtons(ctx, m.ChatID, text, rows)
}

func (u *UI) editText(ctx context.Context, m callback.Message, text string, rows [][]telegram.Button) error {
	if m.MessageID == 0 {
		if len(rows) == 0 {
			return u.transport.SendText(ctx, m.ChatID, text)
		}
		return u.transport.SendTextWithButtons(ctx, m.ChatID, text, rows)
	}
	return u.transport.EditMessageText(ctx, m.ChatID, m.MessageID, text, rows)
}

func (u *UI) findGroup(key string) (skills.Group, []skills.Definition, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return skills.Group{}, nil, false
	}
	for _, g := range u.reg.ListGroups() {
		if g.Key == key {
			return g, u.reg.ListByGroup(key), true
		}
	}
	return skills.Group{}, nil, false
}

func (u *UI) quickActionMetas() []keyboards.QuickActionMeta {
	out := make([]keyboards.QuickActionMeta, 0, len(u.quickOrder))
	for _, id := range u.quickOrder {
		qa := u.quickActions[id]
		out = append(out, keyboards.QuickActionMeta{ID: id, Label: qa.Label})
	}
	return out
}

// ----- helpers ------------------------------------------------------------

func missingInputs(hints skills.UIDescriptor, args map[string]string) []skills.InputField {
	missing := make([]skills.InputField, 0, len(hints.Inputs))
	for _, field := range hints.Inputs {
		if _, ok := args[field.Name]; ok {
			continue
		}
		missing = append(missing, field)
	}
	return missing
}

func mergeRows(a, b [][]telegram.Button) [][]telegram.Button {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([][]telegram.Button, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func copyArgs(args map[string]string) map[string]string {
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func prompt(field skills.InputField) string {
	if strings.TrimSpace(field.Prompt) != "" {
		return field.Prompt
	}
	return "Send " + field.Name
}

func humanReadable(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	parts := strings.Fields(name)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func userErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var ufe skills.UserFacingError
	if errors.As(err, &ufe) {
		return ufe.UserMessage()
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
