package watch

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
)

type addSpec struct {
	Name         string
	Kind         string
	Target       string
	Threshold    float64
	Duration     time.Duration
	ReactionMode string
	ActionType   string
	Cooldown     time.Duration
}

func parseAddSpec(raw string) (addSpec, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return addSpec{}, fmt.Errorf("%w: watch rule is required", skills.ErrInvalidArguments)
	}

	switch strings.ToLower(fields[0]) {
	case "service":
		return parseServiceAddSpec(fields)
	case "cpu", "memory", "disk", "temperature":
		return parseMetricAddSpec(fields)
	default:
		return addSpec{}, fmt.Errorf("%w: unsupported watch target %q", skills.ErrInvalidArguments, fields[0])
	}
}

func parseServiceAddSpec(fields []string) (addSpec, error) {
	if len(fields) < 2 {
		return addSpec{}, fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
	}

	spec := addSpec{
		Kind:         models.WatchKindServiceDown,
		Target:       strings.TrimSpace(fields[1]),
		Duration:     30 * time.Second,
		ReactionMode: models.WatchReactionNotify,
		ActionType:   models.WatchActionNone,
		Cooldown:     10 * time.Minute,
	}
	if spec.Target == "" {
		return addSpec{}, fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
	}

	for idx := 2; idx < len(fields); idx++ {
		token := strings.ToLower(strings.TrimSpace(fields[idx]))
		switch token {
		case "down", "stopped", "stop", "failed", "unhealthy":
			// Service watches currently model "service is down" only, so these
			// condition words are accepted as explicit no-op markers.
		case "if", "when", "then", "is", "goes", "becomes", "stays", "remains":
			// Allow a few transition words so a slightly more natural LLM-produced
			// rule can still normalize into the deterministic watch format.
		case "notify":
			spec.ReactionMode = models.WatchReactionNotify
			spec.ActionType = models.WatchActionNone
		case "ask":
			spec.ReactionMode = models.WatchReactionAsk
			spec.ActionType = models.WatchActionServiceRestart
		case "auto":
			spec.ReactionMode = models.WatchReactionAuto
			spec.ActionType = models.WatchActionServiceRestart
		case "restart":
			if spec.ReactionMode == models.WatchReactionNotify {
				spec.ReactionMode = models.WatchReactionAsk
			}
			spec.ActionType = models.WatchActionServiceRestart
		case "for":
			if idx+1 >= len(fields) {
				return addSpec{}, fmt.Errorf("%w: duration value is required after for", skills.ErrInvalidArguments)
			}
			if strings.EqualFold(strings.TrimSpace(fields[idx+1]), "restart") {
				if spec.ReactionMode == models.WatchReactionNotify {
					spec.ReactionMode = models.WatchReactionAsk
				}
				spec.ActionType = models.WatchActionServiceRestart
				idx++
				continue
			}
			duration, err := time.ParseDuration(fields[idx+1])
			if err != nil {
				return addSpec{}, fmt.Errorf("%w: invalid duration %q", skills.ErrInvalidArguments, fields[idx+1])
			}
			spec.Duration = duration
			idx++
		case "cooldown":
			if idx+1 >= len(fields) {
				return addSpec{}, fmt.Errorf("%w: cooldown value is required", skills.ErrInvalidArguments)
			}
			duration, err := time.ParseDuration(fields[idx+1])
			if err != nil {
				return addSpec{}, fmt.Errorf("%w: invalid cooldown %q", skills.ErrInvalidArguments, fields[idx+1])
			}
			spec.Cooldown = duration
			idx++
		default:
			return addSpec{}, fmt.Errorf("%w: unsupported token %q", skills.ErrInvalidArguments, fields[idx])
		}
	}

	spec.Name = "service/" + spec.Target + " down"
	return spec, nil
}

func parseMetricAddSpec(fields []string) (addSpec, error) {
	metric := strings.ToLower(strings.TrimSpace(fields[0]))
	spec := addSpec{
		Target:       metric,
		ReactionMode: models.WatchReactionNotify,
		ActionType:   models.WatchActionNone,
		Cooldown:     15 * time.Minute,
	}

	switch metric {
	case "cpu":
		spec.Kind = models.WatchKindCPUHigh
		spec.Duration = 5 * time.Minute
	case "memory":
		spec.Kind = models.WatchKindMemoryHigh
		spec.Duration = 5 * time.Minute
	case "disk":
		spec.Kind = models.WatchKindDiskHigh
		spec.Duration = 3 * time.Minute
		spec.Target = "/"
	case "temperature":
		spec.Kind = models.WatchKindTemperatureHigh
		spec.Duration = 5 * time.Minute
	default:
		return addSpec{}, fmt.Errorf("%w: unsupported metric %q", skills.ErrInvalidArguments, metric)
	}

	idx := 1
	if metric == "disk" && idx < len(fields) && looksLikeDiskPath(fields[idx]) {
		spec.Target = fields[idx]
		idx++
	}
	if idx < len(fields) && isThresholdMarker(fields[idx]) {
		idx++
	}
	if idx >= len(fields) {
		return addSpec{}, fmt.Errorf("%w: threshold is required", skills.ErrInvalidArguments)
	}

	threshold, err := parseThreshold(fields[idx])
	if err != nil {
		return addSpec{}, err
	}
	spec.Threshold = threshold
	idx++

	for idx < len(fields) {
		token := strings.ToLower(strings.TrimSpace(fields[idx]))
		switch token {
		case "notify":
		case "for":
			if idx+1 >= len(fields) {
				return addSpec{}, fmt.Errorf("%w: duration value is required after for", skills.ErrInvalidArguments)
			}
			duration, parseErr := time.ParseDuration(fields[idx+1])
			if parseErr != nil {
				return addSpec{}, fmt.Errorf("%w: invalid duration %q", skills.ErrInvalidArguments, fields[idx+1])
			}
			spec.Duration = duration
			idx++
		case "cooldown":
			if idx+1 >= len(fields) {
				return addSpec{}, fmt.Errorf("%w: cooldown value is required", skills.ErrInvalidArguments)
			}
			duration, parseErr := time.ParseDuration(fields[idx+1])
			if parseErr != nil {
				return addSpec{}, fmt.Errorf("%w: invalid cooldown %q", skills.ErrInvalidArguments, fields[idx+1])
			}
			spec.Cooldown = duration
			idx++
		case "ask", "auto", "restart":
			return addSpec{}, fmt.Errorf("%w: metric watches currently support notify only", skills.ErrInvalidArguments)
		default:
			return addSpec{}, fmt.Errorf("%w: unsupported token %q", skills.ErrInvalidArguments, fields[idx])
		}
		idx++
	}

	spec.Name = metric + " high"
	if metric == "disk" {
		spec.Name = "disk " + spec.Target + " high"
	}
	return spec, nil
}

func parseThreshold(value string) (float64, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), "%")
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid threshold %q", skills.ErrInvalidArguments, value)
	}
	if threshold <= 0 {
		return 0, fmt.Errorf("%w: threshold must be greater than zero", skills.ErrInvalidArguments)
	}
	return threshold, nil
}

func isThresholdMarker(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ">", ">=", "above":
		return true
	default:
		return false
	}
}

func looksLikeDiskPath(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "/")
}
