package watch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"openlight/internal/models"
	"openlight/internal/telegram"
)

const (
	ActionRestartServiceID = "restart_service"
	ActionShowLogsID       = "show_logs"
	ActionShowStatusID     = "show_status"
	ActionIgnoreAlertID    = "ignore_alert"
)

type Action struct {
	ID           string
	Label        string
	Target       string
	Mutating     bool
	Handler      ActionHandler
	ProgressText string
}

type ActionHandler func(context.Context, models.Watch, models.WatchIncident, Action, string) (ActionResult, error)

type ActionResult struct {
	Text   string
	Status string
}

func (s *Service) actionsForIncident(watch models.Watch, incident models.WatchIncident) []Action {
	var actions []Action
	switch watch.Kind {
	case models.WatchKindServiceDown:
		if watch.ReactionMode == models.WatchReactionAsk || watch.ReactionMode == models.WatchReactionAuto {
			actions = append(actions, Action{
				ID:           ActionRestartServiceID,
				Label:        "Restart",
				Target:       watch.Target,
				Mutating:     true,
				ProgressText: fmt.Sprintf("Restarting %s...", watch.Target),
				Handler:      s.handleRestartService,
			})
		}
		actions = append(actions,
			Action{
				ID:      ActionShowLogsID,
				Label:   "Logs",
				Target:  watch.Target,
				Handler: s.handleShowLogs,
			},
			Action{
				ID:      ActionShowStatusID,
				Label:   "Status",
				Target:  watch.Target,
				Handler: s.handleShowServiceStatus,
			},
		)
	default:
		actions = append(actions, Action{
			ID:      ActionShowStatusID,
			Label:   "Status",
			Target:  incident.WatchName,
			Handler: s.handleShowSystemStatus,
		})
	}

	actions = append(actions, Action{
		ID:      ActionIgnoreAlertID,
		Label:   "Ignore",
		Target:  incident.WatchName,
		Handler: s.handleIgnoreAlert,
	})
	return actions
}

func (s *Service) defaultActionID(watch models.Watch, incident models.WatchIncident) string {
	actions := s.actionsForIncident(watch, incident)
	for _, action := range actions {
		if action.Mutating {
			return action.ID
		}
	}
	if len(actions) > 0 {
		return actions[0].ID
	}
	return ""
}

func (s *Service) resolveAction(watch models.Watch, incident models.WatchIncident, actionID string) (Action, bool) {
	actionID = strings.TrimSpace(strings.ToLower(actionID))
	for _, action := range s.actionsForIncident(watch, incident) {
		if action.ID == actionID {
			return action, true
		}
	}
	return Action{}, false
}

func (s *Service) executeIncidentAction(
	ctx context.Context,
	watch models.Watch,
	incident models.WatchIncident,
	actionID string,
	reason string,
) (ActionResult, error) {
	action, ok := s.resolveAction(watch, incident, actionID)
	if !ok {
		return ActionResult{
			Text:   fmt.Sprintf("Unsupported action for alert #%d.", incident.ID),
			Status: models.WatchActionStatusFailed,
		}, nil
	}
	return action.Handler(ctx, watch, incident, action, reason)
}

func (s *Service) handleRestartService(
	ctx context.Context,
	watch models.Watch,
	_ models.WatchIncident,
	_ Action,
	reason string,
) (ActionResult, error) {
	result, err := s.executeSkill(ctx, "service_restart", map[string]string{"service": watch.Target}, watch.TelegramUserID, watch.TelegramChatID, "[alert restart service]")
	if err != nil {
		return ActionResult{
			Text:   fmt.Sprintf("Restart failed for %s: %s", watch.Target, err.Error()),
			Status: models.WatchActionStatusFailed,
		}, nil
	}

	statusResult, statusErr := s.executeSkill(ctx, "service_status", map[string]string{"service": watch.Target}, watch.TelegramUserID, watch.TelegramChatID, "[alert status after restart]")
	lines := []string{
		fmt.Sprintf("Action: restarted %s (%s).", watch.Target, reason),
		result.Text,
	}
	if statusErr != nil {
		lines = append(lines, "Post-action status check failed: "+statusErr.Error())
		return ActionResult{
			Text:   joinNonEmptyLines(lines...),
			Status: models.WatchActionStatusFailed,
		}, nil
	}
	lines = append(lines, statusResult.Text)
	return ActionResult{
		Text:   joinNonEmptyLines(lines...),
		Status: models.WatchActionStatusSucceeded,
	}, nil
}

