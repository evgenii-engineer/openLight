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

func (s stubManager) Targets() []Info {
	return []Info{s.status}
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

func (s stubManager) Exec(context.Context, string, ...string) (string, error) {
	return "", nil
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

func TestStatusSkillFormatsGenericServiceName(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubManager{
		status: Info{
			Name:        "matrix.service",
			LoadState:   "loaded",
			ActiveState: "active",
			SubState:    "running",
			Description: "Matrix bridge",
		},
	}).Execute(context.Background(), skills.Input{Args: map[string]string{"service": "matrix"}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	want := "Service: matrix\nLoad: loaded\nActive: active\nSub: running\nDescription: Matrix bridge"
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

func TestStatusSkillIncludesRemoteHost(t *testing.T) {
	t.Parallel()

	result, err := NewStatusSkill(stubManager{
		status: Info{
			Name:        "jitsi-web",
			Host:        "vps",
			LoadState:   "compose",
			ActiveState: "active",
			SubState:    "running",
			Description: "docker compose service web",
		},
	}).Execute(context.Background(), skills.Input{Args: map[string]string{"service": "jitsi-web"}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	want := "Service: jitsi-web\nHost: vps\nLoad: compose\nActive: active\nSub: running\nDescription: docker compose service web"
	if result.Text != want {
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

func TestListSkillIncludesRemoteHost(t *testing.T) {
	t.Parallel()

	result, err := NewListSkill(stubManager{
		status: Info{
			Name:        "jitsi-web",
			Host:        "vps",
			ActiveState: "active",
		},
	}).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Allowed services:\n- jitsi-web@vps: active"; result.Text != want {
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
	}, 50, 3000).Execute(context.Background(), skills.Input{Args: map[string]string{}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Logs for tailscale:\nline one\nline two"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestLogsSkillTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	result, err := NewLogsSkill(stubManager{
		status: Info{
			Name: "synapse.service",
		},
		logs: "line one\nline two\nline three\nline four",
	}, 50, 24).Execute(context.Background(), skills.Input{Args: map[string]string{"service": "synapse"}})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if want := "Logs for synapse:\nline one\n...\n[truncated]"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestSystemdManagerRejectsNonWhitelistedService(t *testing.T) {
	t.Parallel()

	manager, err := NewManager([]string{"tailscale"}, nil, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	_, err = manager.Status(context.Background(), "nginx")
	if err == nil {
		t.Fatal("expected non-whitelisted service to be rejected")
	}
}

func TestSystemdManagerNormalizesTailscaleAlias(t *testing.T) {
	t.Parallel()

	manager, err := NewManager([]string{"tailscale"}, nil, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
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

func TestSystemdManagerNormalizesGenericService(t *testing.T) {
	t.Parallel()

	manager, err := NewManager([]string{"matrix"}, nil, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	systemdManager, ok := manager.(*SystemdManager)
	if !ok {
		t.Fatal("expected concrete SystemdManager")
	}

	service, err := systemdManager.normalizeService("matrix")
	if err != nil {
		t.Fatalf("normalizeService returned error: %v", err)
	}
	if service != "matrix.service" {
		t.Fatalf("unexpected normalized service: %q", service)
	}
}

func TestSystemdManagerNormalizesComposeService(t *testing.T) {
	t.Parallel()

	manager, err := NewManager([]string{"synapse=compose:/home/damk/matrix/docker-compose.yml"}, nil, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	systemdManager, ok := manager.(*SystemdManager)
	if !ok {
		t.Fatal("expected concrete SystemdManager")
	}

	service, err := systemdManager.normalizeService("synapse")
	if err != nil {
		t.Fatalf("normalizeService returned error: %v", err)
	}
	if service != "synapse" {
		t.Fatalf("unexpected normalized service: %q", service)
	}
}

func TestSystemdManagerNormalizesDockerContainer(t *testing.T) {
	t.Parallel()

	manager, err := NewManager([]string{"jitsi-web=docker:docker-jitsi-meet_web_1"}, nil, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	systemdManager, ok := manager.(*SystemdManager)
	if !ok {
		t.Fatal("expected concrete SystemdManager")
	}

	service, err := systemdManager.normalizeService("jitsi-web")
	if err != nil {
		t.Fatalf("normalizeService returned error: %v", err)
	}
	if service != "docker-jitsi-meet_web_1" {
		t.Fatalf("unexpected normalized docker service: %q", service)
	}
}

func TestAllowedServiceNamesUsesFriendlyComposeAlias(t *testing.T) {
	t.Parallel()

	names, err := AllowedServiceNames([]string{
		"tailscale",
		"synapse=compose:/home/damk/matrix/docker-compose.yml",
		"jitsi-web=docker:docker-jitsi-meet_web_1",
	})
	if err != nil {
		t.Fatalf("AllowedServiceNames returned error: %v", err)
	}

	if len(names) != 3 || names[0] != "jitsi-web" || names[1] != "synapse" || names[2] != "tailscale" {
		t.Fatalf("unexpected allowed service names: %#v", names)
	}
}

func TestNewManagerRejectsUnknownRemoteHost(t *testing.T) {
	t.Parallel()

	_, err := NewManager([]string{"jitsi-web=host:vps:compose:/opt/jitsi/docker-compose.yml:web"}, nil, nil)
	if err == nil {
		t.Fatal("expected unknown remote host to be rejected")
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
