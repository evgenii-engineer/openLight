package services

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"openlight/internal/skills"
)

func TestValidateBackendOnHostAllowsDockerEverywhere(t *testing.T) {
	t.Parallel()

	for _, backend := range []serviceBackend{backendDocker, backendCompose} {
		if err := validateBackendOnHost(serviceTarget{Backend: backend}); err != nil {
			t.Fatalf("backend %q on %s: expected nil, got %v", backend, runtime.GOOS, err)
		}
	}
}

func TestValidateBackendOnHostAllowsRemoteSystemd(t *testing.T) {
	t.Parallel()

	target := serviceTarget{Backend: backendSystemd, Host: "pi"}
	if err := validateBackendOnHost(target); err != nil {
		t.Fatalf("remote systemd: expected nil, got %v", err)
	}
}

func TestValidateBackendOnHostBlocksLocalSystemdOnNonLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		t.Skip("only meaningful on non-Linux hosts")
	}
	err := validateBackendOnHost(serviceTarget{Backend: backendSystemd})
	if err == nil {
		t.Fatal("expected ErrUnavailable for local systemd on non-Linux")
	}
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "docker/compose") {
		t.Fatalf("error message should suggest docker/compose, got %q", err.Error())
	}
}

func TestValidateBackendOnHostAllowsLocalSystemdOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("only meaningful on Linux hosts")
	}
	if err := validateBackendOnHost(serviceTarget{Backend: backendSystemd}); err != nil {
		t.Fatalf("local systemd on linux: expected nil, got %v", err)
	}
}
