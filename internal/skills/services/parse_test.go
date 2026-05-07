package services

import (
	"strings"
	"testing"
)

// TestParseAllowedServicesContract pins the public contract of
// parseAllowedServices: which spec shapes are accepted and which are rejected.
// New spec shapes must be added here as a regression line so the parser does
// not silently start accepting shapes the rest of the agent cannot enforce.
func TestParseAllowedServicesContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   []string
		wantErr string
	}{
		{
			name:  "simple systemd service",
			input: []string{"tailscale"},
		},
		{
			name:  "compose service with explicit path",
			input: []string{"synapse=compose:/home/pi/matrix/docker-compose.yml"},
		},
		{
			name:  "docker container",
			input: []string{"jitsi-web=docker:docker-jitsi-meet_web_1"},
		},
		{
			name:    "empty alias on compose service is rejected",
			input:   []string{"=compose:/home/pi/docker-compose.yml"},
			wantErr: "invalid service entry",
		},
		{
			name:    "compose backend without spec is rejected",
			input:   []string{"synapse=compose:"},
			wantErr: "compose",
		},
		{
			name:    "docker backend without container is rejected",
			input:   []string{"jitsi=docker:"},
			wantErr: "docker service entry",
		},
		{
			name:    "remote host without registered host is rejected",
			input:   []string{"jitsi-web=host:vps:docker:web"},
			wantErr: "unknown",
		},
		{
			name:    "duplicate service names are rejected",
			input:   []string{"tailscale", "tailscale"},
			wantErr: "duplicate",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewManager(tc.input, nil, nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
				t.Fatalf("expected error to contain %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestLimitLogOutputBoundedByMaxChars guards the helper used by the logs
// skill. The bound matters because Telegram caps message length and large log
// dumps used to truncate mid-message in unsafe ways.
func TestLimitLogOutputBoundedByMaxChars(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a\n", 500)

	got := limitLogOutput(body, 200)
	if len([]rune(got)) > 200 {
		t.Fatalf("expected limitLogOutput to bound rune length to 200, got %d", len([]rune(got)))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker in result, got %q", got)
	}
}

func TestLimitLogOutputUnboundedWhenMaxIsZero(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("line\n", 50)
	if got := limitLogOutput(body, 0); got != body {
		t.Fatalf("expected unmodified body when max_chars=0")
	}
}
