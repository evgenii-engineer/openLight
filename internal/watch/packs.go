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
	case "tls":
		return s.enableTLSPack(ctx, chatID, userID)
	case "homelab":
		return s.enableHomelabPack(ctx, chatID, userID)
	case "mac":
		return s.enableMacPack(ctx, chatID, userID)
	case "pi":
		return s.enablePiPack(ctx, chatID, userID)
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
	case "macmini", "mac-mini", "macos":
		return "mac"
	case "raspberrypi", "raspberry-pi", "rpi":
		return "pi"
	case "ssl", "certs", "certificates":
		return "tls"
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

// enableTLSPack creates one cert_expiring watch per allowlisted network
// target on port 443. It is a no-op if the network manager is not wired
// in or no targets are configured.
func (s *Service) enableTLSPack(ctx context.Context, chatID, userID int64) (string, error) {
	if s.networkManager == nil {
		return "", fmt.Errorf("%w: network skills are disabled; set network.enabled=true and add allowed targets", skills.ErrUnavailable)
	}
	targets := s.networkManager.Targets()
	if len(targets) == 0 {
		return "TLS pack is ready, but no network targets are allowlisted yet.", nil
	}

	specs := make([]string, 0, len(targets))
	for _, t := range targets {
		port := t.Port
		if port == 0 || port == 443 {
			specs = append(specs, fmt.Sprintf("cert %s:443 expires-in 14d cooldown 24h", t.Host))
		}
	}
	if len(specs) == 0 {
		return "TLS pack is ready, but none of the allowlisted targets cover port 443.", nil
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "tls"); err != nil {
		return "", err
	}
	return strings.Join([]string{
		"TLS pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"Each cert that drops to 14 days or fewer triggers a notify alert.",
	}, "\n"), nil
}

// enableHomelabPack composes the system pack with port watches for every
// explicit host:port spec in network.allowed. Bare-host entries are
// skipped because there is no canonical port to probe.
func (s *Service) enableHomelabPack(ctx context.Context, chatID, userID int64) (string, error) {
	specs := []string{
		"cpu > 90% for 5m cooldown 15m",
		"memory > 90% for 5m cooldown 15m",
		"disk / > 85% for 3m cooldown 15m",
	}
	portCount := 0
	if s.networkManager != nil {
		for _, t := range s.networkManager.Targets() {
			if t.Port == 0 {
				continue
			}
			specs = append(specs, fmt.Sprintf("port %s:%d for 30s cooldown 10m", t.Host, t.Port))
			portCount++
		}
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "homelab"); err != nil {
		return "", err
	}
	return strings.Join([]string{
		"Homelab pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d (CPU/Memory/Disk + %d port watch(es)).", created, updated, portCount),
		"Add `port host:port` entries to `network.allowed` to monitor more hosts.",
	}, "\n"), nil
}

// enableMacPack tunes the system pack for a Mac mini: the temperature
// watch is omitted because the Darwin provider can't read SMC without
// root, and disk thresholds are looser since macOS volumes tend to run
// fuller (Time Machine snapshots, etc.).
func (s *Service) enableMacPack(ctx context.Context, chatID, userID int64) (string, error) {
	specs := []string{
		"cpu > 90% for 5m cooldown 15m",
		"memory > 90% for 5m cooldown 15m",
		"disk / > 90% for 5m cooldown 30m",
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "mac"); err != nil {
		return "", err
	}
	return strings.Join([]string{
		"Mac mini pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"Temperature is omitted on macOS (powermetrics requires sudo).",
	}, "\n"), nil
}

// enablePiPack tunes the system pack for a Raspberry Pi: lower disk
// threshold (SD cards fill fast) and an explicit temperature alert.
func (s *Service) enablePiPack(ctx context.Context, chatID, userID int64) (string, error) {
	specs := []string{
		"cpu > 85% for 5m cooldown 15m",
		"memory > 85% for 5m cooldown 15m",
		"disk / > 80% for 3m cooldown 15m",
		"temperature > 75 for 2m cooldown 15m",
	}

	created, updated, err := s.ensurePackSpecs(ctx, chatID, userID, specs)
	if err != nil {
		return "", err
	}
	if err := s.markPackEnabled(ctx, chatID, "pi"); err != nil {
		return "", err
	}
	return strings.Join([]string{
		"Raspberry Pi pack enabled.",
		fmt.Sprintf("Created %d watch(es), updated %d.", created, updated),
		"Defaults are tighter than the system pack to suit Pi-class hardware.",
	}, "\n"), nil
}
