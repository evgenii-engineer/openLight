package memory

import (
	"strings"
	"unicode"
)

// RetrievalMode selects how the agent decides whether a message needs a
// memory lookup at all.
type RetrievalMode string

const (
	// ModeHeuristic uses the deterministic classifier below. This is the
	// default and, deliberately, the only mode that costs nothing: the
	// alternative — asking the fast model on every single message — puts
	// a network round trip to the Mac mini in front of "включи свет" and
	// keeps the brain node busy for no benefit.
	ModeHeuristic RetrievalMode = "heuristic"

	// ModeAlways retrieves for every free-form message. Useful while
	// tuning; wasteful in production.
	ModeAlways RetrievalMode = "always"

	// ModeOff disables retrieval without disabling ingestion, so memory
	// keeps accumulating while the read path stays out of the way.
	ModeOff RetrievalMode = "off"
)

// ParseRetrievalMode normalises a config string.
func ParseRetrievalMode(raw string) RetrievalMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ModeAlways):
		return ModeAlways
	case string(ModeOff):
		return ModeOff
	default:
		return ModeHeuristic
	}
}

// recallTriggers are the phrases that make a message worth a memory
// lookup: explicit recall ("помнишь"), history questions ("что мы
// решили"), and possessive state questions ("какой у меня"). Matching is
// done on a lowercased, punctuation-stripped form so "Помнишь?" hits.
var recallTriggers = []string{
	// Russian — explicit recall and history
	"помнишь", "помните", "напомни", "ты помнил", "запомнил",
	"что мы решили", "что решили", "почему мы", "зачем мы", "как мы",
	"что я говорил", "что я просил", "что я делал", "что было",
	"о чем мы", "мы обсуждали", "обсуждали", "договорились",
	"какой у меня", "какая у меня", "какое у меня", "какие у меня",
	"сколько у меня", "где у меня", "что у меня",
	"где документ", "в документе", "в pdf", "в файле", "по документу",
	"что ты знаешь", "откуда ты", "из памяти", "в прошлый раз", "ранее",
	"мой ", "моя ", "мои ", "моё ", "мое ",

	// English — same categories
	"do you remember", "remember when", "you remembered", "recall",
	"what did we", "why did we", "how did we", "what have i",
	"what did i", "we decided", "we agreed", "we discussed",
	"what is my", "what's my", "which of my", "how many of my",
	"where is my", "where did i", "in the document", "in the pdf",
	"in the file", "according to the", "what do you know about",
	"where is the document", "where are the docs", "which document",
	"how do you know", "last time", "previously", "earlier we",
}

// commandLike are the shapes that must never trigger retrieval: device
// control, trivial status checks, greetings. These are the messages the
// user's warning was about — nothing here benefits from a vector search
// and everything here benefits from a fast reply.
var commandLike = []string{
	"включи", "выключи", "запусти", "останови", "перезапусти", "рестарт",
	"ping", "пинг", "статус", "status", "время", "который час", "сколько времени",
	"turn on", "turn off", "start", "stop", "restart", "reboot",
	"привет", "спасибо", "ок", "ага", "угу", "hi", "hello", "thanks", "ok",
}

// ShouldRetrieve reports whether a message is worth a memory lookup.
//
// The rules, in order:
//  1. Slash commands and empty input never retrieve — they route
//     deterministically and have their own arguments.
//  2. Very short messages ("ок", "да") never retrieve.
//  3. Command-like openers never retrieve, even if long.
//  4. An explicit recall trigger always retrieves.
//  5. Otherwise a question that is long enough retrieves — questions are
//     where stale-but-relevant context pays off, statements usually are
//     not.
func ShouldRetrieve(mode RetrievalMode, text string) bool {
	switch mode {
	case ModeOff:
		return false
	case ModeAlways:
		trimmed := strings.TrimSpace(text)
		return trimmed != "" && !strings.HasPrefix(trimmed, "/")
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return false
	}

	normalized := normalizeForGate(trimmed)
	if normalized == "" {
		return false
	}

	words := strings.Fields(normalized)
	if len(words) < 2 {
		return false
	}

	for _, trigger := range recallTriggers {
		if strings.Contains(normalized+" ", trigger) {
			return true
		}
	}

	if isCommandLike(normalized, words) {
		return false
	}

	// A question with some substance to it. Four words filters out
	// "как дела" and "who are you" while keeping "какой диск подключен к
	// raspberry".
	if strings.Contains(trimmed, "?") && len(words) >= 4 {
		return true
	}

	return false
}

func isCommandLike(normalized string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, prefix := range commandLike {
		if words[0] == prefix || strings.HasPrefix(normalized, prefix+" ") {
			return true
		}
	}
	return false
}

// normalizeForGate lowercases and strips punctuation so trigger matching
// is not defeated by "Помнишь," or "what's my…".
func normalizeForGate(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	lastSpace := true
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '-':
			builder.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				builder.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(builder.String())
}
