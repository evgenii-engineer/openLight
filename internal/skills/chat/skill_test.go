package chat

import (
	"context"
	"testing"
	"time"

	"openlight/internal/llm"
	"openlight/internal/models"
	"openlight/internal/skills"
)

type stubProvider struct {
	reply    string
	messages []llm.ChatMessage
}

func (s *stubProvider) ClassifyIntent(context.Context, string, llm.ClassificationRequest) (llm.Classification, error) {
	return llm.Classification{}, nil
}

func (s *stubProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (s *stubProvider) Chat(_ context.Context, messages []llm.ChatMessage) (string, error) {
	s.messages = messages
	return s.reply, nil
}

type stubHistoryStore struct {
	messages []models.Message
}

func (s stubHistoryStore) ListMessagesByChat(context.Context, int64, int) ([]models.Message, error) {
	return s.messages, nil
}

func TestChatSkillBuildsConversation(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "hello back"}
	store := stubHistoryStore{messages: []models.Message{
		{Role: models.RoleUser, Text: "current raw"},
		{Role: models.RoleAssistant, Text: "Use /help for commands", CreatedAt: time.Now().Add(-30 * time.Second)},
		{Role: models.RoleUser, Text: "/help", CreatedAt: time.Now().Add(-45 * time.Second)},
		{Role: models.RoleAssistant, Text: "previous answer", CreatedAt: time.Now().Add(-time.Minute)},
		{Role: models.RoleUser, Text: "previous question", CreatedAt: time.Now().Add(-2 * time.Minute)},
	}}

	result, err := NewSkill(provider, store, 10).Execute(context.Background(), skills.Input{
		RawText: "current raw",
		Args:    map[string]string{"text": "normalized prompt"},
		ChatID:  1,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Text != "hello back" {
		t.Fatalf("unexpected result: %q", result.Text)
	}
	if len(provider.messages) != 4 {
		t.Fatalf("expected 4 chat messages including system prompt, got %d", len(provider.messages))
	}
	if provider.messages[1].Content != "previous question" {
		t.Fatalf("unexpected preserved history after filtering: %#v", provider.messages)
	}
	if provider.messages[2].Content != "previous answer" {
		t.Fatalf("unexpected preserved assistant history after filtering: %#v", provider.messages)
	}
	if provider.messages[len(provider.messages)-1].Content != "normalized prompt" {
		t.Fatalf("unexpected final user message: %#v", provider.messages[len(provider.messages)-1])
	}
}

func TestChatSkillRequiresText(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "unused"}
	_, err := NewSkill(provider, stubHistoryStore{}, 10).Execute(context.Background(), skills.Input{})
	if err == nil {
		t.Fatal("expected empty chat input to fail")
	}
}

func TestChatSkillTrimsLongResponses(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "Это очень длинный ответ, который нужно аккуратно сократить до более компактного варианта для слабой локальной модели."}
	skill := NewSkillWithOptions(provider, stubHistoryStore{}, Options{
		HistoryLimit:     4,
		HistoryChars:     300,
		MaxResponseChars: 48,
	})

	result, err := skill.Execute(context.Background(), skills.Input{
		RawText: "объясни память",
		ChatID:  1,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len([]rune(result.Text)) > 51 {
		t.Fatalf("expected trimmed response, got %q", result.Text)
	}
}

func TestChatSkillStripsImplicitChatPrefixAndResetsGreetingHistory(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "Привет! Всё нормально."}
	store := stubHistoryStore{messages: []models.Message{
		{Role: models.RoleAssistant, Text: "Расскажу про Raspberry Pi", CreatedAt: time.Now().Add(-time.Minute)},
		{Role: models.RoleUser, Text: "расскажи про raspberry pi", CreatedAt: time.Now().Add(-2 * time.Minute)},
	}}

	_, err := NewSkillWithOptions(provider, store, Options{
		HistoryLimit:     6,
		HistoryChars:     600,
		MaxResponseChars: 200,
	}).Execute(context.Background(), skills.Input{
		RawText: "chat привет",
		ChatID:  1,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if len(provider.messages) != 2 {
		t.Fatalf("expected only system and current greeting message, got %#v", provider.messages)
	}
	if provider.messages[1].Content != "привет" {
		t.Fatalf("expected normalized greeting without chat prefix, got %#v", provider.messages[1])
	}
}
