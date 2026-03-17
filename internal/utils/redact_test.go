package utils

import "testing"

func TestRedactSensitiveTextRedactsUserAddPassword(t *testing.T) {
	t.Parallel()

	got := RedactSensitiveText("/user_add jitsi anya 123456")
	if want := "/user_add jitsi anya [redacted]"; got != want {
		t.Fatalf("unexpected redacted text: %q", got)
	}

	got = RedactSensitiveText("user add anya 123456")
	if want := "user add anya [redacted]"; got != want {
		t.Fatalf("unexpected implicit-provider text: %q", got)
	}
}

func TestRedactSensitiveArgsRedactsPasswordKeys(t *testing.T) {
	t.Parallel()

	got := RedactSensitiveArgs(map[string]string{
		"provider": "jitsi",
		"username": "anya",
		"password": "123456",
	})
	if got["password"] != "[redacted]" {
		t.Fatalf("expected redacted password, got %#v", got)
	}
	if got["provider"] != "jitsi" || got["username"] != "anya" {
		t.Fatalf("unexpected non-secret args: %#v", got)
	}
}
