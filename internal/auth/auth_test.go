package auth

import "testing"

func TestAuthorizerAllowed(t *testing.T) {
	t.Parallel()

	authorizer := New([]int64{100}, []int64{200})

	if !authorizer.Allowed(100, 200) {
		t.Fatal("expected authorized user/chat pair to pass")
	}

	if authorizer.Allowed(101, 200) {
		t.Fatal("expected unknown user to be rejected")
	}

	if authorizer.Allowed(100, 201) {
		t.Fatal("expected unknown chat to be rejected")
	}
}

func TestAuthorizerRejectsWhenNoWhitelistConfigured(t *testing.T) {
	t.Parallel()

	authorizer := New(nil, nil)
	if authorizer.Allowed(1, 1) {
		t.Fatal("expected access to be denied when no whitelist is configured")
	}
}
