package models

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	SkillCallSuccess = "success"
	SkillCallFailed  = "failed"

	WatchKindServiceDown      = "service_down"
	WatchKindCPUHigh          = "cpu_high"
	WatchKindMemoryHigh       = "memory_high"
	WatchKindDiskHigh         = "disk_high"
	WatchKindTemperatureHigh  = "temperature_high"
	WatchKindPortDown         = "port_down"
	WatchKindCertExpiringSoon = "cert_expiring"

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

type Memory struct {
	ID        int64
	Text      string
	Kind      string
	Tags      []string
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
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

// VisualWatch is a periodic browser-screenshot diff target. It belongs to a
// single chat and runs on its own polling cadence, independent from the
// service/metric watch engine.
type VisualWatch struct {
	ID                  int64
	TelegramUserID      int64
	TelegramChatID      int64
	Name                string
	URL                 string
	Keywords            []string
	NotifyOnChange      bool
	NotifyOnKeywords    bool
	DiffThreshold       float64
	Interval            time.Duration
	Cooldown            time.Duration
	BaselinePath        string
	LastScreenshotPath  string
	LastChangedFraction float64
	LastKeywordsSeen    []string
	LastCheckedAt       time.Time
	LastChangedAt       time.Time
	LastAlertedAt       time.Time
	Enabled             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
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
