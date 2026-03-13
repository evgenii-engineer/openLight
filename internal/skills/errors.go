package skills

import "errors"

var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrNotFound         = errors.New("not found")
	ErrSkillNotFound    = errors.New("skill not found")
	ErrAccessDenied     = errors.New("access denied")
	ErrUnavailable      = errors.New("unavailable")
)
