package auth

import "testing"

// TestAuthorizerOnlyUsersAllowlistIgnoresChat documents the contract: when the
// chat allowlist is empty, only the user_id is checked. Without this, a user
// who is allowed but happens to be in a brand new chat would be rejected,
// which would lock the bot out of fresh group/private chats.
func TestAuthorizerOnlyUsersAllowlistIgnoresChat(t *testing.T) {
	t.Parallel()

	authorizer := New([]int64{100}, nil)
	if !authorizer.Allowed(100, 999) {
		t.Fatalf("expected allowed user to pass regardless of chat when chat allowlist is empty")
	}
	if authorizer.Allowed(101, 999) {
		t.Fatalf("expected unallowed user to be rejected")
	}
}

// TestAuthorizerOnlyChatsAllowlistIgnoresUser is the symmetric case: when only
// the chat allowlist is set, any user inside an allowed chat is permitted.
// This is how shared group bots are typically configured.
func TestAuthorizerOnlyChatsAllowlistIgnoresUser(t *testing.T) {
	t.Parallel()

	authorizer := New(nil, []int64{200})
	if !authorizer.Allowed(1, 200) {
		t.Fatalf("expected user inside allowed chat to pass when user allowlist is empty")
	}
	if authorizer.Allowed(1, 201) {
		t.Fatalf("expected user outside allowed chat to be rejected")
	}
}

// TestAuthorizerErrorMessageMentionsBothIds protects telemetry/logging: the
// error string must include the offending user_id and chat_id so operators
// can tell who tried to access the bot.
func TestAuthorizerErrorMessageMentionsBothIds(t *testing.T) {
	t.Parallel()

	authorizer := New([]int64{1}, []int64{2})
	err := authorizer.Error(99, 88)
	if err == nil {
		t.Fatalf("expected denied access to return error")
	}
	msg := err.Error()
	if !contains(msg, "99") || !contains(msg, "88") {
		t.Fatalf("expected error to mention both ids, got %q", msg)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
