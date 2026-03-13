package services

import (
	"context"
	"testing"

	"openlight/internal/skills"
)

type stubManager struct {
	status Info
	logs   string
}

func (s stubManager) List(context.Context) ([]Info, error) {
	return []Info{s.status}, nil
}

func (s stubManager) Status(context.Context, string) (Info, error) {
	return s.status, nil
}

func (s stubManager) Restart(context.Context, string) error {
	return nil
}

func (s stubManager) Logs(context.Context, string, int) (string, error) {
	return s.logs, nil
}

func TestRestartSkillRequiresService(t *testing.T) {
	t.Parallel()

	_, err := NewRestartSkill(stubManager{}).Execute(context.Background(), skills.Input{Args: map[string]string{}})
	if err == nil {
		t.Fatal("expected missing service name to fail")
	}
}

func TestStatusSkillFormatsResponse(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubManager{
		status: Info{
			Name:        "tailscale.service",
			LoadState:   "loaded",
			ActiveState: "active",
			SubState:    "running",
			Description: "Tailscale node agent",
		},
	}).Execute(context.Background(), skills.Input{Args: map[string]string{"service": "tailscale"}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	want := "Service: tailscale\nLoad: loaded\nActive: active\nSub: running\nDescription: Tailscale node agent"
	if result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestStatusSkillUsesSingleWhitelistedServiceWhenArgumentMissing(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubManager{
		status: Info{
			Name:        "tailscaled.service",
			LoadState:   "loaded",
			ActiveState: "active",
			SubState:    "running",
			Description: "Tailscale node agent",
		},
	}).Execute(context.Background(), skills.Input{Args: map[string]string{}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Service: tailscale\nLoad: loaded\nActive: active\nSub: running\nDescription: Tailscale node agent"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestListSkillUsesFriendlyServiceName(t *testing.T) {
	t.Parallel()

	result, err := NewListSkill(stubManager{
		status: Info{
			Name:        "tailscaled.service",
			ActiveState: "active",
		},
	}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Allowed services:\n- tailscale: active"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestLogsSkillUsesSingleWhitelistedServiceWhenArgumentMissing(t *testing.T) {
	t.Parallel()

	result, err := NewLogsSkill(stubManager{
		status: Info{
			Name: "tailscaled.service",
		},
		logs: "line one\nline two",
	}, 50).Execute(context.Background(), skills.Input{Args: map[string]string{}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Logs for tailscale:\nline one\nline two"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestSystemdManagerRejectsNonWhitelistedService(t *testing.T) {
	t.Parallel()

	manager := NewSystemdManager([]string{"tailscale"}, nil)
	_, err := manager.Status(context.Background(), "nginx")
	if err == nil {
		t.Fatal("expected non-whitelisted service to be rejected")
	}
}

func TestSystemdManagerNormalizesTailscaleAlias(t *testing.T) {
	t.Parallel()

	manager := NewSystemdManager([]string{"tailscale"}, nil)
	systemdManager, ok := manager.(*SystemdManager)
	if !ok {
		t.Fatal("expected concrete SystemdManager")
	}

	service, err := systemdManager.normalizeService("tailscale")
	if err != nil {
		t.Fatalf("normalizeService returned error: %v", err)
	}
	if service != "tailscaled.service" {
		t.Fatalf("unexpected normalized service: %q", service)
	}
}

func TestShouldRetryWithSudo(t *testing.T) {
	t.Parallel()

	if !shouldRetryWithSudo([]byte("Failed to restart tailscaled.service: Interactive authentication required.")) {
		t.Fatal("expected interactive auth error to trigger sudo retry")
	}
	if shouldRetryWithSudo([]byte("Unit tailscaled.service not found.")) {
		t.Fatal("did not expect missing unit error to trigger sudo retry")
	}
}
