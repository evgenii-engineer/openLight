package memory

import (
	"context"
	"testing"
	"time"
)

func TestRememberFactSupersedesRatherThanOverwrites(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	first, superseded, err := RememberFact(ctx, h.store, Fact{
		Subject:   "raspberry",
		Predicate: "storage",
		Value:     "1 TB SSD",
		Category:  "hardware",
	})
	requireNoError(t, err, "first fact")
	if superseded {
		t.Fatal("the first value of a pair cannot supersede anything")
	}

	// Give the two facts distinct timestamps so the validity interval is
	// observably closed rather than zero-length.
	later := time.Now().UTC().Add(time.Hour)
	second, superseded, err := RememberFact(ctx, h.store, Fact{
		Subject:   "raspberry",
		Predicate: "storage",
		Value:     "4 TB SSD",
		Category:  "hardware",
		ValidFrom: later,
	})
	requireNoError(t, err, "second fact")
	if !superseded {
		t.Fatal("a changed value must supersede the previous one")
	}

	current, err := h.store.CurrentFact(ctx, "raspberry", "storage")
	requireNoError(t, err, "current fact")
	if current.Value != "4 TB SSD" {
		t.Fatalf("current value = %q, want the new one", current.Value)
	}
	if !current.ValidTo.IsZero() {
		t.Fatal("the new fact should still be open-ended")
	}

	// The old row is kept, closed, and linked — history stays
	// answerable and a bad extraction stays auditable.
	facts, err := h.store.SearchFacts(ctx, nil, 50)
	requireNoError(t, err, "search facts")
	if len(facts) != 1 {
		t.Fatalf("expected exactly one *current* fact, got %d", len(facts))
	}

	old, err := factByID(ctx, h.store, first.ID)
	requireNoError(t, err, "load superseded fact")
	if old.ValidTo.IsZero() {
		t.Fatal("the superseded fact is still marked current")
	}
	if !old.ValidTo.Equal(later) {
		t.Fatalf("valid_to = %v, want the new fact's valid_from %v", old.ValidTo, later)
	}
	if old.SupersededBy != second.ID {
		t.Fatalf("superseded_by = %q, want %q", old.SupersededBy, second.ID)
	}
}

func TestRememberFactIgnoresRestatedValue(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	first, _, err := RememberFact(ctx, h.store, Fact{Subject: "pi", Predicate: "ram", Value: "8 GB"})
	requireNoError(t, err, "first")

	second, superseded, err := RememberFact(ctx, h.store, Fact{Subject: "Pi", Predicate: "RAM", Value: "8 GB"})
	requireNoError(t, err, "restate")

	if superseded {
		t.Fatal("restating the same value must not create a history entry")
	}
	if second.ID != first.ID {
		t.Fatalf("restating produced a new row: %s vs %s", second.ID, first.ID)
	}
	if !second.ValidFrom.Equal(first.ValidFrom) {
		t.Fatal("restating moved valid_from, losing when the fact became true")
	}
}

func TestRememberFactNormalisesKeysSoCasingDoesNotForkHistory(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, _, err := RememberFact(ctx, h.store, Fact{Subject: "Raspberry Pi", Predicate: "Storage", Value: "1 TB"})
	requireNoError(t, err, "first")
	_, superseded, err := RememberFact(ctx, h.store, Fact{Subject: "raspberry pi", Predicate: "storage", Value: "4 TB"})
	requireNoError(t, err, "second")

	if !superseded {
		t.Fatal("differently-cased keys must resolve to the same fact")
	}

	facts, err := h.store.SearchFacts(ctx, nil, 50)
	requireNoError(t, err, "list")
	if len(facts) != 1 {
		t.Fatalf("expected one current fact, got %d", len(facts))
	}
}

func TestForgetFactClosesItWithoutDeleting(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	fact, _, err := RememberFact(ctx, h.store, Fact{Subject: "mac", Predicate: "role", Value: "brain"})
	requireNoError(t, err, "remember")

	requireNoError(t, h.service.Forget(ctx, fact.ID), "forget")

	if _, err := h.store.CurrentFact(ctx, "mac", "role"); err == nil {
		t.Fatal("the fact is still current after forget")
	}
	stored, err := factByID(ctx, h.store, fact.ID)
	requireNoError(t, err, "load forgotten fact")
	if stored.ValidTo.IsZero() {
		t.Fatal("forget should close the interval, not delete the row")
	}
}

func TestRememberFactRejectsIncompleteInput(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	for _, fact := range []Fact{
		{Predicate: "storage", Value: "1 TB"},
		{Subject: "pi", Value: "1 TB"},
		{Subject: "pi", Predicate: "storage"},
	} {
		if _, _, err := RememberFact(ctx, h.store, fact); err == nil {
			t.Fatalf("expected an error for %+v", fact)
		}
	}
}

func TestFactQueryTermsDropStopWords(t *testing.T) {
	terms := factQueryTerms("какой диск подключен к raspberry?", 6)

	joined := map[string]bool{}
	for _, term := range terms {
		joined[term] = true
	}
	if joined["какой"] {
		t.Fatalf("stop word survived: %v", terms)
	}
	if !joined["диск"] || !joined["raspberry"] {
		t.Fatalf("content words were dropped: %v", terms)
	}
}

func TestNormalizeCategoryMapsFreeFormLabels(t *testing.T) {
	cases := map[string]string{
		"hardware/configuration":         CategoryHardware,
		"Preferences":                    CategoryPreference,
		"stable environment information": CategoryEnvironment,
		"something else entirely":        CategoryOther,
	}
	for input, want := range cases {
		if got := NormalizeCategory(input); got != want {
			t.Fatalf("NormalizeCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

// factByID reads any fact, current or superseded. Only tests need this;
// production code always goes through the current-fact path.
func factByID(ctx context.Context, store *Store, id string) (Fact, error) {
	row := store.db.QueryRowContext(ctx,
		`SELECT id, subject, predicate, value, category, confidence, source_id,
		        valid_from, valid_to, superseded_by, created_at, updated_at
		   FROM memory_facts WHERE id = ?`, id)
	return scanFact(row)
}
