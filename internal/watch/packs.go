package watch

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/models"
	"openlight/internal/skills"
	serviceskills "openlight/internal/skills/services"
	"openlight/internal/storage"
)

func (s *Service) EnablePack(ctx context.Context, chatID, userID int64, rawPack string) (string, error) {
	pack := normalizePackName(rawPack)
	switch pack {
	case "docker":
		return s.enableDockerPack(ctx, chatID, userID)
	case "system":
		return s.enableSystemPack(ctx, chatID, userID)
	case "auto-heal":
		return s.enableAutoHealPack(ctx, chatID, userID)
	default:
		return "", fmt.Errorf("%w: unsupported pack %q", skills.ErrInvalidArguments, rawPack)
	}
}

func normalizePackName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	switch value {
	case "autoheal", "auto-heal", "auto-healing":
		return "auto-heal"
	default:
		return value
	}
}

func (s *Service) enableDockerPack(ctx context.Context, chatID, userID int64) (string, error) {
	var specs []string
	for _, service := range s.serviceManager.Targets() {
		if service.Backend != serviceskills.BackendDocker && service.Backend != serviceskills.BackendCompose {
			continue
		}
		specs = append(specs, "service "+service.Name+" ask for 30s cooldown 10m")
	}

	if len(specs) == 0 {
		return "Docker pack is ready, but no Docker or Compose services are allowlisted yet.", nil
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "docker"); err != nil {
		return "", err
	}

	lines := []string{
		"Docker pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"Each container-down alert will offer Restart, Logs, Status, and Ignore.",
		"Stop one allowlisted container to trigger your first alert.",
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) enableSystemPack(ctx context.Context, chatID, userID int64) (string, error) {
	specs := []string{
		"cpu > 90% for 5m cooldown 15m",
		"memory > 90% for 5m cooldown 15m",
		"disk / > 85% for 3m cooldown 15m",
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "system"); err != nil {
		return "", err
	}

	lines := []string{
		"System pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"Defaults: CPU > 90%, Memory > 90%, Disk / > 85%.",
		"System alerts will offer quick Status and Ignore actions.",
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) enableAutoHealPack(ctx context.Context, chatID, userID int64) (string, error) {
	services := s.serviceManager.Targets()
	if len(services) == 0 {
		return "Auto-heal is ready, but no allowlisted services were found.", nil
	}

	specs := make([]string, 0, len(services))
	for _, service := range services {
		specs = append(specs, "service "+service.Name+" auto for 30s cooldown 10m")
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "auto-heal"); err != nil {
		return "", err
	}

	lines := []string{
		"Auto-heal enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"openLight will try one automatic restart for a down service or container and then report the result.",
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) ensurePackSpecs(ctx context.Context, chatID, userID int64, specs []string) (int, int, error) {
	existing, err := s.repository.ListWatches(ctx, storage.WatchListOptions{ChatID: chatID})
	if err != nil {
		return 0, 0, err
	}

	created := 0
	updated := 0
	for _, raw := range specs {
		watch, wasCreated, wasUpdated, err := s.ensureWatchSpec(ctx, existing, chatID, userID, raw)
		if err != nil {
			return created, updated, err
		}
		if wasCreated {
			created++
			existing = append(existing, watch)
		}
		if wasUpdated {
			updated++
			for idx := range existing {
				if existing[idx].ID == watch.ID {
					existing[idx] = watch
					break
				}
			}
		}
	}
	return created, updated, nil
}

func (s *Service) ensureWatchSpec(
	ctx context.Context,
	existing []models.Watch,
	chatID, userID int64,
	raw string,
) (models.Watch, bool, bool, error) {
	spec, err := parseAddSpec(raw)
	if err != nil {
		return models.Watch{}, false, false, err
	}
	if spec.Kind == models.WatchKindServiceDown {
		if err := s.validateServiceTarget(ctx, spec.Target); err != nil {
			return models.Watch{}, false, false, err
		}
	}
	if err := validateSpec(spec); err != nil {
		return models.Watch{}, false, false, err
	}

	for _, watch := range existing {
		if watch.TelegramChatID != chatID {
			continue
		}
		if watch.Kind != spec.Kind || watch.Target != spec.Target {
			continue
		}

		updated := false
		if watch.Name != spec.Name {
			watch.Name = spec.Name
			updated = true
		}
		if watch.Threshold != spec.Threshold {
			watch.Threshold = spec.Threshold
			updated = true
		}
		if watch.Duration != spec.Duration {
			watch.Duration = spec.Duration
			updated = true
		}
		if watch.ReactionMode != spec.ReactionMode {
			watch.ReactionMode = spec.ReactionMode
			updated = true
		}
		if watch.ActionType != spec.ActionType {
			watch.ActionType = spec.ActionType
			updated = true
		}
		if watch.Cooldown != spec.Cooldown {
			watch.Cooldown = spec.Cooldown
			updated = true
		}
		if !watch.Enabled {
			watch.Enabled = true
			updated = true
		}
		if !updated {
			return watch, false, false, nil
		}
		if err := s.repository.UpdateWatch(ctx, watch); err != nil {
			return models.Watch{}, false, false, err
		}
		return watch, false, true, nil
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

	createdWatch, err := s.repository.CreateWatch(ctx, watch)
	if err != nil {
		return models.Watch{}, false, false, err
	}
	return createdWatch, true, false, nil
}

func (s *Service) markPackEnabled(ctx context.Context, chatID int64, pack string) error {
	return s.repository.SetSetting(ctx, fmt.Sprintf("pack:%d:%s", chatID, pack), "enabled")
}
