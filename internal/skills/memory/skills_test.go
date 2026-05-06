package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
)

type stubStore struct {
	memories []models.Memory
}

func (s *stubStore) AddMemory(_ context.Context, memory models.Memory) (models.Memory, error) {
	memory.ID = int64(len(s.memories) + 1)
	memory.CreatedAt = time.Now()
	memory.UpdatedAt = memory.CreatedAt
	s.memories = append([]models.Memory{memory}, s.memories...)
	return memory, nil
}

func (s *stubStore) ListMemories(_ context.Context, limit int) ([]models.Memory, error) {
	if limit > len(s.memories) {
		limit = len(s.memories)
	}
	return s.memories[:limit], nil
}

func (s *stubStore) SearchMemories(_ context.Context, query string, limit int) ([]models.Memory, error) {
	results := make([]models.Memory, 0)
	for _, memory := range s.memories {
		if query == "" || containsFold(memory.Text, query) {
			results = append(results, memory)
		}
	}
	if limit > len(results) {
		limit = len(results)
	}
	return results[:limit], nil
}

func (s *stubStore) DeleteMemory(_ context.Context, id int64) error {
	for idx, memory := range s.memories {
		if memory.ID == id {
			s.memories = append(s.memories[:idx], s.memories[idx+1:]...)
			return nil
		}
	}
	return skills.ErrNotFound
}

func TestRememberSkillStoresMemory(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	result, err := NewRememberSkill(store, true).Execute(context.Background(), skills.Input{
		Args:   map[string]string{"text": "host: Mac mini is the inference node #homelab"},
		Source: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "Saved memory #1" {
		t.Fatalf("unexpected response: %q", result.Text)
	}
	if len(store.memories) != 1 {
		t.Fatalf("unexpected memories: %#v", store.memories)
	}
	if store.memories[0].Kind != "host" || store.memories[0].Source != "telegram" {
		t.Fatalf("unexpected memory metadata: %#v", store.memories[0])
	}
	if len(store.memories[0].Tags) != 1 || store.memories[0].Tags[0] != "homelab" {
		t.Fatalf("unexpected memory tags: %#v", store.memories[0].Tags)
	}
}

func TestListSkillSearchesMemories(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		memories: []models.Memory{
			{ID: 2, Kind: "service", Text: "Jitsi runs on the VPS"},
			{ID: 1, Kind: "host", Text: "Mac mini is the main inference node"},
		},
	}

	result, err := NewListSkill(store, 10, true).Execute(context.Background(), skills.Input{
		Args: map[string]string{"query": "jitsi"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := "Matching memories:\n- #2 [service] Jitsi runs on the VPS"; result.Text != want {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestForgetSkillDeletesByText(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		memories: []models.Memory{
			{ID: 2, Kind: "service", Text: "Jitsi runs on the VPS"},
			{ID: 1, Kind: "host", Text: "Mac mini is the main inference node"},
		},
	}

	result, err := NewForgetSkill(store, 10, true).Execute(context.Background(), skills.Input{
		Args: map[string]string{"ref": "Jitsi runs on the VPS"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "Forgot memory #2" {
		t.Fatalf("unexpected response: %q", result.Text)
	}
	if len(store.memories) != 1 || store.memories[0].ID != 1 {
		t.Fatalf("unexpected remaining memories: %#v", store.memories)
	}
}

func TestRememberSkillReturnsDisabledMessage(t *testing.T) {
	t.Parallel()

	_, err := NewRememberSkill(&stubStore{}, false).Execute(context.Background(), skills.Input{
		Args: map[string]string{"text": "remember this"},
	})
	if err == nil {
		t.Fatal("expected disabled memory skill to fail")
	}
	var userErr skills.UserFacingError
	if !errors.As(err, &userErr) || userErr.UserMessage() != "memory is disabled" {
		t.Fatalf("unexpected disabled error: %v", err)
	}
}

func containsFold(text, query string) bool {
	return len(query) == 0 || strings.Contains(strings.ToLower(text), strings.ToLower(query))
}
