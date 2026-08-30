package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"openlight/internal/skills"
)

type recordingLongTerm struct {
	mu sync.Mutex

	texts []string
	facts [][3]string
	err   error
}

func (l *recordingLongTerm) RememberText(_ context.Context, text, _ string, _, _ int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.texts = append(l.texts, text)
	return nil
}

func (l *recordingLongTerm) RememberFact(_ context.Context, subject, predicate, value string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.facts = append(l.facts, [3]string{subject, predicate, value})
	return nil
}

func runRemember(t *testing.T, skill skills.Skill, text string) skills.Result {
	t.Helper()
	result, err := skill.Execute(context.Background(), skills.Input{
		Args: map[string]string{"text": text}, ChatID: 7, UserID: 9, Source: "telegram",
	})
	if err != nil {
		t.Fatalf("execute %q: %v", text, err)
	}
	return result
}

func TestRememberMirrorsFreeTextIntoLongTerm(t *testing.T) {
	store := &stubStore{}
	longTerm := &recordingLongTerm{}
	skill := NewRememberSkillWithLongTerm(store, longTerm, true)

	result := runRemember(t, skill, "у raspberry теперь SSD на 1 ТБ")

	// The old note store keeps working unchanged.
	if len(store.memories) != 1 || store.memories[0].Text != "у raspberry теперь SSD на 1 ТБ" {
		t.Fatalf("note store: %+v", store.memories)
	}
	if len(longTerm.texts) != 1 || longTerm.texts[0] != "у raspberry теперь SSD на 1 ТБ" {
		t.Fatalf("long-term store: %+v", longTerm.texts)
	}
	if len(longTerm.facts) != 0 {
		t.Fatalf("free text must not write a structured fact directly: %+v", longTerm.facts)
	}
	if !strings.Contains(result.Text, "Запомнил") {
		t.Fatalf("reply = %q", result.Text)
	}
}

func TestRememberWritesStructuredFactWithoutTheModel(t *testing.T) {
	store := &stubStore{}
	longTerm := &recordingLongTerm{}
	skill := NewRememberSkillWithLongTerm(store, longTerm, true)

	result := runRemember(t, skill, "raspberry.storage = 4 TB SSD")

	if len(longTerm.facts) != 1 {
		t.Fatalf("expected one fact, got %+v", longTerm.facts)
	}
	if longTerm.facts[0] != [3]string{"raspberry", "storage", "4 TB SSD"} {
		t.Fatalf("parsed wrong: %+v", longTerm.facts[0])
	}
	// The structured form is the deterministic path: no note, no queue,
	// nothing that needs the brain node.
	if len(store.memories) != 0 || len(longTerm.texts) != 0 {
		t.Fatalf("structured form should not also archive text: notes=%+v texts=%+v", store.memories, longTerm.texts)
	}
	if !strings.Contains(result.Text, "4 TB SSD") {
		t.Fatalf("reply = %q", result.Text)
	}
}

func TestRememberWithoutLongTermKeepsOldBehaviour(t *testing.T) {
	store := &stubStore{}
	skill := NewRememberSkillWithLongTerm(store, nil, true)

	result := runRemember(t, skill, "synapse живёт на raspberry")

	if len(store.memories) != 1 {
		t.Fatalf("note store: %+v", store.memories)
	}
	if result.Text != "Saved memory #1" {
		t.Fatalf("reply changed with long-term disabled: %q", result.Text)
	}
}

func TestRememberReportsLongTermFailureWithoutLosingTheNote(t *testing.T) {
	store := &stubStore{}
	longTerm := &recordingLongTerm{err: errors.New("диск переполнен")}
	skill := NewRememberSkillWithLongTerm(store, longTerm, true)

	result := runRemember(t, skill, "какой-то факт")

	// The note still saved, and the user is told the rest did not.
	if len(store.memories) != 1 {
		t.Fatalf("note was lost: %+v", store.memories)
	}
	if !strings.Contains(result.Text, "не попало") {
		t.Fatalf("failure not surfaced: %q", result.Text)
	}
}

func TestStructuredFactNeedsLongTerm(t *testing.T) {
	skill := NewRememberSkillWithLongTerm(&stubStore{}, nil, true)

	_, err := skill.Execute(context.Background(), skills.Input{
		Args: map[string]string{"text": "raspberry.storage = 4 TB"},
	})
	if err == nil {
		t.Fatal("expected an error when long-term memory is off")
	}
	if !strings.Contains(err.Error(), "=") {
		t.Fatalf("error should say what to do instead: %v", err)
	}
}

func TestParseStructuredFact(t *testing.T) {
	cases := []struct {
		in                   string
		subject, pred, value string
		ok                   bool
	}{
		{"raspberry.storage = 4 TB SSD", "raspberry", "storage", "4 TB SSD", true},
		{"  frame37.neon.project_id=sparkling-queen-39963007  ", "frame37.neon", "project_id", "sparkling-queen-39963007", true},
		// No dot: ambiguous subject/predicate, so it falls through to the
		// model-extracted path rather than inventing a key.
		{"цена = 25", "", "", "", false},
		{"у raspberry теперь SSD на 1 ТБ", "", "", "", false},
		{"raspberry.storage =", "", "", "", false},
		{".storage = 4 TB", "", "", "", false},
		{"raspberry. = 4 TB", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, tc := range cases {
		subject, pred, value, ok := parseStructuredFact(tc.in)
		if ok != tc.ok || subject != tc.subject || pred != tc.pred || value != tc.value {
			t.Errorf("parseStructuredFact(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				tc.in, subject, pred, value, ok, tc.subject, tc.pred, tc.value, tc.ok)
		}
	}
}

func TestRememberAliasesCoverRussian(t *testing.T) {
	definition := NewRememberSkillWithLongTerm(&stubStore{}, nil, true).Definition()

	aliases := map[string]bool{}
	for _, alias := range definition.Aliases {
		aliases[alias] = true
	}
	// The first word of a message resolves against these before the LLM
	// classifier runs, so a Russian alias is what makes "запомни ..."
	// immune to misrouting.
	for _, want := range []string{"remember", "запомни"} {
		if !aliases[want] {
			t.Errorf("alias %q missing from %v", want, definition.Aliases)
		}
	}
}
