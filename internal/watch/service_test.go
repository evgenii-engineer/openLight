package watch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
	serviceskills "openlight/internal/skills/services"
	systemskills "openlight/internal/skills/system"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
)

type fakeNotifier struct {
	messages []string
	buttons  [][][]telegram.Button
}

func (n *fakeNotifier) SendText(_ context.Context, _ int64, text string) error {
	n.messages = append(n.messages, text)
	n.buttons = append(n.buttons, nil)
	return nil
}

func (n *fakeNotifier) SendTextWithButtons(_ context.Context, _ int64, text string, buttons [][]telegram.Button) error {
	n.messages = append(n.messages, text)
	n.buttons = append(n.buttons, buttons)
	return nil
}

type fakeProvider struct {
	cpu         float64
	memoryUsed  uint64
	memoryTotal uint64
	diskUsed    uint64
	diskTotal   uint64
	temperature float64
}

func (p fakeProvider) CPUUsage(context.Context) (float64, error) { return p.cpu, nil }

func (p fakeProvider) MemoryStats(context.Context) (systemskills.MemoryStats, error) {
	return systemskills.MemoryStats{}, fmt.Errorf("not implemented")
}

func (p fakeProvider) DiskStats(context.Context, string) (systemskills.DiskStats, error) {
	return systemskills.DiskStats{}, fmt.Errorf("not implemented")
}

func (p fakeProvider) Uptime(context.Context) (time.Duration, error) { return time.Hour, nil }
func (p fakeProvider) Hostname(context.Context) (string, error)      { return "raspberry", nil }
func (p fakeProvider) IPAddresses(context.Context) ([]string, error) {
	return []string{"127.0.0.1"}, nil
}
func (p fakeProvider) Temperature(context.Context) (float64, error) { return p.temperature, nil }

type fakeServiceManager struct {
	service serviceskills.Info
}

func (m *fakeServiceManager) Targets() []serviceskills.Info {
	return []serviceskills.Info{m.service}
}

func (m *fakeServiceManager) List(context.Context) ([]serviceskills.Info, error) {
	return []serviceskills.Info{m.service}, nil
}

func (m *fakeServiceManager) Status(context.Context, string) (serviceskills.Info, error) {
	return m.service, nil
}

func (m *fakeServiceManager) Restart(_ context.Context, service string) error {
	m.service.Name = service
	m.service.ActiveState = "active"
	m.service.SubState = "running"
	m.service.Description = "healthy after restart"
	return nil
}

func (m *fakeServiceManager) Logs(context.Context, string, int) (string, error) {
	return "bind error", nil
}

func (m *fakeServiceManager) Exec(context.Context, string, ...string) (string, error) {
	return "", nil
}

