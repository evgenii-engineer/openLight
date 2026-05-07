package skills

import (
	"context"

	"openlight/internal/telegram"
)

type Definition struct {
	Name        string
	Group       Group
	Description string
	Aliases     []string
	Usage       string
	Examples    []string
	Mutating    bool
	Hidden      bool
}

type Input struct {
	RawText string
	Args    map[string]string
	UserID  int64
	ChatID  int64
	Source  string
}

// AttachmentKind enumerates the supported transport-level attachment shapes.
// "photo" gets rendered inline by clients that support it (Telegram squashes
// non-image files); "document" is used for arbitrary binaries we still want to
// deliver alongside the text.
type AttachmentKind string

const (
	AttachmentPhoto    AttachmentKind = "photo"
	AttachmentDocument AttachmentKind = "document"
)

// Attachment is a file the skill wants delivered alongside its text reply.
// The path must point at a file readable by the agent process; the caller is
// responsible for not deleting it before delivery completes.
type Attachment struct {
	Path    string
	Caption string
	Kind    AttachmentKind
}

type Result struct {
	Text        string
	Buttons     [][]telegram.Button
	Attachments []Attachment
}

type Skill interface {
	Definition() Definition
	Execute(ctx context.Context, input Input) (Result, error)
}

type UIDescriptor struct {
	Inputs    []InputField
	FollowUps []FollowUp
	Confirm   string
}

type InputField struct {
	Name        string
	Prompt      string
	Placeholder string
	Validate    func(string) error
}

type FollowUp struct {
	Label  string
	Action string
	Target string
}

type UIHinted interface {
	UI() UIDescriptor
}

func DescribeUI(skill Skill) UIDescriptor {
	if skill == nil {
		return UIDescriptor{}
	}
	if hinted, ok := skill.(UIHinted); ok {
		return hinted.UI()
	}
	return UIDescriptor{}
}
