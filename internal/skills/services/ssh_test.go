package services

import (
	"os"
	"testing"
)

func TestBuildRemoteCommandQuotesArguments(t *testing.T) {
	t.Parallel()

	command := buildRemoteCommand(true, "docker", "compose", "-f", "/opt/jitsi/docker-compose.yml", "logs", "--tail", "50", "web")
	if want := "sudo -n docker compose -f /opt/jitsi/docker-compose.yml logs --tail 50 web"; command != want {
		t.Fatalf("unexpected command: %q", command)
	}

	command = buildRemoteCommand(false, "systemctl", "show", "my service")
	if want := "systemctl show 'my service'"; command != want {
		t.Fatalf("unexpected quoted command: %q", command)
	}
}

func TestResolveSecretPrefersInlineValue(t *testing.T) {
	t.Setenv("OPENLIGHT_TEST_SECRET", "from-env")
	if got := resolveSecret("inline", "OPENLIGHT_TEST_SECRET"); got != "inline" {
		t.Fatalf("unexpected secret value: %q", got)
	}
}

func TestResolveSecretUsesEnvironment(t *testing.T) {
	t.Setenv("OPENLIGHT_TEST_SECRET", "from-env")
	if got := resolveSecret("", "OPENLIGHT_TEST_SECRET"); got != "from-env" {
		t.Fatalf("unexpected env secret value: %q", got)
	}
}

func TestResolveSecretReturnsEmptyWhenUnset(t *testing.T) {
	_ = os.Unsetenv("OPENLIGHT_TEST_SECRET_EMPTY")
	if got := resolveSecret("", "OPENLIGHT_TEST_SECRET_EMPTY"); got != "" {
		t.Fatalf("expected empty secret, got %q", got)
	}
}
