package models

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	SkillCallSuccess = "success"
	SkillCallFailed  = "failed"
)

type Message struct {
	ID             int64
	TelegramUserID int64
	TelegramChatID int64
	Role           string
	Text           string
	CreatedAt      time.Time
}

type SkillCall struct {
	ID         int64
	SkillName  string
	InputText  string
	ArgsJSON   string
	Status     string
	ErrorText  string
	DurationMS int64
	CreatedAt  time.Time
}

type Note struct {
	ID        int64
	Text      string
	CreatedAt time.Time
}

type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}
