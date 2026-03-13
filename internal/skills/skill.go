package skills

import "context"

type Definition struct {
	Name        string
	Description string
	Aliases     []string
	Usage       string
	Examples    []string
	Hidden      bool
}

type Input struct {
	RawText string
	Args    map[string]string
	UserID  int64
	ChatID  int64
}

type Result struct {
	Text string
}

type Skill interface {
	Definition() Definition
	Execute(ctx context.Context, input Input) (Result, error)
}
