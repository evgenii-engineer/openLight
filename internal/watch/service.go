package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
	servicepkg "openlight/internal/skills/services"
	systempkg "openlight/internal/skills/system"
	"openlight/internal/storage"
	"openlight/internal/telegram"
	"openlight/internal/utils"
)

type Notifier interface {
	SendText(ctx context.Context, chatID int64, text string) error
}

type buttonNotifier interface {
	SendTextWithButtons(ctx context.Context, chatID int64, text string, buttons [][]telegram.Button) error
}

type confirmationRequest struct {
	Decision   string
	IncidentID int64
	Explicit   bool
}

type actionRequest struct {
	ActionID   string
	IncidentID int64
}

type Options struct {
	PollInterval   time.Duration
	AskTTL         time.Duration
	RequestTimeout time.Duration
}

type Service struct {
	repository     storage.Repository
	registry       *skills.Registry
	notifier       Notifier
	systemProvider systempkg.Provider
	serviceManager servicepkg.Manager
	logger         *slog.Logger
	pollInterval   time.Duration
	askTTL         time.Duration
	requestTimeout time.Duration
}

type ProbeResult struct {
	Watch        models.Watch
	ConditionMet bool
	ConditionFor time.Duration
	Summary      string
	Details      string
	TriggerReady bool
	CooldownLeft time.Duration
	OpenIncident bool
}

type evaluation struct {
	condition bool
	summary   string
	details   string
}

func NewService(
	repository storage.Repository,
	registry *skills.Registry,
	notifier Notifier,
	systemProvider systempkg.Provider,
	serviceManager servicepkg.Manager,
	logger *slog.Logger,
	options Options,
) *Service {
	return &Service{
		repository:     repository,
		registry:       registry,
		notifier:       notifier,
		systemProvider: systemProvider,
		serviceManager: serviceManager,
		logger:         logger,
		pollInterval:   options.PollInterval,
		askTTL:         options.AskTTL,
		requestTimeout: options.RequestTimeout,
	}
}

func (s *Service) SetNotifier(notifier Notifier) {
	if s == nil {
		return
	}
	s.notifier = notifier
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil {
		return nil
	}

	if err := s.runCycle(ctx); err != nil {
		return normalizeWatchContextError(ctx, err)
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.runCycle(ctx); err != nil {
				return normalizeWatchContextError(ctx, err)
			}
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return normalizeWatchContextError(ctx, s.runCycle(ctx))
}

func normalizeWatchContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *Service) AddWatch(ctx context.Context, chatID, userID int64, raw string) (models.Watch, error) {
	spec, err := parseAddSpec(raw)
	if err != nil {
		return models.Watch{}, err
	}

	if spec.Kind == models.WatchKindServiceDown {
		if err := s.validateServiceTarget(ctx, spec.Target); err != nil {
			return models.Watch{}, err
		}
	}
	if err := validateSpec(spec); err != nil {
		return models.Watch{}, err
	}

	watch := models.Watch{
		TelegramUserID: userID,
		TelegramChatID: chatID,
		Name:           spec.Name,
		Kind:           spec.Kind,
		Target:         spec.Target,
		Threshold:      spec.Threshold,
		Duration:       spec.Duration,
		ReactionMode:   spec.ReactionMode,
		ActionType:     spec.ActionType,
		Cooldown:       spec.Cooldown,
		Enabled:        true,
		IncidentState:  models.WatchIncidentStateClear,
	}

	return s.repository.CreateWatch(ctx, watch)
}

func (s *Service) ListWatches(ctx context.Context, chatID int64) ([]models.Watch, error) {
	return s.repository.ListWatches(ctx, storage.WatchListOptions{ChatID: chatID})
}

func (s *Service) ToggleWatch(ctx context.Context, chatID, id int64) (models.Watch, error) {
	watch, ok, err := s.repository.GetWatch(ctx, id)
	if err != nil {
		return models.Watch{}, err
	}
	if !ok || watch.TelegramChatID != chatID {
		return models.Watch{}, fmt.Errorf("%w: watch #%d", skills.ErrNotFound, id)
	}

	watch.Enabled = !watch.Enabled
	if !watch.Enabled {
		watch.ConditionSince = time.Time{}
	}
	if err := s.repository.UpdateWatch(ctx, watch); err != nil {
		return models.Watch{}, err
	}
	return watch, nil
}

