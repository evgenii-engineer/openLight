package memory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
)

// NormalizeCategory maps a free-form category from the extractor onto
// the fixed set retrieval ranking understands.
func NormalizeCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "hardware", "configuration", "config", "hardware/configuration":
		return CategoryHardware
	case "project", "project_state", "project state", "work":
		return CategoryProject
	case "preference", "preferences", "style":
		return CategoryPreference
	case "decision", "decisions":
		return CategoryDecision
	case "people", "person", "entity", "entities", "people/entities":
		return CategoryEntity
	case "environment", "env", "stable environment information", "infrastructure":
		return CategoryEnvironment
	default:
		return CategoryOther
	}
}

// NormalizeFactKey canonicalises a subject or predicate so
// "Raspberry Pi" and "raspberry pi" supersede each other instead of
// coexisting as two competing "current" facts.
func NormalizeFactKey(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))
	lastSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastSpace = false
		case r == '.' || r == '_' || r == '-':
			builder.WriteRune('.')
			lastSpace = false
		default:
			if !lastSpace {
				builder.WriteByte('.')
				lastSpace = true
			}
		}
	}
	return strings.Trim(builder.String(), ".")
}

// RememberFact writes a fact, superseding the current value for the same
// subject/predicate pair.
//
// Superseding never deletes: the previous row gets valid_to set to the
// new fact's valid_from and a pointer to its replacement. "Raspberry had
// a 1 TB SSD until March, now it has 4 TB" therefore stays answerable
// both ways, and a wrong extraction can be audited rather than silently
// having overwritten the truth.
//
// A repeat of the same value is a no-op: re-stating "the Pi has a 1 TB
// SSD" three times must not produce three history entries.
func RememberFact(ctx context.Context, store *Store, fact Fact) (stored Fact, superseded bool, err error) {
	fact.Subject = NormalizeFactKey(fact.Subject)
	fact.Predicate = NormalizeFactKey(fact.Predicate)
	fact.Value = strings.TrimSpace(fact.Value)
	fact.Category = NormalizeCategory(fact.Category)

	if fact.Subject == "" || fact.Predicate == "" || fact.Value == "" {
		return Fact{}, false, errors.New("memory: fact needs subject, predicate, and value")
	}
	if fact.Confidence <= 0 {
		fact.Confidence = 0.6
	}
	if fact.Confidence > 1 {
		fact.Confidence = 1
	}

	now := time.Now().UTC()
	if fact.ValidFrom.IsZero() {
		fact.ValidFrom = now
	}
	if fact.ID == "" {
		fact.ID = newID()
	}
	fact.CreatedAt = now
	fact.UpdatedAt = now

	current, lookupErr := store.CurrentFact(ctx, fact.Subject, fact.Predicate)
	switch {
	case lookupErr == nil && strings.EqualFold(strings.TrimSpace(current.Value), fact.Value):
		// Same value restated. Keep the original row (and its earlier
		// valid_from) so the timeline stays honest.
		return current, false, nil
	case lookupErr == nil:
		if supErr := store.SupersedeFact(ctx, current.ID, fact.ValidFrom, fact.ID); supErr != nil {
			return Fact{}, false, supErr
		}
		superseded = true
	case errors.Is(lookupErr, ErrNotFound):
		// First value for this pair.
	default:
		return Fact{}, false, lookupErr
	}

	if insertErr := store.InsertFact(ctx, fact); insertErr != nil {
		return Fact{}, false, insertErr
	}
	return fact, superseded, nil
}

// factQueryTerms turns a natural-language question into the LIKE terms
// used to probe structured memory. Stop words are dropped so "какой диск
// подключен к raspberry" probes on "диск", "подключен", "raspberry"
// rather than on "какой".
func factQueryTerms(query string, limit int) []string {
	if limit <= 0 {
		limit = 6
	}
	var terms []string
	seen := map[string]struct{}{}
	for _, field := range strings.Fields(normalizeForGate(query)) {
		if len([]rune(field)) < 3 {
			continue
		}
		if _, stop := factStopWords[field]; stop {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
		if len(terms) >= limit {
			break
		}
	}
	return terms
}

var factStopWords = map[string]struct{}{
	// Russian
	"что": {}, "как": {}, "где": {}, "кто": {}, "чем": {}, "это": {}, "для": {},
	"мне": {}, "меня": {}, "тебя": {}, "мой": {}, "моя": {}, "мои": {}, "моё": {},
	"мое": {}, "есть": {}, "была": {}, "было": {}, "были": {}, "будет": {},
	"какой": {}, "какая": {}, "какие": {}, "какое": {}, "сколько": {},
	"помнишь": {}, "напомни": {}, "скажи": {}, "почему": {}, "зачем": {},
	"когда": {}, "или": {}, "если": {}, "тоже": {}, "очень": {}, "туда": {},
	// English
	"what": {}, "which": {}, "where": {}, "when": {}, "who": {}, "why": {},
	"how": {}, "the": {}, "and": {}, "for": {}, "you": {}, "your": {},
	"our": {}, "does": {}, "did": {}, "was": {}, "were": {}, "are": {},
	"have": {}, "has": {}, "remember": {}, "tell": {}, "about": {}, "with": {},
	"that": {}, "this": {}, "from": {}, "into": {}, "many": {}, "much": {},
}
