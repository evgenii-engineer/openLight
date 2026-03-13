package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"openlight/internal/models"
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
}
