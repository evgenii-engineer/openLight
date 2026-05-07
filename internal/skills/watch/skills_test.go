package watch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openlight/internal/skills"
	serviceskills "openlight/internal/skills/services"
	systemskills "openlight/internal/skills/system"
	"openlight/internal/storage/sqlite"
	watchengine "openlight/internal/watch"
)

// stubServiceManager is the smallest implementation of serviceskills.Manager
// the watch engine accepts for setup. The watch skills under test do not
// actually exercise it — they go through the watch engine which only needs a
// non-nil dependency.
type stubServiceManager struct{}

func (stubServiceManager) Targets() []serviceskills.Info { return nil }
func (stubServiceManager) List(context.Context) ([]serviceskills.Info, error) {
	return nil, nil
}
func (stubServiceManager) Status(context.Context, string) (serviceskills.Info, error) {
	return serviceskills.Info{}, nil
}
func (stubServiceManager) Restart(context.Context, string) error { return nil }
func (stubServiceManager) Logs(context.Context, string, int) (string, error) {
	return "", nil
}
func (stubServiceManager) Exec(context.Context, string, ...string) (string, error) {
	return "", nil
}

// stubSystemProvider satisfies systemskills.Provider with deterministic
// values. The watch tests below do not actually trigger metric watches; they
// exercise the add/list/remove flows.
type stubSystemProvider struct{}

func (stubSystemProvider) CPUUsage(context.Context) (float64, error) { return 1, nil }
func (stubSystemProvider) MemoryStats(context.Context) (systemskills.MemoryStats, error) {
	return systemskills.MemoryStats{Total: 1, Used: 1}, nil
}
func (stubSystemProvider) DiskStats(context.Context, string) (systemskills.DiskStats, error) {
	return systemskills.DiskStats{Total: 1, Used: 1}, nil
}
func (stubSystemProvider) Uptime(context.Context) (time.Duration, error) { return 0, nil }
func (stubSystemProvider) Hostname(context.Context) (string, error)      { return "test", nil }
func (stubSystemProvider) IPAddresses(context.Context) ([]string, error) {
	return []string{"127.0.0.1"}, nil
}
func (stubSystemProvider) Temperature(context.Context) (float64, error) { return 0, nil }

func newWatchEngine(t *testing.T) *watchengine.Service {
	t.Helper()
	repo, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "watch.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	registry := skills.NewRegistry()
	return watchengine.NewService(repo, registry, nil, stubSystemProvider{}, stubServiceManager{}, nil, watchengine.Options{
		PollInterval:   time.Minute,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})
}

func TestWatchAddSkillRejectsEmptySpec(t *testing.T) {
	t.Parallel()

	skill := NewAddSkill(newWatchEngine(t))
	_, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 1,
		UserID: 1,
		Args:   map[string]string{"spec": ""},
	})
	if err == nil {
		t.Fatalf("expected empty spec to be rejected")
	}
}

func TestWatchAddSkillCreatesWatchAndListsIt(t *testing.T) {
	t.Parallel()

	engine := newWatchEngine(t)
	add := NewAddSkill(engine)
	list := NewListSkill(engine)

	addResult, err := add.Execute(context.Background(), skills.Input{
		ChatID: 42,
		UserID: 7,
		Args:   map[string]string{"spec": "cpu > 90% for 5m cooldown 15m"},
	})
	if err != nil {
		t.Fatalf("watch_add Execute: %v", err)
	}
	if !strings.Contains(addResult.Text, "Watch created") {
		t.Fatalf("expected confirmation, got %q", addResult.Text)
	}

	listResult, err := list.Execute(context.Background(), skills.Input{ChatID: 42})
	if err != nil {
		t.Fatalf("watch_list Execute: %v", err)
	}
	if !strings.Contains(listResult.Text, "cpu") {
		t.Fatalf("expected new watch in list output, got %q", listResult.Text)
	}
}

func TestWatchListSkillEmpty(t *testing.T) {
	t.Parallel()

	engine := newWatchEngine(t)
	result, err := NewListSkill(engine).Execute(context.Background(), skills.Input{ChatID: 1})
	if err != nil {
		t.Fatalf("watch_list Execute: %v", err)
	}
	if !strings.Contains(result.Text, "No watches") {
		t.Fatalf("expected friendly empty message, got %q", result.Text)
	}
}