func TestServiceRunCycleAskAndRestartAction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	manager := &fakeServiceManager{
		service: serviceskills.Info{
			Name:        "nginx",
			ActiveState: "inactive",
			SubState:    "failed",
			Description: "bind error",
		},
	}
	registry := skills.NewRegistry()
	if err := skills.RegisterModules(registry, serviceskills.NewModule(manager, 30, 3000)); err != nil {
		t.Fatalf("RegisterModules returned error: %v", err)
	}

	notifier := &fakeNotifier{}
	service := NewService(repo, registry, notifier, fakeProvider{}, manager, nil, Options{
		PollInterval:   5 * time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	watch, err := service.AddWatch(ctx, 200, 100, "service nginx ask for 1ms cooldown 1m")
	if err != nil {
		t.Fatalf("AddWatch returned error: %v", err)
	}

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("runCycle returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("second runCycle returned error: %v", err)
	}

	if len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0], "Alert #") || !strings.Contains(notifier.messages[0], "Choose an action below.") {
		t.Fatalf("unexpected alert messages: %#v", notifier.messages)
	}
	if len(notifier.buttons) != 1 || len(notifier.buttons[0]) != 2 {
		t.Fatalf("expected two rows of action buttons, got %#v", notifier.buttons)
	}
	if notifier.buttons[0][0][0].CallbackData != "watch:action:1:restart_service" ||
		notifier.buttons[0][0][1].CallbackData != "watch:action:1:show_logs" ||
		notifier.buttons[0][0][2].CallbackData != "watch:action:1:show_status" ||
		notifier.buttons[0][1][0].CallbackData != "watch:action:1:ignore_alert" {
		t.Fatalf("unexpected callback data: %#v", notifier.buttons)
	}

	handled, err := service.HandleAction(ctx, 200, 100, "watch:action:1:restart_service")
	if err != nil {
		t.Fatalf("HandleAction returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected action to be handled")
	}
	if len(notifier.messages) < 3 || notifier.messages[1] != "Restarting nginx..." || !strings.Contains(notifier.messages[2], "Action: restarted nginx") {
		t.Fatalf("unexpected action messages: %#v", notifier.messages)
	}

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("recovery runCycle returned error: %v", err)
	}
	if got := notifier.messages[len(notifier.messages)-1]; !strings.Contains(got, "Resolved #") {
		t.Fatalf("expected resolved message, got %#v", notifier.messages)
	}

	incidents, err := repo.ListWatchIncidents(ctx, storage.WatchIncidentListOptions{
		ChatID: watch.TelegramChatID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListWatchIncidents returned error: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected one incident, got %#v", incidents)
	}
	if incidents[0].ActionStatus != models.WatchActionStatusSucceeded {
		t.Fatalf("unexpected action status: %#v", incidents[0])
	}
	if incidents[0].Status != models.WatchIncidentStatusResolved {
		t.Fatalf("expected resolved incident, got %#v", incidents[0])
	}
}

func TestServiceRunCycleDoesNotResolveWhileActionRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	manager := &fakeServiceManager{
		service: serviceskills.Info{
			Name:        "nginx",
			ActiveState: "inactive",
			SubState:    "failed",
			Description: "bind error",
		},
	}
	registry := skills.NewRegistry()
	if err := skills.RegisterModules(registry, serviceskills.NewModule(manager, 30, 3000)); err != nil {
		t.Fatalf("RegisterModules returned error: %v", err)
	}

	notifier := &fakeNotifier{}
	service := NewService(repo, registry, notifier, fakeProvider{}, manager, nil, Options{
		PollInterval:   5 * time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	watch, err := service.AddWatch(ctx, 200, 100, "service nginx ask for 1ms cooldown 1m")
	if err != nil {
		t.Fatalf("AddWatch returned error: %v", err)
	}

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("runCycle returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("second runCycle returned error: %v", err)
	}

	incident, ok, err := repo.GetOpenWatchIncident(ctx, watch.ID)
	if err != nil {
		t.Fatalf("GetOpenWatchIncident returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected open incident")
	}

	incident.ActionStatus = models.WatchActionStatusRunning
	if err := repo.UpdateWatchIncident(ctx, incident); err != nil {
		t.Fatalf("UpdateWatchIncident returned error: %v", err)
	}

	manager.service.ActiveState = "active"
	manager.service.SubState = "running"
	manager.service.Description = "healthy again"

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("healthy runCycle returned error: %v", err)
	}

	incident, ok, err = repo.GetOpenWatchIncident(ctx, watch.ID)
	if err != nil {
		t.Fatalf("GetOpenWatchIncident after running returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected incident to remain open while action is running")
	}
	if incident.ActionStatus != models.WatchActionStatusRunning {
		t.Fatalf("unexpected action status while running: %#v", incident)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("expected no resolved message while action is running, got %#v", notifier.messages)
	}

	incident.ActionStatus = models.WatchActionStatusSucceeded
	incident.ActionCompletedAt = time.Now()
	if err := repo.UpdateWatchIncident(ctx, incident); err != nil {
		t.Fatalf("UpdateWatchIncident after success returned error: %v", err)
	}

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("resolved runCycle returned error: %v", err)
	}

	incidents, err := repo.ListWatchIncidents(ctx, storage.WatchIncidentListOptions{
		ChatID: watch.TelegramChatID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListWatchIncidents returned error: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected one incident, got %#v", incidents)
	}
	if incidents[0].Status != models.WatchIncidentStatusResolved {
		t.Fatalf("expected resolved incident after action completion, got %#v", incidents[0])
	}
	if got := notifier.messages[len(notifier.messages)-1]; !strings.Contains(got, "Resolved #") {
		t.Fatalf("expected resolved message after action completion, got %#v", notifier.messages)
	}
}

