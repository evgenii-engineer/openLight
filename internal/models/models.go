package models

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	SkillCallSuccess = "success"
	SkillCallFailed  = "failed"

	WatchKindServiceDown     = "service_down"
	WatchKindCPUHigh         = "cpu_high"
	WatchKindMemoryHigh      = "memory_high"
	WatchKindDiskHigh        = "disk_high"
	WatchKindTemperatureHigh = "temperature_high"

	WatchReactionNotify = "notify"
	WatchReactionAsk    = "ask"
	WatchReactionAuto   = "auto"

	WatchActionNone           = "none"
	WatchActionServiceRestart = "service_restart"

	WatchIncidentStateClear = "clear"
	WatchIncidentStateOpen  = "open"

	WatchIncidentStatusOpen     = "open"
	WatchIncidentStatusResolved = "resolved"

	WatchActionStatusNone      = "none"
	WatchActionStatusPending   = "pending"
	WatchActionStatusRunning   = "running"
	WatchActionStatusDeclined  = "declined"
	WatchActionStatusSucceeded = "succeeded"
	WatchActionStatusFailed    = "failed"
	WatchActionStatusExpired   = "expired"
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

type Watch struct {
	ID              int64
	TelegramUserID  int64
	TelegramChatID  int64
	Name            string
	Kind            string
	Target          string
	Threshold       float64
	Duration        time.Duration
	ReactionMode    string
	ActionType      string
	Cooldown        time.Duration
	Enabled         bool
	IncidentState   string
	ConditionSince  time.Time
	LastTriggeredAt time.Time
	LastCheckedAt   time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type WatchIncident struct {
	ID                int64
	WatchID           int64
	WatchName         string
	TelegramChatID    int64
	Summary           string
	Details           string
	Status            string
	ReactionMode      string
	ActionType        string
	ActionStatus      string
	ActionPrompt      string
	ActionRequestedAt time.Time
	ActionExpiresAt   time.Time
	ActionCompletedAt time.Time
	Report            string
	OpenedAt          time.Time
	ResolvedAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
