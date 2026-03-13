package system

import (
	"context"
	"testing"
	"time"

	"openlight/internal/skills"
)

type stubProvider struct{}

func (stubProvider) CPUUsage(context.Context) (float64, error) {
	return 12.5, nil
}

func (stubProvider) MemoryStats(context.Context) (MemoryStats, error) {
	return MemoryStats{Total: 8 * 1024, Available: 2 * 1024, Used: 6 * 1024}, nil
}

func (stubProvider) DiskStats(context.Context, string) (DiskStats, error) {
	return DiskStats{Path: "/", Total: 100 * 1024, Free: 60 * 1024, Used: 40 * 1024}, nil
}

func (stubProvider) Uptime(context.Context) (time.Duration, error) {
	return 3*time.Hour + 12*time.Minute, nil
}

func (stubProvider) Hostname(context.Context) (string, error) {
	return "pi-zero", nil
}

func (stubProvider) IPAddresses(context.Context) ([]string, error) {
	return []string{"192.168.1.10"}, nil
}

func (stubProvider) Temperature(context.Context) (float64, error) {
	return 44.5, nil
}

func TestStatusSkillIncludesAvailableMetrics(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubProvider{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty status response")
	}
}

func TestCPUSkillFormatsUsage(t *testing.T) {
	t.Parallel()

	result, err := NewCPUSkill(stubProvider{}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "CPU usage: 12.5%" {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}
