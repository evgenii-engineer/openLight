package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	serviceskills "openlight/internal/skills/services"
	"openlight/internal/skills"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
)

// TestServiceCooldownSuppressesDuplicateIncidents verifies that once an
// incident is opened and closed (auto-recovery), the same condition triggering
// again *within* the cooldown does not produce a second incident. Without
// this suppression, a flapping service would page the user repeatedly.
func TestServiceCooldownSuppressesDuplicateIncidents(t *testing.T) {
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
		t.Fatalf("RegisterModules: %v", err)
	}

	notifier := &fakeNotifier{}
	service := NewService(repo, registry, notifier, fakeProvider{}, manager, nil, Options{
		PollInterval:   5 * time.Millisecond,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})

	watch, err := service.AddWatch(ctx, 200, 100, "service tailscale auto for 1ms cooldown 1h")
	if err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	// First two cycles open and auto-restart the incident — the action
	// succeeds and the manager flips the service back to active.
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("first runCycle: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("second runCycle: %v", err)
	}

	// Service flaps back down inside the cooldown window. The cooldown is
	// 1 hour from the first trigger, so this must NOT open a new incident.
	manager.service.ActiveState = "inactive"
	manager.service.SubState = "dead"

	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("third runCycle: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := service.runCycle(ctx); err != nil {
		t.Fatalf("fourth runCycle: %v", err)
	}

	incidents, err := repo.ListWatchIncidents(ctx, storage.WatchIncidentListOptions{
		ChatID: watch.TelegramChatID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListWatchIncidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected cooldown to suppress duplicates, got %d incidents", len(incidents))
	}
}
