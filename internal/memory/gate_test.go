package memory

import "testing"

func TestShouldRetrieveSkipsCommandsAndChatter(t *testing.T) {
	// These are the messages the retrieval gate exists to protect: none
	// of them benefits from a vector lookup, and every one of them would
	// otherwise cost a round trip to the brain node.
	skip := []string{
		"включи свет",
		"выключи свет в спальне",
		"перезапусти synapse",
		"ping",
		"статус",
		"сколько времени",
		"привет",
		"спасибо",
		"ок",
		"ага",
		"turn on the kitchen light",
		"restart the tailscale service",
		"/status",
		"/chat расскажи анекдот",
		"",
		"   ",
	}
	for _, text := range skip {
		if ShouldRetrieve(ModeHeuristic, text) {
			t.Errorf("ShouldRetrieve(%q) = true, want false", text)
		}
	}
}

func TestShouldRetrieveCatchesRecallQuestions(t *testing.T) {
	retrieve := []string{
		"помнишь что мы решили по поводу бэкапов?",
		"какой у меня диск на raspberry",
		"что я говорил про Mac mini",
		"почему мы выбрали qdrant",
		"что мы решили с деплоем",
		"где документ по настройке",
		"что было с synapse на прошлой неделе",
		"do you remember what we decided about backups",
		"what is my raspberry storage configuration",
		"why did we choose qdrant over pgvector",
		"where is the document about deployment",
		"what did i say about the mac mini",
	}
	for _, text := range retrieve {
		if !ShouldRetrieve(ModeHeuristic, text) {
			t.Errorf("ShouldRetrieve(%q) = false, want true", text)
		}
	}
}

func TestShouldRetrieveAcceptsSubstantialQuestions(t *testing.T) {
	if !ShouldRetrieve(ModeHeuristic, "как настроен reverse proxy на этой машине?") {
		t.Error("a substantial question should retrieve")
	}
	// Short questions are chatter, not recall.
	if ShouldRetrieve(ModeHeuristic, "как дела?") {
		t.Error("small talk should not retrieve")
	}
}

func TestRetrievalModesOverrideTheHeuristic(t *testing.T) {
	if ShouldRetrieve(ModeOff, "помнишь что мы решили?") {
		t.Error("mode=off must never retrieve")
	}
	if !ShouldRetrieve(ModeAlways, "включи свет") {
		t.Error("mode=always must retrieve for any free-form message")
	}
	// Even in "always" mode, slash commands route deterministically and
	// carry their own arguments.
	if ShouldRetrieve(ModeAlways, "/status") {
		t.Error("mode=always must still skip slash commands")
	}
}

func TestParseRetrievalModeDefaultsToHeuristic(t *testing.T) {
	cases := map[string]RetrievalMode{
		"":          ModeHeuristic,
		"heuristic": ModeHeuristic,
		"ALWAYS":    ModeAlways,
		" off ":     ModeOff,
		"nonsense":  ModeHeuristic,
	}
	for input, want := range cases {
		if got := ParseRetrievalMode(input); got != want {
			t.Errorf("ParseRetrievalMode(%q) = %q, want %q", input, got, want)
		}
	}
}
