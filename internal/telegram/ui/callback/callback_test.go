package callback

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Action{
		{Kind: KindHome},
		{Kind: KindGroup, Target: "system"},
		{Kind: KindSkill, Target: "cpu"},
		{Kind: KindSkill, Target: "browser_open", Extra: "abcd1234"},
		{Kind: KindAction, Target: "logs", Extra: "nginx"},
		{Kind: KindBack, Target: "groups"},
		{Kind: KindBack, Target: "g", Extra: "system"},
		{Kind: KindPage, Target: "system", Extra: "2"},
		{Kind: KindConfirm, Target: "tok123"},
		{Kind: KindQuick, Target: "ollama_restart"},
	}
	for _, want := range cases {
		encoded := Encode(want)
		if len(encoded) > MaxBytes {
			t.Errorf("encoded callback %q exceeds %d bytes", encoded, MaxBytes)
		}
		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode(%q): %v", encoded, err)
		}
		if got != want {
			t.Errorf("round-trip mismatch: encoded=%q got=%+v want=%+v", encoded, got, want)
		}
	}
}

func TestDecodeRejectsEmpty(t *testing.T) {
	if _, err := Decode(""); err == nil {
		t.Fatal("expected error for empty data")
	}
	if _, err := Decode("   "); err == nil {
		t.Fatal("expected error for whitespace-only data")
	}
}

func TestDecodeKeepsExtraColons(t *testing.T) {
	a, err := Decode("a:logs:my:service")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if a.Kind != "a" || a.Target != "logs" || a.Extra != "my:service" {
		t.Fatalf("unexpected action: %+v", a)
	}
}

func TestBackToGroupRoundTrip(t *testing.T) {
	got, err := Decode(BackToGroup("system"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Kind != KindBack || got.Target != "g" || got.Extra != "system" {
		t.Fatalf("unexpected back-to-group action: %+v", got)
	}
}

func TestPageRoundTrip(t *testing.T) {
	got, err := Decode(Page("services", 4))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Kind != KindPage || got.Target != "services" || got.PageNumber() != 4 {
		t.Fatalf("unexpected page action: %+v", got)
	}
}

func TestPageNumber(t *testing.T) {
	a := Action{Kind: KindPage, Target: "system", Extra: "5"}
	if a.PageNumber() != 5 {
		t.Fatalf("expected 5, got %d", a.PageNumber())
	}
	a.Extra = "abc"
	if a.PageNumber() != 0 {
		t.Fatalf("expected 0 for invalid input, got %d", a.PageNumber())
	}
	a.Extra = "-3"
	if a.PageNumber() != 0 {
		t.Fatalf("expected 0 for negative input, got %d", a.PageNumber())
	}
}
