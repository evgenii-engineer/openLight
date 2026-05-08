package visualwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills"
	"openlight/internal/storage"
	visualwatchservice "openlight/internal/visualwatch"
)

type stubRepository struct {
	created models.VisualWatch
	stored  []models.VisualWatch
}

func (s *stubRepository) CreateVisualWatch(_ context.Context, watch models.VisualWatch) (models.VisualWatch, error) {
	watch.ID = int64(len(s.stored) + 1)
	if watch.CreatedAt.IsZero() {
		watch.CreatedAt = time.Now().UTC()
	}
	if watch.UpdatedAt.IsZero() {
		watch.UpdatedAt = watch.CreatedAt
	}
	s.created = watch
	s.stored = append(s.stored, watch)
	return watch, nil
}

func (s *stubRepository) GetVisualWatch(_ context.Context, id int64) (models.VisualWatch, bool, error) {
	for _, w := range s.stored {
		if w.ID == id {
			return w, true, nil
		}
	}
	return models.VisualWatch{}, false, nil
}

func (s *stubRepository) ListVisualWatches(_ context.Context, _ storage.VisualWatchListOptions) ([]models.VisualWatch, error) {
	return s.stored, nil
}

func (s *stubRepository) UpdateVisualWatch(_ context.Context, _ models.VisualWatch) error { return nil }
func (s *stubRepository) DeleteVisualWatch(_ context.Context, id int64) error {
	for idx, w := range s.stored {
		if w.ID == id {
			s.stored = append(s.stored[:idx], s.stored[idx+1:]...)
			return nil
		}
	}
	return nil
}

// remaining repository methods (unused here)
func (s *stubRepository) SaveMessage(context.Context, models.Message) error { return nil }
func (s *stubRepository) ListMessagesByChat(context.Context, int64, int) ([]models.Message, error) {
	return nil, nil
}
func (s *stubRepository) SaveSkillCall(context.Context, models.SkillCall) error                    { return nil }
func (s *stubRepository) AddNote(context.Context, string) (models.Note, error)                     { return models.Note{}, nil }
func (s *stubRepository) ListNotes(context.Context, int) ([]models.Note, error)                    { return nil, nil }
func (s *stubRepository) DeleteNote(context.Context, int64) error                                  { return nil }
func (s *stubRepository) AddMemory(context.Context, models.Memory) (models.Memory, error)          { return models.Memory{}, nil }
func (s *stubRepository) ListMemories(context.Context, int) ([]models.Memory, error)               { return nil, nil }
func (s *stubRepository) SearchMemories(context.Context, string, int) ([]models.Memory, error)     { return nil, nil }
func (s *stubRepository) DeleteMemory(context.Context, int64) error                                { return nil }
func (s *stubRepository) CreateWatch(context.Context, models.Watch) (models.Watch, error) {
	return models.Watch{}, nil
}
func (s *stubRepository) ListWatches(context.Context, storage.WatchListOptions) ([]models.Watch, error) {
	return nil, nil
}
func (s *stubRepository) GetWatch(context.Context, int64) (models.Watch, bool, error) {
	return models.Watch{}, false, nil
}
func (s *stubRepository) UpdateWatch(context.Context, models.Watch) error { return nil }
func (s *stubRepository) DeleteWatch(context.Context, int64) error        { return nil }
func (s *stubRepository) CreateWatchIncident(context.Context, models.WatchIncident) (models.WatchIncident, error) {
	return models.WatchIncident{}, nil
}
func (s *stubRepository) GetWatchIncident(context.Context, int64) (models.WatchIncident, bool, error) {
	return models.WatchIncident{}, false, nil
}
func (s *stubRepository) GetOpenWatchIncident(context.Context, int64) (models.WatchIncident, bool, error) {
	return models.WatchIncident{}, false, nil
}
func (s *stubRepository) ListWatchIncidents(context.Context, storage.WatchIncidentListOptions) ([]models.WatchIncident, error) {
	return nil, nil
}
func (s *stubRepository) ListPendingWatchIncidents(context.Context, int64, time.Time) ([]models.WatchIncident, error) {
	return nil, nil
}
func (s *stubRepository) ListExpiredPendingWatchIncidents(context.Context, time.Time) ([]models.WatchIncident, error) {
	return nil, nil
}
func (s *stubRepository) UpdateWatchIncident(context.Context, models.WatchIncident) error { return nil }
func (s *stubRepository) SetSetting(context.Context, string, string) error                { return nil }
func (s *stubRepository) GetSetting(context.Context, string) (models.Setting, bool, error) {
	return models.Setting{}, false, nil
}
func (s *stubRepository) PruneOlderThan(context.Context, time.Time) (int64, int64, error) {
	return 0, 0, nil
}
func (s *stubRepository) Close() error { return nil }

type stubService struct {
	defaults visualwatchservice.Options
}

func (s *stubService) Defaults() visualwatchservice.Options { return s.defaults }
func (s *stubService) Evaluate(_ context.Context, id int64) (visualwatchservice.EvaluateResult, error) {
	return visualwatchservice.EvaluateResult{WatchID: id}, nil
}

func TestAddSkillParsesOptions(t *testing.T) {
	t.Parallel()
	repo := &stubRepository{}
	service := &stubService{defaults: visualwatchservice.Options{
		DefaultInterval:  10 * time.Minute,
		DefaultThreshold: 0.20,
		DefaultCooldown:  20 * time.Minute,
	}}
	skill := newAddSkill(repo, service)

	result, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 7,
		Args: map[string]string{
			"url":     "https://example.com/drop",
			"options": "interval=5m threshold=10% keywords=42,in stock notify=both name=drop",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if repo.created.URL != "https://example.com/drop" {
		t.Fatalf("URL not stored, got %q", repo.created.URL)
	}
	if repo.created.Name != "drop" {
		t.Fatalf("name not parsed, got %q", repo.created.Name)
	}
	if repo.created.Interval != 5*time.Minute {
		t.Fatalf("interval not parsed, got %v", repo.created.Interval)
	}
	if repo.created.DiffThreshold > 0.11 || repo.created.DiffThreshold < 0.09 {
		t.Fatalf("threshold not parsed, got %v", repo.created.DiffThreshold)
	}
	if !repo.created.NotifyOnChange || !repo.created.NotifyOnKeywords {
		t.Fatalf("notify=both not applied, got change=%v keywords=%v", repo.created.NotifyOnChange, repo.created.NotifyOnKeywords)
	}
	if len(repo.created.Keywords) != 2 || repo.created.Keywords[0] != "42" || repo.created.Keywords[1] != "in stock" {
		t.Fatalf("keywords not parsed, got %v", repo.created.Keywords)
	}
	if result.Text == "" {
		t.Fatalf("expected non-empty text reply")
	}
}

func TestAddSkillRejectsKeywordsModeWithoutKeywords(t *testing.T) {
	t.Parallel()
	repo := &stubRepository{}
	service := &stubService{}
	skill := newAddSkill(repo, service)
	_, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 7,
		Args: map[string]string{
			"url":     "https://example.com",
			"options": "notify=keywords",
		},
	})
	if !errors.Is(err, skills.ErrInvalidArguments) {
		t.Fatalf("expected ErrInvalidArguments, got %v", err)
	}
}
