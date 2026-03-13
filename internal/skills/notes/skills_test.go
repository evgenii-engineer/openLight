package notes

import (
	"context"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
)

type stubStore struct {
	notes []models.Note
}

func (s *stubStore) AddNote(_ context.Context, text string) (models.Note, error) {
	note := models.Note{ID: int64(len(s.notes) + 1), Text: text, CreatedAt: time.Now()}
	s.notes = append([]models.Note{note}, s.notes...)
	return note, nil
}

func (s *stubStore) ListNotes(_ context.Context, limit int) ([]models.Note, error) {
	if limit > len(s.notes) {
		limit = len(s.notes)
	}
	return s.notes[:limit], nil
}

func TestAddSkillRequiresText(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	_, err := NewAddSkill(store).Execute(context.Background(), skills.Input{Args: map[string]string{}})
	if err == nil {
		t.Fatal("expected missing note text to fail")
	}
}

func TestListSkillFormatsNotes(t *testing.T) {
	t.Parallel()

	store := &stubStore{notes: []models.Note{
		{ID: 2, Text: "second"},
		{ID: 1, Text: "first"},
	}}

	result, err := NewListSkill(store, 10).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := "Notes:\n- #2 second\n- #1 first"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func (s *stubStore) DeleteNote(_ context.Context, id int64) error {
	for i, note := range s.notes {
		if note.ID == id {
			s.notes = append(s.notes[:i], s.notes[i+1:]...)
			return nil
		}
	}
	return skills.ErrNotFound
}

func TestDeleteSkillDeletesNote(t *testing.T) {
	t.Parallel()

	store := &stubStore{notes: []models.Note{
		{ID: 2, Text: "second"},
		{ID: 1, Text: "first"},
	}}

	result, err := NewDeleteSkill(store).Execute(context.Background(), skills.Input{
		Args: map[string]string{"id": "2"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "Deleted note #2" {
		t.Fatalf("unexpected response: %q", result.Text)
	}
	if len(store.notes) != 1 || store.notes[0].ID != 1 {
		t.Fatalf("unexpected remaining notes: %#v", store.notes)
	}
}

func TestDeleteSkillRequiresPositiveID(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	_, err := NewDeleteSkill(store).Execute(context.Background(), skills.Input{
		Args: map[string]string{"id": "abc"},
	})
	if err == nil {
		t.Fatal("expected invalid id to fail")
	}
}
