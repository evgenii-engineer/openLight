package watch

import (
	"context"
	"strings"

	"openlight/internal/skills"
	"openlight/internal/telegram"
	watchengine "openlight/internal/watch"
)

type enableSkill struct {
	service *watchengine.Service
}

func NewEnableSkill(service *watchengine.Service) skills.Skill {
	return &enableSkill{service: service}
}

func (s *enableSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "watch_enable",
		Group:       skills.GroupWatch,
		Description: "Enable built-in monitoring packs such as docker, system, or auto-heal.",
		Aliases:     []string{"enable monitoring", "enable pack"},
		Usage:       "/enable <docker|system|auto-heal>",
		Examples: []string{
			"/enable docker",
			"/enable system",
			"/enable auto-heal",
		},
		Mutating: true,
	}
}

func (s *enableSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	pack := strings.TrimSpace(input.Args["pack"])
	if pack == "" {
		return skills.Result{
			Text:    "Enable monitoring packs:\n- Docker: alerts when an allowlisted container or compose service goes down.\n- System: CPU, memory, and disk alerts with safe defaults.\n- Auto-heal: one automatic restart attempt for down services.\n\nUse /enable docker, /enable system, or /enable auto-heal.",
			Buttons: enableButtons(""),
		}, nil
	}

	text, err := s.service.EnablePack(ctx, input.ChatID, input.UserID, pack)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{
		Text:    text,
		Buttons: enableButtons(pack),
	}, nil
}

func enableButtons(selected string) [][]telegram.Button {
	selected = strings.TrimSpace(strings.ToLower(selected))
	rows := [][]telegram.Button{
		{
			{Text: "Docker", CallbackData: "enable docker"},
			{Text: "System", CallbackData: "enable system"},
		},
		{
			{Text: "Auto-heal", CallbackData: "enable auto-heal"},
		},
	}

	if selected == "" {
		return rows
	}

	filtered := make([][]telegram.Button, 0, len(rows))
	for _, row := range rows {
		nextRow := make([]telegram.Button, 0, len(row))
		for _, button := range row {
			if strings.EqualFold(strings.TrimSpace(button.CallbackData), "enable "+selected) {
				continue
			}
			nextRow = append(nextRow, button)
		}
		if len(nextRow) > 0 {
			filtered = append(filtered, nextRow)
		}
	}
	return filtered
}
