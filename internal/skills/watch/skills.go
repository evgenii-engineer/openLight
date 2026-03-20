package watch

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"openlight/internal/models"
	"openlight/internal/skills"
	watchengine "openlight/internal/watch"
)

type addSkill struct {
	service *watchengine.Service
}

func NewAddSkill(service *watchengine.Service) skills.Skill {
	return &addSkill{service: service}
}

func (s *addSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_add",
		Group:       skills.GroupWatch,
		Description: "Create a watch rule for an allowlisted service or a local system metric.",
		Aliases:     []string{"watch create"},
		Usage:       "/watch add <rule>",
		Examples: []string{
			"/watch add service tailscale ask for 30s cooldown 10m",
			"/watch add cpu > 90% for 5m cooldown 15m",
			"/watch add disk / > 90% for 3m",
		},
		Mutating: true,
	}
}

func (s *addSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	watch, err := s.service.AddWatch(ctx, input.ChatID, input.UserID, input.Args["spec"])
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "Watch created:\n" + renderWatch(watch)}, nil
}

type listSkill struct {
	service *watchengine.Service
}

func NewListSkill(service *watchengine.Service) skills.Skill {
	return &listSkill{service: service}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_list",
		Group:       skills.GroupWatch,
		Description: "List watch rules for the current chat.",
		Aliases:     []string{"watches"},
		Usage:       "/watch list",
	}
}

func (s *listSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	watches, err := s.service.ListWatches(ctx, input.ChatID)
	if err != nil {
		return skills.Result{}, err
	}
	if len(watches) == 0 {
		return skills.Result{Text: "No watches configured."}, nil
	}

	lines := []string{"Watches:"}
	for _, watch := range watches {
		lines = append(lines, "- "+singleLineWatch(watch))
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type pauseSkill struct {
	service *watchengine.Service
}

func NewPauseSkill(service *watchengine.Service) skills.Skill {
	return &pauseSkill{service: service}
}

func (s *pauseSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_pause",
		Group:       skills.GroupWatch,
		Description: "Toggle one watch between enabled and paused.",
		Aliases:     []string{"watch toggle"},
		Usage:       "/watch pause <id>",
		Mutating:    true,
	}
}

func (s *pauseSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	id, err := parseID(input.Args["id"])
	if err != nil {
		return skills.Result{}, err
	}
	watch, err := s.service.ToggleWatch(ctx, input.ChatID, id)
	if err != nil {
		return skills.Result{}, err
	}

	state := "paused"
	if watch.Enabled {
		state = "enabled"
	}
	return skills.Result{Text: fmt.Sprintf("Watch #%d is now %s.", watch.ID, state)}, nil
}

type removeSkill struct {
	service *watchengine.Service
}

func NewRemoveSkill(service *watchengine.Service) skills.Skill {
	return &removeSkill{service: service}
}

func (s *removeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_remove",
		Group:       skills.GroupWatch,
		Description: "Delete one watch rule.",
		Aliases:     []string{"watch delete"},
		Usage:       "/watch remove <id>",
		Mutating:    true,
	}
}

func (s *removeSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	id, err := parseID(input.Args["id"])
	if err != nil {
		return skills.Result{}, err
	}
	watch, err := s.service.DeleteWatch(ctx, input.ChatID, id)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: fmt.Sprintf("Removed watch #%d: %s", watch.ID, watch.Name)}, nil
}

type historySkill struct {
	service *watchengine.Service
}

func NewHistorySkill(service *watchengine.Service) skills.Skill {
	return &historySkill{service: service}
}

func (s *historySkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_history",
		Group:       skills.GroupWatch,
		Description: "Show recent watch incidents for the current chat or for one watch id.",
		Aliases:     []string{"watch incidents"},
		Usage:       "/watch history [id]",
	}
}

func (s *historySkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	var watchID int64
	var err error
	if strings.TrimSpace(input.Args["id"]) != "" {
		watchID, err = parseID(input.Args["id"])
		if err != nil {
			return skills.Result{}, err
		}
	}

	incidents, err := s.service.ListHistory(ctx, input.ChatID, watchID, 10)
	if err != nil {
		return skills.Result{}, err
	}
	if len(incidents) == 0 {
		return skills.Result{Text: "No watch incidents yet."}, nil
	}

	lines := []string{"Recent watch incidents:"}
	for _, incident := range incidents {
		lines = append(lines, "- "+singleLineIncident(incident))
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type testSkill struct {
	service *watchengine.Service
}

func NewTestSkill(service *watchengine.Service) skills.Skill {
	return &testSkill{service: service}
}

func (s *testSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_test",
		Group:       skills.GroupWatch,
		Description: "Run one dry probe for a watch and show its current condition.",
		Aliases:     []string{"watch probe"},
		Usage:       "/watch test <id>",
	}
}

func (s *testSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	id, err := parseID(input.Args["id"])
	if err != nil {
		return skills.Result{}, err
	}
	result, err := s.service.ProbeWatch(ctx, input.ChatID, id)
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("Watch #%d: %s", result.Watch.ID, result.Watch.Name),
		"Condition: false",
	}
	if result.ConditionMet {
		lines[1] = "Condition: true"
	}
	if result.Summary != "" {
		lines = append(lines, result.Summary)
	}
	if result.Details != "" {
		lines = append(lines, result.Details)
	}
	if result.ConditionMet {
		lines = append(lines, "Condition for: "+result.ConditionFor.String())
	}
	if result.TriggerReady {
		lines = append(lines, "Trigger: ready")
	} else {
		lines = append(lines, "Trigger: waiting")
	}
	if result.CooldownLeft > 0 {
		lines = append(lines, "Cooldown left: "+result.CooldownLeft.String())
	}
	if result.OpenIncident {
		lines = append(lines, "Incident: open")
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

func renderWatch(watch models.Watch) string {
	lines := []string{
		fmt.Sprintf("#%d %s", watch.ID, watch.Name),
		fmt.Sprintf("Kind: %s", watch.Kind),
	}
	if strings.TrimSpace(watch.Target) != "" {
		lines = append(lines, "Target: "+watch.Target)
	}
	if watch.Threshold > 0 {
		lines = append(lines, fmt.Sprintf("Threshold: %.1f", watch.Threshold))
	}
	lines = append(lines,
		"Duration: "+watch.Duration.String(),
		"Reaction: "+watch.ReactionMode,
		"Cooldown: "+watch.Cooldown.String(),
	)
	if watch.ActionType != "" && watch.ActionType != models.WatchActionNone {
		lines = append(lines, "Action: "+watch.ActionType)
	}
	return strings.Join(lines, "\n")
}

func singleLineWatch(watch models.Watch) string {
	state := "enabled"
	if !watch.Enabled {
		state = "paused"
	}

	parts := []string{
		fmt.Sprintf("#%d", watch.ID),
		watch.Name,
		"[" + state + "]",
	}
	if watch.IncidentState == models.WatchIncidentStateOpen {
		parts = append(parts, "[open incident]")
	}
	return strings.Join(parts, " ")
}

func singleLineIncident(incident models.WatchIncident) string {
	parts := []string{
		fmt.Sprintf("#%d", incident.ID),
		incident.WatchName + ":",
		incident.Status,
	}
	if incident.ActionStatus != "" && incident.ActionStatus != models.WatchActionStatusNone {
		parts = append(parts, "action="+incident.ActionStatus)
	}
	if incident.Summary != "" {
		parts = append(parts, "- "+incident.Summary)
	}
	return strings.Join(parts, " ")
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: numeric id is required", skills.ErrInvalidArguments)
	}
	return id, nil
}