func TestServiceRunCycleAutoRestartsService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	manager := &fakeServiceManager{
		service: serviceskills.Info{
			Name:        "tailscale",
			Backend:     serviceskills.BackendSystemd,
			ActiveState: "inactive",
			SubState:    "dead",
			Description: "Tailscale node agent",
		},
	}
	registry := skills.NewRegistry()
	if err := skills.RegisterModules(registry, serviceskills.NewModule(manager, 30, 3000)); err != nil {
		t.Fatalf("RegisterModules returned error: %v", err)
	}

	notifier := &fakeNotifier{}
	service := NewService(repo, registry, notifier, fakeProvider{}, manager, nil, Options{
		PollInterval:   5 * time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	watch, err := service.AddWatch(ctx, 200, 100, "service tailscale auto for 1ms cooldown 1m")
	if err != nil {
		t.Fatalf("AddWatch returned error: %v", err)
	}

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("runCycle returned error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("second runCycle returned error: %v", err)
	}

	if len(notifier.messages) == 0 || !strings.Contains(notifier.messages[0], "Automatic recovery is enabled for this alert.") {
		t.Fatalf("unexpected auto alert messages: %#v", notifier.messages)
	}
	if !strings.Contains(notifier.messages[0], "Action: restarted tailscale (automatic recovery).") {
		t.Fatalf("expected auto restart report, got %#v", notifier.messages)
	}

	incidents, err := repo.ListWatchIncidents(ctx, storage.WatchIncidentListOptions{
		ChatID: watch.TelegramChatID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListWatchIncidents returned error: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected one incident, got %#v", incidents)
	}
	if incidents[0].ActionStatus != models.WatchActionStatusSucceeded {
		t.Fatalf("unexpected action status: %#v", incidents[0])
	}
}

func TestServiceHandleActionExpiredCallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	notifier := &fakeNotifier{}
	service := NewService(repo, skills.NewRegistry(), notifier, fakeProvider{}, &fakeServiceManager{}, nil, Options{
		PollInterval:   time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	handled, err := service.HandleAction(ctx, 200, 100, "watch:action:99:restart_service")
	if err != nil {
		t.Fatalf("HandleAction returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected stale callback to be handled")
	}
	if len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0], "Alert #99 is no longer pending.") {
		t.Fatalf("unexpected stale callback response: %#v", notifier.messages)
	}
}

func TestParseAddSpecMetric(t *testing.T) {
	t.Parallel()

	spec, err := parseAddSpec("cpu > 90% for 5m cooldown 15m")
	if err != nil {
		t.Fatalf("parseAddSpec returned error: %v", err)
	}

	if spec.Kind != models.WatchKindCPUHigh {
		t.Fatalf("unexpected kind: %#v", spec)
	}
	if spec.Threshold != 90 {
		t.Fatalf("unexpected threshold: %#v", spec)
	}
	if spec.Duration != 5*time.Minute || spec.Cooldown != 15*time.Minute {
		t.Fatalf("unexpected durations: %#v", spec)
	}
}

func TestParseAddSpecServiceAcceptsLLMStyleWatchRule(t *testing.T) {
	t.Parallel()

	spec, err := parseAddSpec("service synapse down ask for restart cooldown 10m")
	if err != nil {
		t.Fatalf("parseAddSpec returned error: %v", err)
	}

	if spec.Kind != models.WatchKindServiceDown {
		t.Fatalf("unexpected kind: %#v", spec)
	}
	if spec.Target != "synapse" {
		t.Fatalf("unexpected target: %#v", spec)
	}
	if spec.ReactionMode != models.WatchReactionAsk {
		t.Fatalf("unexpected reaction mode: %#v", spec)
	}
	if spec.ActionType != models.WatchActionServiceRestart {
		t.Fatalf("unexpected action type: %#v", spec)
	}
	if spec.Cooldown != 10*time.Minute {
		t.Fatalf("unexpected cooldown: %#v", spec)
	}
}

func TestParseConfirmationCallback(t *testing.T) {
	t.Parallel()

	request, ok := parseConfirmation("watch:no:42")
	if !ok {
		t.Fatal("expected callback confirmation to parse")
	}
	if request.Decision != "no" || request.IncidentID != 42 || !request.Explicit {
		t.Fatalf("unexpected request: %#v", request)
	}
}

func TestParseActionRequestCallback(t *testing.T) {
	t.Parallel()

	request, ok := parseActionRequest("watch:action:42:show_logs")
	if !ok {
		t.Fatal("expected action callback to parse")
	}
	if request.IncidentID != 42 || request.ActionID != ActionShowLogsID {
		t.Fatalf("unexpected action request: %#v", request)
	}
}

func TestEnableDockerPackCreatesAskWatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	manager := &fakeServiceManager{
		service: serviceskills.Info{
			Name:        "api",
			Backend:     serviceskills.BackendDocker,
			ActiveState: "active",
			SubState:    "running",
			Description: "docker container api",
		},
	}

	service := NewService(repo, skills.NewRegistry(), &fakeNotifier{}, fakeProvider{}, manager, nil, Options{
		PollInterval:   time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	text, err := service.EnablePack(ctx, 200, 100, "docker")
	if err != nil {
		t.Fatalf("EnablePack returned error: %v", err)
	}
	if !strings.Contains(text, "Docker pack enabled.") {
		t.Fatalf("unexpected pack text: %q", text)
	}

	watches, err := repo.ListWatches(ctx, storage.WatchListOptions{ChatID: 200})
	if err != nil {
		t.Fatalf("ListWatches returned error: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected one docker watch, got %#v", watches)
	}
	if watches[0].ReactionMode != models.WatchReactionAsk || watches[0].ActionType != models.WatchActionServiceRestart {
		t.Fatalf("unexpected docker watch: %#v", watches[0])
	}

	setting, ok, err := repo.GetSetting(ctx, "pack:200:docker")
	if err != nil {
		t.Fatalf("GetSetting returned error: %v", err)
	}
	if !ok || setting.Value != "enabled" {
		t.Fatalf("unexpected docker pack setting: %#v", setting)
	}
}

func TestEnableAutoHealPackUpdatesExistingServiceWatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	manager := &fakeServiceManager{
		service: serviceskills.Info{
			Name:        "api",
			Backend:     serviceskills.BackendSystemd,
			ActiveState: "active",
			SubState:    "running",
			Description: "systemd service api",
		},
	}

	service := NewService(repo, skills.NewRegistry(), &fakeNotifier{}, fakeProvider{}, manager, nil, Options{
		PollInterval:   time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	if _, err := service.AddWatch(ctx, 200, 100, "service api ask for 30s cooldown 10m"); err != nil {
		t.Fatalf("AddWatch returned error: %v", err)
	}

	text, err := service.EnablePack(ctx, 200, 100, "auto-heal")
	if err != nil {
		t.Fatalf("EnablePack returned error: %v", err)
	}
	if !strings.Contains(text, "Auto-heal enabled.") {
		t.Fatalf("unexpected pack text: %q", text)
	}

	watches, err := repo.ListWatches(ctx, storage.WatchListOptions{ChatID: 200})
	if err != nil {
		t.Fatalf("ListWatches returned error: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected one watch after auto-heal update, got %#v", watches)
	}
	if watches[0].ReactionMode != models.WatchReactionAuto || watches[0].ActionType != models.WatchActionServiceRestart {
		t.Fatalf("unexpected auto-heal watch: %#v", watches[0])
	}
}
