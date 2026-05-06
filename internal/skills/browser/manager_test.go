package browser

import (
	"context"
	"errors"
	"testing"

	"openlight/internal/skills"
)

type stubRunner struct {
	request  Request
	response Response
}

func (r *stubRunner) Run(_ context.Context, request Request) (Response, error) {
	r.request = request
	return r.response, nil
}

func TestLocalManagerRejectsDisallowedDomain(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, []string{"example.com"}, false, false, t.TempDir(), 20, &stubRunner{})
	_, err := manager.Title(context.Background(), "https://github.com")
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
}

func TestLocalManagerRejectsPrivateNetworkWhenDisabled(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, []string{"localhost"}, false, false, t.TempDir(), 20, &stubRunner{})
	_, err := manager.Title(context.Background(), "http://localhost:3000")
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected private network access denied, got %v", err)
	}
}

func TestLocalManagerRunsTitleRequest(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{
		response: Response{OK: true, Title: "Example Domain"},
	}
	manager := NewLocalManager(true, []string{"example.com"}, false, true, t.TempDir(), 20, runner)

	response, err := manager.Title(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Title returned error: %v", err)
	}
	if response.Title != "Example Domain" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if runner.request.Action != ActionTitle || runner.request.URL != "https://example.com" {
		t.Fatalf("unexpected browser request: %#v", runner.request)
	}
}

func TestLocalManagerBuildsScreenshotPath(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{
		response: Response{OK: true, Title: "Example Domain"},
	}
	manager := NewLocalManager(true, []string{"example.com"}, false, true, t.TempDir(), 20, runner)

	if _, err := manager.Screenshot(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("Screenshot returned error: %v", err)
	}
	if runner.request.ScreenshotPath == "" {
		t.Fatal("expected screenshot path to be populated")
	}
}

func TestLocalManagerAllowsAnyPublicDomainWhenConfigured(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{
		response: Response{OK: true, Title: "GitHub"},
	}
	manager := NewLocalManager(true, nil, true, false, t.TempDir(), 20, runner)

	response, err := manager.Title(context.Background(), "https://github.com")
	if err != nil {
		t.Fatalf("Title returned error: %v", err)
	}
	if response.Title != "GitHub" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if runner.request.URL != "https://github.com" {
		t.Fatalf("unexpected browser request: %#v", runner.request)
	}
}

func TestLocalManagerAllowAllDomainsStillBlocksPrivateNetwork(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, nil, true, false, t.TempDir(), 20, &stubRunner{})
	_, err := manager.Title(context.Background(), "http://localhost:3000")
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected private network access denied, got %v", err)
	}
}
