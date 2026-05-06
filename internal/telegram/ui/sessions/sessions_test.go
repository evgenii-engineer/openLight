package sessions

import (
	"testing"
	"time"
)

func TestInputFlowLifecycle(t *testing.T) {
	s := NewStore(time.Minute)
	s.StartInput(42, "browser_open", "g:browser")
	flow, ok := s.Pending(42)
	if !ok {
		t.Fatal("expected pending flow")
	}
	if flow.SkillName != "browser_open" {
		t.Fatalf("unexpected skill: %q", flow.SkillName)
	}
	if _, ok := s.AdvanceInput(42, "url", "https://example.com"); !ok {
		t.Fatal("expected AdvanceInput to succeed")
	}
	flow2, _ := s.Pending(42)
	if flow2.Collected["url"] != "https://example.com" {
		t.Fatalf("unexpected collected: %+v", flow2.Collected)
	}
	if flow2.StepIndex != 1 {
		t.Fatalf("unexpected step index: %d", flow2.StepIndex)
	}
	s.ClearInput(42)
	if _, ok := s.Pending(42); ok {
		t.Fatal("expected no pending flow after clear")
	}
}

func TestInputFlowExpiry(t *testing.T) {
	s := NewStore(time.Hour)
	s.nowFunc = func() time.Time { return time.Unix(0, 0) }
	s.StartInput(1, "skill", "")
	s.nowFunc = func() time.Time { return time.Unix(0, 0).Add(2 * time.Hour) }
	if _, ok := s.Pending(1); ok {
		t.Fatal("expected expiry to drop pending flow")
	}
}

func TestStoreAndLoadArgs(t *testing.T) {
	s := NewStore(time.Minute)
	token := s.StoreArgs(map[string]string{"path": "/tmp/x"})
	if token == "" {
		t.Fatal("expected token")
	}
	loaded, ok := s.LoadArgs(token)
	if !ok {
		t.Fatal("expected stored args to load")
	}
	if loaded["path"] != "/tmp/x" {
		t.Fatalf("unexpected args: %+v", loaded)
	}
	if _, ok := s.LoadArgs(""); ok {
		t.Fatal("empty token should not match")
	}
}

func TestMutationConfirmAndCancel(t *testing.T) {
	s := NewStore(time.Minute)
	token := s.StoreMutation(PendingMutation{
		SkillName: "service_restart",
		Args:      map[string]string{"service": "ollama"},
		UserID:    7,
	})
	if _, ok := s.ClaimMutation(token, 8); ok {
		t.Fatal("claim should fail for wrong user")
	}
	got, ok := s.ClaimMutation(token, 7)
	if !ok {
		t.Fatal("expected claim to succeed")
	}
	if got.SkillName != "service_restart" || got.Args["service"] != "ollama" {
		t.Fatalf("unexpected mutation: %+v", got)
	}
	if _, ok := s.ClaimMutation(token, 7); ok {
		t.Fatal("second claim should fail")
	}

	token2 := s.StoreMutation(PendingMutation{SkillName: "x", UserID: 9})
	if !s.CancelMutation(token2, 9) {
		t.Fatal("cancel should succeed")
	}
	if _, ok := s.ClaimMutation(token2, 9); ok {
		t.Fatal("cancelled mutation should not be claimable")
	}
}
