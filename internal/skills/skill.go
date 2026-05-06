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

type Result struct {
	Text    string
	Buttons [][]telegram.Button
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