func (s *Service) DeleteWatch(ctx context.Context, chatID, id int64) (models.Watch, error) {
	watch, ok, err := s.repository.GetWatch(ctx, id)
	if err != nil {
		return models.Watch{}, err
	}
	if !ok || watch.TelegramChatID != chatID {
		return models.Watch{}, fmt.Errorf("%w: watch #%d", skills.ErrNotFound, id)
	}
	if err := s.repository.DeleteWatch(ctx, id); err != nil {
		return models.Watch{}, err
	}
	return watch, nil
}

func (s *Service) ListHistory(ctx context.Context, chatID, watchID int64, limit int) ([]models.WatchIncident, error) {
	return s.repository.ListWatchIncidents(ctx, storage.WatchIncidentListOptions{
		ChatID:  chatID,
		WatchID: watchID,
		Limit:   limit,
	})
}

func (s *Service) ProbeWatch(ctx context.Context, chatID, id int64) (ProbeResult, error) {
	watch, ok, err := s.repository.GetWatch(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	if !ok || watch.TelegramChatID != chatID {
		return ProbeResult{}, fmt.Errorf("%w: watch #%d", skills.ErrNotFound, id)
	}

	eval, err := s.evaluateWatch(ctx, watch)
	if err != nil {
		return ProbeResult{}, err
	}

	now := time.Now().UTC()
	conditionFor := time.Duration(0)
	if eval.condition && !watch.ConditionSince.IsZero() {
		conditionFor = now.Sub(watch.ConditionSince)
	}

	cooldownLeft := time.Duration(0)
	if !watch.LastTriggeredAt.IsZero() && watch.Cooldown > 0 {
		elapsed := now.Sub(watch.LastTriggeredAt)
		if elapsed < watch.Cooldown {
			cooldownLeft = watch.Cooldown - elapsed
		}
	}

	return ProbeResult{
		Watch:        watch,
		ConditionMet: eval.condition,
		ConditionFor: conditionFor,
		Summary:      eval.summary,
		Details:      eval.details,
		TriggerReady: eval.condition && conditionFor >= watch.Duration && cooldownLeft == 0,
		CooldownLeft: cooldownLeft,
		OpenIncident: watch.IncidentState == models.WatchIncidentStateOpen,
	}, nil
}

func (s *Service) HandleAction(ctx context.Context, chatID, userID int64, text string) (bool, error) {
	if request, ok := parseActionRequest(text); ok {
		return true, s.handleIncidentAction(ctx, chatID, userID, request)
	}

	request, ok := parseConfirmation(text)
	if !ok {
		return false, nil
	}

	now := time.Now().UTC()
	pending, err := s.repository.ListPendingWatchIncidents(ctx, chatID, now)
	if err != nil {
		return true, err
	}
	if len(pending) == 0 {
		if request.Explicit {
			return true, s.sendStaleConfirmationMessage(ctx, chatID, userID, request.IncidentID)
		}
		return false, nil
	}

	incident, err := resolvePendingIncident(pending, request.IncidentID)
	if err != nil {
		return true, s.sendAssistantMessage(ctx, chatID, userID, err.Error())
	}

	actionID := ActionIgnoreAlertID
	if request.Decision != "no" {
		watch, exists, getErr := s.repository.GetWatch(ctx, incident.WatchID)
		if getErr != nil {
			return true, getErr
		}
		if !exists {
			return true, s.sendAssistantMessage(ctx, chatID, userID, fmt.Sprintf("Watch for alert #%d no longer exists.", incident.ID))
		}
		actionID = s.defaultActionID(watch, incident)
	}

	return true, s.handleIncidentAction(ctx, chatID, userID, actionRequest{
		ActionID:   actionID,
		IncidentID: incident.ID,
	})
}

func (s *Service) handleIncidentAction(ctx context.Context, chatID, userID int64, request actionRequest) error {
	if request.IncidentID <= 0 {
		return s.sendAssistantMessage(ctx, chatID, userID, "Alert id is required.")
	}

	now := time.Now().UTC()
	incident, ok, err := s.repository.GetWatchIncident(ctx, request.IncidentID)
	if err != nil {
		return err
	}
	if !ok || incident.TelegramChatID != chatID {
		return s.sendStaleConfirmationMessage(ctx, chatID, userID, request.IncidentID)
	}

	watch, exists, err := s.repository.GetWatch(ctx, incident.WatchID)
	if err != nil {
		return err
	}
	if !exists {
		return s.sendAssistantMessage(ctx, chatID, userID, fmt.Sprintf("Watch for alert #%d no longer exists.", incident.ID))
	}

	action, ok := s.resolveAction(watch, incident, request.ActionID)
	if !ok {
		return s.sendAssistantMessage(ctx, chatID, userID, fmt.Sprintf("Alert #%d does not support that action.", incident.ID))
	}

	if incident.Status == models.WatchIncidentStatusResolved {
		return s.sendAssistantMessage(ctx, chatID, userID, staleIncidentMessage(incident))
	}
	if mutatingActionExpired(incident, action, now) {
		if incident.ActionStatus == models.WatchActionStatusPending {
			incident.ActionStatus = models.WatchActionStatusExpired
			incident.ActionCompletedAt = now
			incident.Report = joinNonEmptyLines(incident.Report, "Action window expired.")
			if err := s.repository.UpdateWatchIncident(ctx, incident); err != nil {
				return err
			}
		}
		return s.sendAssistantMessage(ctx, chatID, userID, staleIncidentMessage(incident))
	}
	if action.Mutating && incident.ActionStatus != "" &&
		incident.ActionStatus != models.WatchActionStatusNone &&
		incident.ActionStatus != models.WatchActionStatusPending {
		return s.sendAssistantMessage(ctx, chatID, userID, staleIncidentMessage(incident))
	}
	if action.ID == ActionIgnoreAlertID && incident.ActionStatus == models.WatchActionStatusDeclined {
		return s.sendAssistantMessage(ctx, chatID, userID, staleIncidentMessage(incident))
	}
	if action.Mutating && incident.ActionStatus == models.WatchActionStatusPending {
		incident.ActionStatus = models.WatchActionStatusRunning
		if err := s.repository.UpdateWatchIncident(ctx, incident); err != nil {
			return err
		}
	}

	if action.ProgressText != "" {
		if err := s.sendAssistantMessage(ctx, chatID, userID, action.ProgressText); err != nil {
			return err
		}
	}

	result, err := s.executeIncidentAction(ctx, watch, incident, action.ID, "from Telegram")
	if err != nil {
		latestIncident, ok, getErr := s.repository.GetWatchIncident(ctx, incident.ID)
		if getErr == nil && ok {
			latestIncident.ActionStatus = models.WatchActionStatusFailed
			latestIncident.ActionCompletedAt = now
			latestIncident.Report = joinNonEmptyLines(latestIncident.Report, "Action failed: "+err.Error())
			_ = s.repository.UpdateWatchIncident(ctx, latestIncident)
		}
		return err
	}

	switch action.ID {
	case ActionRestartServiceID, ActionIgnoreAlertID:
		latestIncident, ok, err := s.repository.GetWatchIncident(ctx, incident.ID)
		if err != nil {
			return err
		}
		if ok {
			incident = latestIncident
		}
		incident.ActionStatus = result.Status
		incident.ActionCompletedAt = now
		incident.Report = joinNonEmptyLines(incident.Report, result.Text)
		if err := s.repository.UpdateWatchIncident(ctx, incident); err != nil {
			return err
		}
	}

	return s.sendAssistantMessage(ctx, chatID, userID, result.Text)
}

func (s *Service) runCycle(ctx context.Context) error {
	now := time.Now().UTC()
	if err := s.expirePendingIncidents(ctx, now); err != nil {
		return err
	}

	watches, err := s.repository.ListWatches(ctx, storage.WatchListOptions{EnabledOnly: true})
	if err != nil {
		return err
	}

	for _, watch := range watches {
		if err := s.processWatch(ctx, watch, now); err != nil {
			s.logWarn("process watch", "watch_id", watch.ID, "error", err)
		}
	}

	return nil
}

func (s *Service) processWatch(ctx context.Context, watch models.Watch, now time.Time) error {
	evalCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	eval, err := s.evaluateWatch(evalCtx, watch)
	if err != nil {
		return err
	}

	watch.LastCheckedAt = now
	changed := true

	if eval.condition {
		if watch.ConditionSince.IsZero() {
			watch.ConditionSince = now
		}
		if watch.IncidentState == models.WatchIncidentStateOpen {
			return s.repository.UpdateWatch(evalCtx, watch)
		}
		if now.Sub(watch.ConditionSince) < watch.Duration {
			return s.repository.UpdateWatch(evalCtx, watch)
		}
		if inCooldown(watch, now) {
			return s.repository.UpdateWatch(evalCtx, watch)
		}

		watch.IncidentState = models.WatchIncidentStateOpen
		watch.LastTriggeredAt = now
		if err := s.repository.UpdateWatch(evalCtx, watch); err != nil {
			return err
		}

		incident := models.WatchIncident{
			WatchID:        watch.ID,
			TelegramChatID: watch.TelegramChatID,
			Summary:        eval.summary,
			Details:        eval.details,
			Status:         models.WatchIncidentStatusOpen,
			ReactionMode:   watch.ReactionMode,
			ActionType:     watch.ActionType,
			OpenedAt:       now,
		}

		switch watch.ReactionMode {
		case models.WatchReactionAsk:
			incident.ActionStatus = models.WatchActionStatusPending
			incident.ActionRequestedAt = now
			incident.ActionExpiresAt = now.Add(s.askTTL)
		default:
			incident.ActionStatus = models.WatchActionStatusNone
		}

		incident, err = s.repository.CreateWatchIncident(evalCtx, incident)
		if err != nil {
			return err
		}

		text := s.renderTriggeredAlert(incident, watch)
		actions := s.actionsForIncident(watch, incident)
		if watch.ReactionMode == models.WatchReactionAsk {
			incident.ActionPrompt = text
			if err := s.repository.UpdateWatchIncident(evalCtx, incident); err != nil {
				return err
			}
			buttons := s.actionButtons(incident.ID, actions)
			return s.sendAssistantMessage(ctx, watch.TelegramChatID, watch.TelegramUserID, text, buttons...)
		}

		if watch.ReactionMode == models.WatchReactionAuto {
			result, execErr := s.executeAction(ctx, watch, incident, "automatic recovery")
			if execErr != nil {
				return execErr
			}
			incident.ActionStatus = result.Status
			incident.ActionCompletedAt = now
			incident.Report = joinNonEmptyLines(incident.Report, result.Text)
			if err := s.repository.UpdateWatchIncident(evalCtx, incident); err != nil {
				return err
			}
			text = text + "\n" + result.Text
			return s.sendAssistantMessage(ctx, watch.TelegramChatID, watch.TelegramUserID, text)
		}

		buttons := s.actionButtons(incident.ID, actions)
		return s.sendAssistantMessage(ctx, watch.TelegramChatID, watch.TelegramUserID, text, buttons...)
	}

	if !watch.ConditionSince.IsZero() {
		watch.ConditionSince = time.Time{}
	}
	if watch.IncidentState == models.WatchIncidentStateOpen {
		incident, ok, getErr := s.repository.GetOpenWatchIncident(evalCtx, watch.ID)
		if getErr != nil {
			return getErr
		}
		if ok {
			latestIncident, exists, latestErr := s.repository.GetWatchIncident(evalCtx, incident.ID)
			if latestErr != nil {
				return latestErr
			}
			if exists {
				incident = latestIncident
			}
			if incident.ActionStatus == models.WatchActionStatusRunning {
				return s.repository.UpdateWatch(evalCtx, watch)
			}
			incident.Status = models.WatchIncidentStatusResolved
			incident.ResolvedAt = now
			if incident.ActionStatus == models.WatchActionStatusPending {
				incident.ActionStatus = models.WatchActionStatusExpired
				if incident.ActionCompletedAt.IsZero() {
					incident.ActionCompletedAt = now
				}
			}
			incident.Report = joinNonEmptyLines(incident.Report, "Condition resolved.")
			if err := s.repository.UpdateWatchIncident(evalCtx, incident); err != nil {
				return err
			}
			if err := s.sendAssistantMessage(
				ctx,
				watch.TelegramChatID,
				watch.TelegramUserID,
				fmt.Sprintf("Resolved #%d: %s", incident.ID, recoveryText(watch)),
			); err != nil {
				return err
			}
		}
		watch.IncidentState = models.WatchIncidentStateClear
	}
	if changed {
		return s.repository.UpdateWatch(evalCtx, watch)
	}
	return nil
}

func (s *Service) expirePendingIncidents(ctx context.Context, now time.Time) error {
	incidents, err := s.repository.ListExpiredPendingWatchIncidents(ctx, now)
	if err != nil {
		return err
	}

	for _, incident := range incidents {
		if incident.ActionStatus != models.WatchActionStatusPending {
			continue
		}
		incident.ActionStatus = models.WatchActionStatusExpired
		incident.ActionCompletedAt = now
		incident.Report = joinNonEmptyLines(incident.Report, "Action window expired.")
		if err := s.repository.UpdateWatchIncident(ctx, incident); err != nil {
			return err
		}
		if err := s.sendAssistantMessage(
			ctx,
			incident.TelegramChatID,
			0,
			fmt.Sprintf("Alert #%d expired: reply window closed for %s.", incident.ID, incident.WatchName),
		); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) evaluateWatch(ctx context.Context, watch models.Watch) (evaluation, error) {
	switch watch.Kind {
	case models.WatchKindServiceDown:
		info, err := s.serviceManager.Status(ctx, watch.Target)
		if err != nil {
			return evaluation{
				condition: true,
				summary:   fmt.Sprintf("%s status check failed", watch.Target),
				details:   err.Error(),
			}, nil
		}
		if serviceHealthy(info) {
			return evaluation{
				condition: false,
				summary:   fmt.Sprintf("%s is healthy", watch.Target),
				details:   renderServiceInfo(info),
			}, nil
		}
		return evaluation{
			condition: true,
			summary:   fmt.Sprintf("%s is down", watch.Target),
			details:   renderServiceInfo(info),
		}, nil
	case models.WatchKindCPUHigh:
		value, err := s.systemProvider.CPUUsage(ctx)
		if err != nil {
			return evaluation{}, err
		}
		return thresholdEvaluation("CPU", value, watch.Threshold), nil
	case models.WatchKindMemoryHigh:
		stats, err := s.systemProvider.MemoryStats(ctx)
		if err != nil {
			return evaluation{}, err
		}
		value := 0.0
		if stats.Total > 0 {
			value = (float64(stats.Used) / float64(stats.Total)) * 100
		}
		eval := thresholdEvaluation("Memory", value, watch.Threshold)
		eval.details = fmt.Sprintf("%s used / %s total", utils.FormatBytes(stats.Used), utils.FormatBytes(stats.Total))
		return eval, nil
	case models.WatchKindDiskHigh:
		path := watch.Target
		if path == "" {
			path = "/"
		}
		stats, err := s.systemProvider.DiskStats(ctx, path)
		if err != nil {
			return evaluation{}, err
		}
		value := 0.0
		if stats.Total > 0 {
			value = (float64(stats.Used) / float64(stats.Total)) * 100
		}
		eval := thresholdEvaluation("Disk "+path, value, watch.Threshold)
		eval.details = fmt.Sprintf("%s used / %s total", utils.FormatBytes(stats.Used), utils.FormatBytes(stats.Total))
		return eval, nil
	case models.WatchKindTemperatureHigh:
		value, err := s.systemProvider.Temperature(ctx)
		if err != nil {
			return evaluation{}, err
		}
		condition := value > watch.Threshold
		summary := fmt.Sprintf("Temperature is %.1fC", value)
		if condition {
			summary = fmt.Sprintf("Temperature is %.1fC (> %.1fC)", value, watch.Threshold)
		}
		return evaluation{
			condition: condition,
			summary:   summary,
			details:   fmt.Sprintf("Threshold: %.1fC", watch.Threshold),
		}, nil
	default:
		return evaluation{}, fmt.Errorf("%w: unsupported watch kind %q", skills.ErrInvalidArguments, watch.Kind)
	}
}

func (s *Service) executeAction(ctx context.Context, watch models.Watch, incident models.WatchIncident, reason string) (ActionResult, error) {
	actionID := s.defaultAutoActionID(watch)
	if actionID == "" {
		return ActionResult{
			Text:   "No automatic action configured.",
			Status: models.WatchActionStatusNone,
		}, nil
	}
	return s.executeIncidentAction(ctx, watch, incident, actionID, reason)
}

func (s *Service) defaultAutoActionID(watch models.Watch) string {
	switch strings.TrimSpace(strings.ToLower(watch.ActionType)) {
	case "", models.WatchActionNone:
		return ""
	case models.WatchActionServiceRestart, ActionRestartServiceID:
		return ActionRestartServiceID
	default:
		return ""
	}
}

func (s *Service) executeSkill(
	ctx context.Context,
	name string,
	args map[string]string,
	userID, chatID int64,
	inputText string,
) (skills.Result, error) {
	skill, ok := s.registry.Get(name)
	if !ok {
		return skills.Result{}, fmt.Errorf("%w: skill %s", skills.ErrSkillNotFound, name)
	}

	startedAt := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	result, err := skill.Execute(execCtx, skills.Input{
		RawText: inputText,
		Args:    args,
		UserID:  userID,
		ChatID:  chatID,
	})
	durationMS := time.Since(startedAt).Milliseconds()
	s.saveSkillCall(ctx, models.SkillCall{
		SkillName:  skill.Definition().Name,
		InputText:  inputText,
		ArgsJSON:   marshalArgs(args),
		Status:     callStatus(err),
		ErrorText:  errorText(err),
		DurationMS: durationMS,
	})
	return result, err
}

func (s *Service) sendAssistantMessage(ctx context.Context, chatID, userID int64, text string, buttons ...[]telegram.Button) error {
	if s.notifier == nil {
		return fmt.Errorf("%w: watch notifier is not configured", skills.ErrUnavailable)
	}

	var err error
	if len(buttons) > 0 {
		if sender, ok := s.notifier.(buttonNotifier); ok {
			err = sender.SendTextWithButtons(ctx, chatID, text, buttons)
		} else {
			err = s.notifier.SendText(ctx, chatID, text)
		}
	} else {
		err = s.notifier.SendText(ctx, chatID, text)
	}
	if err != nil {
		return err
	}
	if saveErr := s.repository.SaveMessage(ctx, models.Message{
		TelegramUserID: userID,
		TelegramChatID: chatID,
		Role:           models.RoleAssistant,
		Text:           text,
	}); saveErr != nil {
		s.logWarn("save proactive watch message", "chat_id", chatID, "error", saveErr)
	}
	return nil
}

func (s *Service) saveSkillCall(ctx context.Context, call models.SkillCall) {
	if err := s.repository.SaveSkillCall(ctx, call); err != nil {
		s.logWarn("save watch skill call", "skill", call.SkillName, "error", err)
	}
}

func (s *Service) validateServiceTarget(_ context.Context, target string) error {
	for _, service := range s.serviceManager.Targets() {
		if strings.EqualFold(service.Name, target) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", skills.ErrAccessDenied, target)
}

func (s *Service) renderTriggeredAlert(incident models.WatchIncident, watch models.Watch) string {
	lines := []string{
		fmt.Sprintf("Alert #%d", incident.ID),
		incident.Summary,
	}
	if incident.Details != "" {
		lines = append(lines, incident.Details)
	}
	actions := s.actionsForIncident(watch, incident)
	switch watch.ReactionMode {
	case models.WatchReactionAsk:
		if s.supportsButtons() {
			lines = append(lines, "Choose an action below.")
		} else {
			lines = append(lines, fmt.Sprintf("Reply yes %d to act or no %d to ignore.", incident.ID, incident.ID))
		}
	case models.WatchReactionNotify:
		if len(actions) > 0 && s.supportsButtons() {
			lines = append(lines, "Quick actions are available below.")
		}
	case models.WatchReactionAuto:
		lines = append(lines, "Automatic recovery is enabled for this alert.")
	}
	return joinNonEmptyLines(lines...)
}

func renderServiceInfo(info servicepkg.Info) string {
	lines := []string{
		fmt.Sprintf("Active: %s", emptyOrUnknown(info.ActiveState)),
		fmt.Sprintf("Sub: %s", emptyOrUnknown(info.SubState)),
		fmt.Sprintf("Description: %s", emptyOrUnknown(info.Description)),
	}
	if strings.TrimSpace(info.Host) != "" {
		lines = append([]string{"Host: " + info.Host}, lines...)
	}
	return strings.Join(lines, "\n")
}

func serviceHealthy(info servicepkg.Info) bool {
	active := strings.ToLower(strings.TrimSpace(info.ActiveState))
	sub := strings.ToLower(strings.TrimSpace(info.SubState))
	if active != "active" {
		return false
	}
	for _, marker := range []string{"unhealthy", "dead", "failed", "exited"} {
		if strings.Contains(sub, marker) {
			return false
		}
	}
	return true
}

func thresholdEvaluation(name string, value float64, threshold float64) evaluation {
	rounded := roundFloat(value, 1)
	condition := rounded > threshold
	summary := fmt.Sprintf("%s is %.1f%%", name, rounded)
	if condition {
		summary = fmt.Sprintf("%s is %.1f%% (> %.1f%%)", name, rounded, threshold)
	}
	return evaluation{
		condition: condition,
		summary:   summary,
		details:   fmt.Sprintf("Threshold: %.1f%%", threshold),
	}
}

func validateSpec(spec addSpec) error {
	if spec.Duration <= 0 {
		return fmt.Errorf("%w: duration must be greater than zero", skills.ErrInvalidArguments)
	}
	if spec.Cooldown < 0 {
		return fmt.Errorf("%w: cooldown must not be negative", skills.ErrInvalidArguments)
	}
	switch spec.Kind {
	case models.WatchKindCPUHigh, models.WatchKindMemoryHigh, models.WatchKindDiskHigh:
		if spec.Threshold <= 0 || spec.Threshold > 100 {
			return fmt.Errorf("%w: threshold must be between 0 and 100", skills.ErrInvalidArguments)
		}
	case models.WatchKindTemperatureHigh:
		if spec.Threshold <= 0 {
			return fmt.Errorf("%w: threshold must be greater than zero", skills.ErrInvalidArguments)
		}
	}
	return nil
}

func inCooldown(watch models.Watch, now time.Time) bool {
	if watch.Cooldown <= 0 || watch.LastTriggeredAt.IsZero() {
		return false
	}
	return now.Sub(watch.LastTriggeredAt) < watch.Cooldown
}

func recoveryText(watch models.Watch) string {
	switch watch.Kind {
	case models.WatchKindServiceDown:
		return watch.Target + " is healthy again."
	case models.WatchKindCPUHigh:
		return "CPU is back below threshold."
	case models.WatchKindMemoryHigh:
		return "Memory usage is back below threshold."
	case models.WatchKindDiskHigh:
		return "Disk usage is back below threshold."
	case models.WatchKindTemperatureHigh:
		return "Temperature is back below threshold."
	default:
		return "Condition is back to normal."
	}
}

func parseConfirmation(text string) (confirmationRequest, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return confirmationRequest{}, false
	}
	if request, ok := parseCallbackConfirmation(trimmed); ok {
		return request, true
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return confirmationRequest{}, false
	}

	switch fields[0] {
	case "yes", "y", "да":
		return confirmationRequest{Decision: "yes", IncidentID: parseOptionalID(fields[1:])}, true
	case "no", "n", "нет":
		return confirmationRequest{Decision: "no", IncidentID: parseOptionalID(fields[1:])}, true
	default:
		return confirmationRequest{}, false
	}
}

func parseCallbackConfirmation(text string) (confirmationRequest, bool) {
	if !strings.HasPrefix(text, "watch:") {
		return confirmationRequest{}, false
	}

	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return confirmationRequest{}, false
	}

	switch parts[1] {
	case "yes", "no":
	default:
		return confirmationRequest{}, false
	}

	id, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	if err != nil || id <= 0 {
		return confirmationRequest{}, false
	}

	return confirmationRequest{
		Decision:   parts[1],
		IncidentID: id,
		Explicit:   true,
	}, true
}

func parseActionRequest(text string) (actionRequest, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "watch:action:") {
		return actionRequest{}, false
	}

	parts := strings.Split(trimmed, ":")
	if len(parts) != 4 {
		return actionRequest{}, false
	}

	id, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	if err != nil || id <= 0 {
		return actionRequest{}, false
	}

	actionID := strings.TrimSpace(parts[3])
	if actionID == "" {
		return actionRequest{}, false
	}

	return actionRequest{
		ActionID:   actionID,
		IncidentID: id,
	}, true
}

