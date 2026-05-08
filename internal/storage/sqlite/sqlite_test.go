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

	memory, err := repo.AddMemory(ctx, models.Memory{
		Text:   "Mac mini is the main inference node",
		Kind:   "host",
		Tags:   []string{"homelab", "mac-mini"},
		Source: "telegram",
	})
	if err != nil {
		t.Fatalf("AddMemory returned error: %v", err)
	}
	if memory.ID == 0 {
		t.Fatal("expected memory id to be assigned")
	}

	memories, err := repo.ListMemories(ctx, 10)
	if err != nil {
		t.Fatalf("ListMemories returned error: %v", err)
	}
	if len(memories) != 1 || memories[0].Text != memory.Text {
		t.Fatalf("unexpected memories: %#v", memories)
	}
	if len(memories[0].Tags) != 2 {
		t.Fatalf("expected memory tags to be stored, got %#v", memories[0].Tags)
	}

	searchResults, err := repo.SearchMemories(ctx, "inference", 10)
	if err != nil {
		t.Fatalf("SearchMemories returned error: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != memory.ID {
		t.Fatalf("unexpected memory search results: %#v", searchResults)
	}

	if err := repo.DeleteMemory(ctx, memory.ID); err != nil {
		t.Fatalf("DeleteMemory returned error: %v", err)
	}

	memories, err = repo.ListMemories(ctx, 10)
	if err != nil {
		t.Fatalf("ListMemories after delete returned error: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected memories to be empty after delete, got %#v", memories)
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

func TestPruneOlderThanDeletesOldRowsAndKeepsRecent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := New(ctx, filepath.Join(t.TempDir(), "prune.db"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer repo.Close()

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	for _, ts := range []time.Time{old, old, recent} {
		if err := repo.SaveMessage(ctx, models.Message{
			TelegramUserID: 1, TelegramChatID: 1, Role: models.RoleUser, Text: "x", CreatedAt: ts,
		}); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
		if err := repo.SaveSkillCall(ctx, models.SkillCall{
			SkillName: "s", InputText: "i", ArgsJSON: "{}", Status: "ok", CreatedAt: ts,
		}); err != nil {
			t.Fatalf("SaveSkillCall: %v", err)
		}
	}

	cutoff := now.Add(-7 * 24 * time.Hour)
	msgs, calls, err := repo.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if msgs != 2 || calls != 2 {
		t.Fatalf("expected 2 messages and 2 skill_calls deleted, got %d / %d", msgs, calls)
	}

	rest, err := repo.ListMessagesByChat(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListMessagesByChat: %v", err)
	}
	if len(rest) != 1 {
		t.Fatalf("expected 1 surviving message, got %d", len(rest))
	}
}
