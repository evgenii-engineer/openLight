package visualwatch

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"openlight/internal/models"
	"openlight/internal/skills/browser"
	"openlight/internal/skills/ocr"
	"openlight/internal/storage"
)

// memoryRepository is a tiny in-memory subset of storage.Repository used to
// drive the visualwatch poller without touching SQLite.
type memoryRepository struct {
	mu      sync.Mutex
	nextID  int64
	watches map[int64]models.VisualWatch
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{watches: map[int64]models.VisualWatch{}}
}

func (m *memoryRepository) CreateVisualWatch(_ context.Context, watch models.VisualWatch) (models.VisualWatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	watch.ID = m.nextID
	if watch.CreatedAt.IsZero() {
		watch.CreatedAt = time.Now().UTC()
	}
	if watch.UpdatedAt.IsZero() {
		watch.UpdatedAt = watch.CreatedAt
	}
	m.watches[watch.ID] = watch
	return watch, nil
}

func (m *memoryRepository) GetVisualWatch(_ context.Context, id int64) (models.VisualWatch, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	watch, ok := m.watches[id]
	return watch, ok, nil
}

func (m *memoryRepository) ListVisualWatches(_ context.Context, options storage.VisualWatchListOptions) ([]models.VisualWatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.VisualWatch, 0, len(m.watches))
	for _, watch := range m.watches {
		if options.EnabledOnly && !watch.Enabled {
			continue
		}
		if options.ChatID != 0 && watch.TelegramChatID != options.ChatID {
			continue
		}
		out = append(out, watch)
	}
	return out, nil
}

func (m *memoryRepository) UpdateVisualWatch(_ context.Context, watch models.VisualWatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	watch.UpdatedAt = time.Now().UTC()
	m.watches[watch.ID] = watch
	return nil
}

func (m *memoryRepository) DeleteVisualWatch(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.watches, id)
	return nil
}

// Stub out the rest of the storage.Repository surface so this fake satisfies
// the interface (the visualwatch service only uses the visual-watch methods).
func (m *memoryRepository) SaveMessage(context.Context, models.Message) error                         { return nil }
func (m *memoryRepository) ListMessagesByChat(context.Context, int64, int) ([]models.Message, error) { return nil, nil }
func (m *memoryRepository) SaveSkillCall(context.Context, models.SkillCall) error                    { return nil }
func (m *memoryRepository) AddNote(context.Context, string) (models.Note, error)                     { return models.Note{}, nil }
func (m *memoryRepository) ListNotes(context.Context, int) ([]models.Note, error)                    { return nil, nil }
func (m *memoryRepository) DeleteNote(context.Context, int64) error                                  { return nil }
func (m *memoryRepository) AddMemory(context.Context, models.Memory) (models.Memory, error) {
	return models.Memory{}, nil
}
func (m *memoryRepository) ListMemories(context.Context, int) ([]models.Memory, error)        { return nil, nil }
func (m *memoryRepository) SearchMemories(context.Context, string, int) ([]models.Memory, error) {
	return nil, nil
}
func (m *memoryRepository) DeleteMemory(context.Context, int64) error { return nil }
func (m *memoryRepository) CreateWatch(context.Context, models.Watch) (models.Watch, error) {
	return models.Watch{}, nil
}
func (m *memoryRepository) ListWatches(context.Context, storage.WatchListOptions) ([]models.Watch, error) {
	return nil, nil
}
func (m *memoryRepository) GetWatch(context.Context, int64) (models.Watch, bool, error) {
	return models.Watch{}, false, nil
}
func (m *memoryRepository) UpdateWatch(context.Context, models.Watch) error { return nil }
func (m *memoryRepository) DeleteWatch(context.Context, int64) error        { return nil }
func (m *memoryRepository) CreateWatchIncident(context.Context, models.WatchIncident) (models.WatchIncident, error) {
	return models.WatchIncident{}, nil
}
func (m *memoryRepository) GetWatchIncident(context.Context, int64) (models.WatchIncident, bool, error) {
	return models.WatchIncident{}, false, nil
}
func (m *memoryRepository) GetOpenWatchIncident(context.Context, int64) (models.WatchIncident, bool, error) {
	return models.WatchIncident{}, false, nil
}
func (m *memoryRepository) ListWatchIncidents(context.Context, storage.WatchIncidentListOptions) ([]models.WatchIncident, error) {
	return nil, nil
}
func (m *memoryRepository) ListPendingWatchIncidents(context.Context, int64, time.Time) ([]models.WatchIncident, error) {
	return nil, nil
}
func (m *memoryRepository) ListExpiredPendingWatchIncidents(context.Context, time.Time) ([]models.WatchIncident, error) {
	return nil, nil
}
func (m *memoryRepository) UpdateWatchIncident(context.Context, models.WatchIncident) error { return nil }
func (m *memoryRepository) SetSetting(context.Context, string, string) error                { return nil }
func (m *memoryRepository) GetSetting(context.Context, string) (models.Setting, bool, error) {
	return models.Setting{}, false, nil
}
func (m *memoryRepository) Close() error { return nil }

type stubBrowser struct {
	enabled         bool
	screenshotPaths []string
	textPreview     string
	idx             int
}