func parseOptionalID(fields []string) int64 {
	if len(fields) == 0 {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(fields[0], "#"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func resolvePendingIncident(incidents []models.WatchIncident, id int64) (models.WatchIncident, error) {
	if id != 0 {
		for _, incident := range incidents {
			if incident.ID == id {
				return incident, nil
			}
		}
		return models.WatchIncident{}, fmt.Errorf("No pending alert with id %d.", id)
	}
	if len(incidents) == 1 {
		return incidents[0], nil
	}

	lines := []string{"Multiple watch actions are waiting:"}
	for _, incident := range incidents {
		lines = append(lines, fmt.Sprintf("- #%d %s", incident.ID, incident.WatchName))
	}
	lines = append(lines, "Reply yes <id> or no <id>.")
	return models.WatchIncident{}, errors.New(strings.Join(lines, "\n"))
}

func (s *Service) supportsButtons() bool {
	if s == nil || s.notifier == nil {
		return false
	}
	_, ok := s.notifier.(buttonNotifier)
	return ok
}

func (s *Service) sendStaleConfirmationMessage(ctx context.Context, chatID, userID, incidentID int64) error {
	if incidentID > 0 {
		incident, ok, err := s.repository.GetWatchIncident(ctx, incidentID)
		if err != nil {
			return err
		}
		if ok && incident.TelegramChatID == chatID {
			return s.sendAssistantMessage(ctx, chatID, userID, staleIncidentMessage(incident))
		}
		return s.sendAssistantMessage(ctx, chatID, userID, fmt.Sprintf("Alert #%d is no longer pending.", incidentID))
	}
	return s.sendAssistantMessage(ctx, chatID, userID, "No pending watch actions right now.")
}

func staleIncidentMessage(incident models.WatchIncident) string {
	switch incident.ActionStatus {
	case models.WatchActionStatusRunning:
		return fmt.Sprintf("Alert #%d is already being handled.", incident.ID)
	case models.WatchActionStatusSucceeded:
		return fmt.Sprintf("Alert #%d was already completed.", incident.ID)
	case models.WatchActionStatusDeclined:
		return fmt.Sprintf("Alert #%d was already declined.", incident.ID)
	case models.WatchActionStatusExpired:
		return fmt.Sprintf("Alert #%d already expired.", incident.ID)
	case models.WatchActionStatusFailed:
		return fmt.Sprintf("Alert #%d was already attempted and failed.", incident.ID)
	}
	if incident.Status == models.WatchIncidentStatusResolved {
		return fmt.Sprintf("Alert #%d is already resolved.", incident.ID)
	}
	return fmt.Sprintf("Alert #%d is no longer pending.", incident.ID)
}

func marshalArgs(args map[string]string) string {
	if len(args) == 0 {
		return "{}"
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(payload)
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

func joinNonEmptyLines(lines ...string) string {
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		trimmed = append(trimmed, line)
	}
	return strings.Join(trimmed, "\n")
}

func emptyOrUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func roundFloat(value float64, digits int) float64 {
	pow := math.Pow10(digits)
	return math.Round(value*pow) / pow
}

func (s *Service) logWarn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}
