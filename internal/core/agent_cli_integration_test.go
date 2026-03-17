package core_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openlight/internal/auth"
	clitransport "openlight/internal/cli"
	"openlight/internal/core"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/skills/notes"
	"openlight/internal/storage/sqlite"
)

func TestAgentRunWithCLITransport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(notes.NewListSkill(repo, 10))

	var output bytes.Buffer
	transport := clitransport.NewTransport(clitransport.Options{
		In:     bytes.NewBufferString("note buy milk\nnotes\n"),
		Out:    &output,
		UserID: 100,
		ChatID: 200,
	})

	agent := core.NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		time.Second,
	)

	if err := agent.Run(ctx); err != nil {
		t.Fatalf("agent.Run returned error: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "Saved note #1") {
		t.Fatalf("expected add response in output, got %q", text)
	}
	if !strings.Contains(text, "Notes:\n- #1 buy milk") {
		t.Fatalf("expected notes response in output, got %q", text)
	}
}
