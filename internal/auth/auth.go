package auth

import "fmt"

type Authorizer struct {
	allowedUsers map[int64]struct{}
	allowedChats map[int64]struct{}
}

func New(allowedUsers, allowedChats []int64) *Authorizer {
	return &Authorizer{
		allowedUsers: toSet(allowedUsers),
		allowedChats: toSet(allowedChats),
	}
}

func (a *Authorizer) Allowed(userID, chatID int64) bool {
	if len(a.allowedUsers) == 0 && len(a.allowedChats) == 0 {
		return false
	}

	if len(a.allowedUsers) > 0 {
		if _, ok := a.allowedUsers[userID]; !ok {
			return false
		}
	}

	if len(a.allowedChats) > 0 {
		if _, ok := a.allowedChats[chatID]; !ok {
			return false
		}
	}

	return true
}

func (a *Authorizer) Error(userID, chatID int64) error {
	if a.Allowed(userID, chatID) {
		return nil
	}

	return fmt.Errorf("access denied for user %d in chat %d", userID, chatID)
}

func toSet(values []int64) map[int64]struct{} {
	result := make(map[int64]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
