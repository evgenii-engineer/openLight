package skills

import "errors"

var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrNotFound         = errors.New("not found")
	ErrSkillNotFound    = errors.New("skill not found")
	ErrAccessDenied     = errors.New("access denied")
	ErrUnavailable      = errors.New("unavailable")
)

type UserFacingError interface {
	error
	UserMessage() string
}

type userFacingError struct {
	err         error
	userMessage string
}

func NewUserError(err error, userMessage string) error {
	if err == nil {
		err = ErrUnavailable
	}
	return userFacingError{
		err:         err,
		userMessage: userMessage,
	}
}

func (e userFacingError) Error() string {
	if e.err == nil {
		return e.userMessage
	}
	return e.err.Error() + ": " + e.userMessage
}

func (e userFacingError) Unwrap() error {
	return e.err
}

func (e userFacingError) UserMessage() string {
	return e.userMessage
}