func (s *Service) handleShowLogs(
	ctx context.Context,
	watch models.Watch,
	incident models.WatchIncident,
	_ Action,
	_ string,
) (ActionResult, error) {
	result, err := s.executeSkill(ctx, "service_logs", map[string]string{"service": watch.Target}, watch.TelegramUserID, watch.TelegramChatID, "[alert show logs]")
	if err != nil {
		return ActionResult{
			Text: fmt.Sprintf("Alert #%d\nCould not fetch logs for %s: %s", incident.ID, watch.Target, err.Error()),
		}, nil
	}
	return ActionResult{
		Text: fmt.Sprintf("Alert #%d\n%s", incident.ID, result.Text),
	}, nil
}

func (s *Service) handleShowServiceStatus(
	ctx context.Context,
	watch models.Watch,
	incident models.WatchIncident,
	_ Action,
	_ string,
) (ActionResult, error) {
	result, err := s.executeSkill(ctx, "service_status", map[string]string{"service": watch.Target}, watch.TelegramUserID, watch.TelegramChatID, "[alert show service status]")
	if err != nil {
		return ActionResult{
			Text: fmt.Sprintf("Alert #%d\nCould not fetch status for %s: %s", incident.ID, watch.Target, err.Error()),
		}, nil
	}
	return ActionResult{
		Text: fmt.Sprintf("Alert #%d\n%s", incident.ID, result.Text),
	}, nil
}

func (s *Service) handleShowSystemStatus(
	ctx context.Context,
	watch models.Watch,
	incident models.WatchIncident,
	_ Action,
	_ string,
) (ActionResult, error) {
	result, err := s.executeSkill(ctx, "status", map[string]string{}, watch.TelegramUserID, watch.TelegramChatID, "[alert show system status]")
	if err != nil {
		return ActionResult{
			Text: fmt.Sprintf("Alert #%d\nCould not fetch system status: %s", incident.ID, err.Error()),
		}, nil
	}
	return ActionResult{
		Text: fmt.Sprintf("Alert #%d\n%s", incident.ID, result.Text),
	}, nil
}

func (s *Service) handleIgnoreAlert(
	_ context.Context,
	watch models.Watch,
	incident models.WatchIncident,
	_ Action,
	_ string,
) (ActionResult, error) {
	return ActionResult{
		Text:   fmt.Sprintf("Ignoring alert #%d for %s. I will wait for the state to change before alerting again.", incident.ID, watch.Name),
		Status: models.WatchActionStatusDeclined,
	}, nil
}

func (s *Service) actionButtons(incidentID int64, actions []Action) [][]telegram.Button {
	if incidentID <= 0 || !s.supportsButtons() || len(actions) == 0 {
		return nil
	}

	var rows [][]telegram.Button
	var current []telegram.Button
	for _, action := range actions {
		button := telegram.Button{
			Text:         action.Label,
			CallbackData: fmt.Sprintf("watch:action:%d:%s", incidentID, action.ID),
		}
		if action.ID == ActionIgnoreAlertID {
			if len(current) > 0 {
				rows = append(rows, current)
				current = nil
			}
			rows = append(rows, []telegram.Button{button})
			continue
		}

		current = append(current, button)
		if len(current) == 3 {
			rows = append(rows, current)
			current = nil
		}
	}
	if len(current) > 0 {
		rows = append(rows, current)
	}
	return rows
}

func mutatingActionExpired(incident models.WatchIncident, action Action, now time.Time) bool {
	if !action.Mutating {
		return false
	}
	if incident.ActionExpiresAt.IsZero() {
		return false
	}
	return now.After(incident.ActionExpiresAt)
}
