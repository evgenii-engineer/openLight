package main

import (
	"bytes"
	"strings"
	"testing"

	"openlight/internal/config"
)

func TestCheckSecurityFlagsInsecureSSH(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{
		Access: config.AccessConfig{
			Hosts: map[string]config.RemoteHostConfig{
				"pi": {
					Address:               "192.168.1.10:22",
					User:                  "pi",
					InsecureIgnoreHostKey: true,
				},
			},
		},
	}
	checkSecurity(r, cfg)

	out := buf.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "insecure_ignore_host_key") {
		t.Fatalf("expected insecure_ignore_host_key warning, got:\n%s", out)
	}
	if r.warnings == 0 {
		t.Fatal("expected warnings to increment")
	}
}

func TestCheckSecurityFlagsInlinePassword(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{
		Access: config.AccessConfig{
			Hosts: map[string]config.RemoteHostConfig{
				"vps": {
					Address:  "1.2.3.4:22",
					User:     "root",
					Password: "hunter2",
				},
			},
		},
	}
	checkSecurity(r, cfg)
	if !strings.Contains(buf.String(), "stores password inline") {
		t.Fatalf("expected inline-password warning, got:\n%s", buf.String())
	}
}

func TestCheckSecurityFlagsInlineLLMAPIKey(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{LLM: config.LLMConfig{Enabled: true, APIKey: "sk-...."}}
	checkSecurity(r, cfg)
	if !strings.Contains(buf.String(), "llm.api_key") {
		t.Fatalf("expected llm.api_key warning, got:\n%s", buf.String())
	}
}

func TestCheckSecurityFlagsShellRuntimes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{
		Workbench: config.WorkbenchConfig{
			Enabled:         true,
			AllowedRuntimes: []string{"python", "bash"},
		},
	}
	checkSecurity(r, cfg)
	out := buf.String()
	if !strings.Contains(out, "shell runtimes enabled") || !strings.Contains(out, "bash") {
		t.Fatalf("expected shell-runtime warning, got:\n%s", out)
	}
}

func TestCheckSecurityFlagsBrowserBypass(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{
		Browser: config.BrowserConfig{
			Enabled:             true,
			AllowAllDomains:     true,
			AllowPrivateNetwork: true,
		},
	}
	checkSecurity(r, cfg)
	out := buf.String()
	if !strings.Contains(out, "allow_all_domains") {
		t.Fatalf("expected allow_all_domains warning, got:\n%s", out)
	}
	if !strings.Contains(out, "allow_private_network") {
		t.Fatalf("expected allow_private_network warning, got:\n%s", out)
	}
}

func TestCheckSecurityCleanConfig(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := newDoctorReport(&buf)
	cfg := config.Config{}
	checkSecurity(r, cfg)
	if r.passes != 1 || r.warnings != 0 {
		t.Fatalf("expected single OK, got passes=%d warnings=%d output=%s", r.passes, r.warnings, buf.String())
	}
}
