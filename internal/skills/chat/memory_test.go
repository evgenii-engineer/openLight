package chat

import (
	"context"
	"strings"
	"testing"

	"openlight/internal/llm"
	"openlight/internal/models"
	"openlight/internal/skills"
)

type capturingProvider struct {
	messages []llm.ChatMessage
	response string
}

func (p *capturingProvider) Chat(_ context.Context, messages []llm.ChatMessage) (string, error) {
	p.messages = append([]llm.ChatMessage(nil), messages...)
	if p.response == "" {
		return "ok", nil
	}
	return p.response, nil
}

func (p *capturingProvider) ClassifyRoute(context.Context, string, llm.RouteClassificationRequest) (llm.RouteClassification, error) {
	return llm.RouteClassification{}, nil
}

func (p *capturingProvider) ClassifySkill(context.Context, string, llm.SkillClassificationRequest) (llm.Classification, error) {
	return llm.Classification{}, nil
}

func (p *capturingProvider) Summarize(context.Context, string) (string, error) { return "", nil }

type emptyHistory struct{}

func (emptyHistory) ListMessagesByChat(context.Context, int64, int) ([]models.Message, error) {
	return nil, nil
}

type stubMemory struct {
	block   string
	queries []string
}

func (m *stubMemory) MemoryPrompt(_ context.Context, _ int64, query string) string {
	m.queries = append(m.queries, query)
	return m.block
}

func TestChatInjectsMemoryAsASeparateSystemMessage(t *testing.T) {
	provider := &capturingProvider{}
	memory := &stubMemory{block: "MEMORY PREAMBLE\n\n<memory>\n- something remembered\n</memory>"}

	skill := NewSkillWithOptions(provider, emptyHistory{}, Options{Memory: memory})
	_, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 7, RawText: "какой диск подключен?", Args: map[string]string{"text": "какой диск подключен?"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(provider.messages) < 3 {
		t.Fatalf("expected system + memory + user, got %d messages", len(provider.messages))
	}
	if provider.messages[0].Role != "system" || strings.Contains(provider.messages[0].Content, "<memory>") {
		t.Fatalf("the agent's own system prompt was polluted: %+v", provider.messages[0])
	}
	// Memory gets its own system message. Merging it into the main
	// system prompt would give retrieved documents the same authority as
	// the operator's instructions.
	if provider.messages[1].Role != "system" || !strings.Contains(provider.messages[1].Content, "<memory>") {
		t.Fatalf("memory was not injected as its own system message: %+v", provider.messages[1])
	}
	last := provider.messages[len(provider.messages)-1]
	if last.Role != "user" || last.Content != "какой диск подключен?" {
		t.Fatalf("user message is wrong: %+v", last)
	}
}

func TestChatWithoutMemoryProducesTheOriginalPrompt(t *testing.T) {
	provider := &capturingProvider{}

	skill := NewSkillWithOptions(provider, emptyHistory{}, Options{})
	_, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 7, RawText: "hello", Args: map[string]string{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// With memory off the message list must be byte-identical to what it
	// was before the feature existed.
	if len(provider.messages) != 2 {
		t.Fatalf("expected exactly system + user, got %d: %+v", len(provider.messages), provider.messages)
	}
	for _, message := range provider.messages {
		if strings.Contains(message.Content, "<memory>") {
			t.Fatal("a memory block leaked into a memory-less chat")
		}
	}
}

func TestChatSkipsAnEmptyMemoryBlock(t *testing.T) {
	provider := &capturingProvider{}
	memory := &stubMemory{block: "   "}

	skill := NewSkillWithOptions(provider, emptyHistory{}, Options{Memory: memory})
	if _, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 7, RawText: "включи свет", Args: map[string]string{"text": "включи свет"},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(provider.messages) != 2 {
		t.Fatalf("a blank memory block added a message: %+v", provider.messages)
	}
	if len(memory.queries) != 1 || memory.queries[0] != "включи свет" {
		t.Fatalf("memory saw the wrong query: %+v", memory.queries)
	}
}

func TestThinkSkillAlsoReceivesMemory(t *testing.T) {
	provider := &capturingProvider{}
	memory := &stubMemory{block: "PREAMBLE\n\n<memory>\n- remembered\n</memory>"}

	skill := NewThinkSkill(provider, emptyHistory{}, Options{Memory: memory})
	if _, err := skill.Execute(context.Background(), skills.Input{
		ChatID: 3, RawText: "почему мы выбрали qdrant?", Args: map[string]string{"text": "почему мы выбрали qdrant?"},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(provider.messages) < 3 || !strings.Contains(provider.messages[1].Content, "<memory>") {
		t.Fatalf("/think did not receive memory: %+v", provider.messages)
	}
}