func (b *stubBrowser) Enabled() bool { return b.enabled }

func (b *stubBrowser) Screenshot(_ context.Context, _ string) (browser.Response, error) {
	idx := b.idx
	if idx >= len(b.screenshotPaths) {
		idx = len(b.screenshotPaths) - 1
	}
	b.idx++
	return browser.Response{OK: true, ScreenshotPath: b.screenshotPaths[idx], Title: "Sample"}, nil
}

func (b *stubBrowser) Text(_ context.Context, _ string) (browser.Response, error) {
	return browser.Response{OK: true, TextPreview: b.textPreview, Title: "Sample"}, nil
}

type stubOCR struct {
	enabled bool
	text    string
}

func (s *stubOCR) Enabled() bool { return s.enabled }

func (s *stubOCR) Extract(_ context.Context, _ string) (ocr.Result, error) {
	return ocr.Result{Text: s.text}, nil
}

type captureNotifier struct {
	mu     sync.Mutex
	alerts []string
}

func (c *captureNotifier) SendVisualAlert(_ context.Context, _ int64, text, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alerts = append(c.alerts, text)
	return nil
}

func writeSolidPNG(t *testing.T, dir, name string, c color.RGBA) string {
	t.Helper()
	const w, h = 64, 64
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	return path
}

func newTestService(t *testing.T, repo *memoryRepository, b BrowserManager, o OCRManager, n Notifier) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(repo, b, o, n, logger, Options{
		PollInterval:     50 * time.Millisecond,
		BaselinesDir:     filepath.Join(t.TempDir(), "baselines"),
		DefaultInterval:  10 * time.Second,
		DefaultThreshold: 0.10,
		DefaultCooldown:  0,
		RequestTimeout:   2 * time.Second,
	})
}

func TestEvaluateInitializesBaseline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	white := writeSolidPNG(t, dir, "first.png", color.RGBA{255, 255, 255, 255})

	repo := newMemoryRepository()
	created, err := repo.CreateVisualWatch(context.Background(), models.VisualWatch{
		TelegramChatID:   42,
		Name:             "test",
		URL:              "https://example.com",
		DiffThreshold:    0.10,
		Interval:         time.Second,
		NotifyOnChange:   true,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	notifier := &captureNotifier{}
	service := newTestService(t, repo, &stubBrowser{enabled: true, screenshotPaths: []string{white}}, nil, notifier)

	result, err := service.Evaluate(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.NewBaseline {
		t.Fatalf("expected NewBaseline=true on first run, got %#v", result)
	}
	if len(notifier.alerts) != 0 {
		t.Fatalf("did not expect alert on first run, got %v", notifier.alerts)
	}
	stored, _, err := repo.GetVisualWatch(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.BaselinePath == "" {
		t.Fatalf("expected baseline_path to be set after first run")
	}
}

func TestEvaluateNotifiesOnVisualChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	white := writeSolidPNG(t, dir, "white.png", color.RGBA{255, 255, 255, 255})
	black := writeSolidPNG(t, dir, "black.png", color.RGBA{0, 0, 0, 255})

	repo := newMemoryRepository()
	created, err := repo.CreateVisualWatch(context.Background(), models.VisualWatch{
		TelegramChatID:   42,
		Name:             "test",
		URL:              "https://example.com",
		DiffThreshold:    0.10,
		Interval:         time.Second,
		NotifyOnChange:   true,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	notifier := &captureNotifier{}
	browserMgr := &stubBrowser{enabled: true, screenshotPaths: []string{white, black}}
	service := newTestService(t, repo, browserMgr, nil, notifier)

	// First run establishes baseline; second run flips to a fully different image.
	if _, err := service.Evaluate(context.Background(), created.ID); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	result, err := service.Evaluate(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected Changed=true on visual flip, got %#v", result)
	}
	if !result.Notified {
		t.Fatalf("expected Notified=true on visual flip, got %#v", result)
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected 1 alert, got %v", notifier.alerts)
	}
}

func TestEvaluateNotifiesOnKeywordMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	white := writeSolidPNG(t, dir, "white.png", color.RGBA{255, 255, 255, 255})

	repo := newMemoryRepository()
	created, err := repo.CreateVisualWatch(context.Background(), models.VisualWatch{
		TelegramChatID:   42,
		Name:             "drop",
		URL:              "https://example.com",
		Keywords:         []string{"42", "in stock"},
		DiffThreshold:    0.10,
		Interval:         time.Second,
		NotifyOnKeywords: true,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	notifier := &captureNotifier{}
	service := newTestService(t, repo,
		&stubBrowser{enabled: true, screenshotPaths: []string{white, white}, textPreview: "size 42 in stock"},
		&stubOCR{enabled: true, text: "size 42 IN STOCK"},
		notifier,
	)

	// First run: baseline + matched keywords (sees them for the first time).
	result, err := service.Evaluate(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if len(result.KeywordsMatched) != 2 {
		t.Fatalf("expected 2 matched keywords, got %v", result.KeywordsMatched)
	}
	if !result.Notified {
		t.Fatalf("expected notification on first keyword match")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected 1 alert, got %v", notifier.alerts)
	}
}
