package files

import (
	"strings"
	"testing"
)

func TestRedactSecretsMasksCommonTokens(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"TELEGRAM=123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"OPENAI=sk-abc1234567890",
		"GITHUB=ghp_abcdefghijklmnopqrstuvwxyz123456",
		"password=supersecret",
		"token: abc123",
		"secret=mysecret",
	}, "\n")

	output := redactSecrets(input)
	for _, secret := range []string{
		"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"sk-abc1234567890",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"supersecret",
		"abc123",
		"mysecret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("expected secret %q to be redacted in %q", secret, output)
		}
	}
}
