package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseSmokeNoteID(t *testing.T) {
	t.Parallel()

	got, err := parseSmokeNoteID("Saved note #42")
	if err != nil {
		t.Fatalf("parseSmokeNoteID returned error: %v", err)
	}
	if got != "42" {
		t.Fatalf("unexpected note id: %q", got)
	}
}

func TestSmokeReportRenderTable(t *testing.T) {
	t.Parallel()

	report := SmokeReport{
		Rows: []SmokeRow{
			{Check: "core.ping", Command: "ping", Status: SmokePass, Duration: 25 * time.Millisecond, Summary: "pong"},
			{Check: "services.restart", Command: "restart tailscale", Status: SmokeSkip, Summary: "skipped for safety"},
		},
	}

	text := report.RenderTable()
	for _, fragment := range []string{
		"| Check",
		"core.ping",
		"PASS",
		"SKIP",
		"Result: PASS | pass=1 fail=0 skip=1",
		"Totals: 2/2 completed without failure",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in rendered table, got:\n%s", fragment, text)
		}
	}
}

func TestPreferredRuntime(t *testing.T) {
	t.Parallel()

	if got := preferredRuntime([]string{"python", "sh"}); got != "sh" {
		t.Fatalf("expected sh to be preferred, got %q", got)
	}
	if got := preferredRuntime([]string{"node"}); got != "node" {
		t.Fatalf("expected node fallback, got %q", got)
	}
}
