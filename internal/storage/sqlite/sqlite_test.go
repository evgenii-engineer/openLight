package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/storage"
)

func TestRepositoryCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer repo.Close()

	if err := repo.SaveMessage(ctx, models.Message{
		TelegramUserID: 1,
		TelegramChatID: 2,
		Role:           models.RoleUser,
		Text:           "hello",
	}); err != nil {
		t.Fatalf("SaveMessage returned error: %v", err)
	}

	if err := repo.SaveSkillCall(ctx, models.SkillCall{
		SkillName: "ping",
		InputText: "/ping",
		ArgsJSON:  "{}",
		Status:    models.SkillCallSuccess,
	}); err != nil {
		t.Fatalf("SaveSkillCall returned error: %v", err)
	}

	note, err := repo.AddNote(ctx, "remember the milk")
	if err != nil {
		t.Fatalf("AddNote returned error: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("expected note id to be assigned")
	}

	notes, err := repo.ListNotes(ctx, 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notes) != 1 || notes[0].Text != "remember the milk" {
		t.Fatalf("unexpected notes: %#v", notes)
	}

	if err := repo.DeleteNote(ctx, note.ID); err != nil {
		t.Fatalf("DeleteNote returned error: %v", err)
	}

	notes, err = repo.ListNotes(ctx, 10)
	if err != nil {
		t.Fatalf("ListNotes after delete returned error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected notes to be empty after delete, got %#v", notes)
	}

	if err := repo.SetSetting(ctx, "cursor", "42"); err != nil {
		t.Fatalf("SetSetting returned error: %v", err)
	}

	setting, ok, err := repo.GetSetting(ctx, "cursor")
	if err != nil {
		t.Fatalf("GetSetting returned error: %v", err)
	}
	if !ok || setting.Value != "42" {
		t.Fatalf("unexpected setting: ok=%v setting=%#v", ok, setting)
	}
	if err := repo.DeleteSetting(ctx, "cursor"); err != nil {
		t.Fatalf("DeleteSetting returned error: %v", err)
	}
	_, ok, err = repo.GetSetting(ctx, "cursor")
	if err != nil {
		t.Fatalf("GetSetting after delete returned error: %v", err)
	}
	if ok {
		t.Fatal("expected setting to be deleted")
	}

	watch, err := repo.CreateWatch(ctx, models.Watch{
		TelegramUserID: 1,
		TelegramChatID: 2,
		Name:           "service/nginx down",
		Kind:           models.WatchKindServiceDown,
		Target:         "nginx",
		Duration:       time.Minute,
		ReactionMode:   models.WatchReactionAsk,
		ActionType:     models.WatchActionServiceRestart,
		Cooldown:       10 * time.Minute,
		Enabled:        true,
		IncidentState:  models.WatchIncidentStateClear,
	})
	if err != nil {
		t.Fatalf("CreateWatch returned error: %v", err)
	}

	watches, err := repo.ListWatches(ctx, storage.WatchListOptions{ChatID: 2})
	if err != nil {
		t.Fatalf("ListWatches returned error: %v", err)
	}
	if len(watches) != 1 || watches[0].ID != watch.ID {
		t.Fatalf("unexpected watches: %#v", watches)
	}

	incident, err := repo.CreateWatchIncident(ctx, models.WatchIncident{
		WatchID:         watch.ID,
		TelegramChatID:  2,
		Summary:         "nginx is down",
		Status:          models.WatchIncidentStatusOpen,
		ReactionMode:    models.WatchReactionAsk,
		ActionType:      models.WatchActionServiceRestart,
		ActionStatus:    models.WatchActionStatusPending,
		ActionExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateWatchIncident returned error: %v", err)
	}

	openIncident, ok, err := repo.GetOpenWatchIncident(ctx, watch.ID)
	if err != nil {
		t.Fatalf("GetOpenWatchIncident returned error: %v", err)
	}
	if !ok || openIncident.ID != incident.ID {
		t.Fatalf("unexpected open incident: ok=%v incident=%#v", ok, openIncident)
	}

	pending, err := repo.ListPendingWatchIncidents(ctx, 2, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListPendingWatchIncidents returned error: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != incident.ID {
		t.Fatalf("unexpected pending incidents: %#v", pending)
	}
}
