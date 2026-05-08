package browser

import (
	"context"
	"errors"
	"net"
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

type stubResolver struct {
	ips map[string][]net.IP
}

func (s *stubResolver) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if ips, ok := s.ips[host]; ok {
		return ips, nil
	}
	return []net.IP{net.ParseIP("93.184.216.34")}, nil // example.com-ish
}

func TestLocalManagerRejectsDisallowedDomain(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, []string{"example.com"}, false, false, t.TempDir(), 20, &stubRunner{})
	_, err := manager.Title(context.Background(), "https://github.com")
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
}

func TestLocalManagerRejectsHostWithoutDot(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, []string{"example.com"}, true, false, t.TempDir(), 20, &stubRunner{})
	_, err := manager.Title(context.Background(), "https://сайта")
	if err == nil || !errors.Is(err, skills.ErrInvalidArguments) {
		t.Fatalf("expected invalid arguments for non-domain host, got %v", err)
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
	manager.SetResolver(&stubResolver{ips: map[string][]net.IP{
		"github.com": {net.ParseIP("140.82.121.3")},
	}})

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

func TestLocalManagerBlocksHostnameResolvingToLoopback(t *testing.T) {
	t.Parallel()

	manager := NewLocalManager(true, nil, true, false, t.TempDir(), 20, &stubRunner{})
	manager.SetResolver(&stubResolver{ips: map[string][]net.IP{
		"evil.example.com": {net.ParseIP("127.0.0.1")},
	}})

	_, err := manager.Title(context.Background(), "https://evil.example.com")
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected DNS-rebind SSRF to be blocked, got %v", err)
	}
}

func TestLocalManagerBlocksHostnameResolvingToPrivateRange(t *testing.T) {
	t.Parallel()

	cases := []net.IP{
		net.ParseIP("10.0.0.5"),
		net.ParseIP("172.16.0.1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("169.254.1.1"), // link-local
		net.ParseIP("fc00::1"),     // ULA
		net.ParseIP("::1"),         // loopback v6
	}
	for _, ip := range cases {
		ip := ip
		t.Run(ip.String(), func(t *testing.T) {
			t.Parallel()
			manager := NewLocalManager(true, nil, true, false, t.TempDir(), 20, &stubRunner{})
			manager.SetResolver(&stubResolver{ips: map[string][]net.IP{
				"evil.example.com": {ip},
			}})
			_, err := manager.Title(context.Background(), "https://evil.example.com")
			if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
				t.Fatalf("expected access denied for %s, got %v", ip, err)
			}
		})
	}
}

func TestLocalManagerAllowsPublicResolvedIP(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{response: Response{OK: true, Title: "ok"}}
	manager := NewLocalManager(true, nil, true, false, t.TempDir(), 20, runner)
	manager.SetResolver(&stubResolver{ips: map[string][]net.IP{
		"example.com": {net.ParseIP("93.184.216.34")},
	}})
	if _, err := manager.Title(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("expected public IP to pass, got %v", err)
	}
}

func TestLocalManagerSkipsResolutionWhenPrivateNetworkAllowed(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{response: Response{OK: true, Title: "ok"}}
	manager := NewLocalManager(true, nil, true, true /* allow private */, t.TempDir(), 20, runner)
	// Resolver that would error if called - confirms we skip lookup.
	manager.SetResolver(&stubResolver{ips: map[string][]net.IP{
		"intranet.local": {net.ParseIP("10.0.0.1")},
	}})
	if _, err := manager.Title(context.Background(), "http://intranet.local"); err != nil {
		t.Fatalf("expected allow_private_network=true to permit private host, got %v", err)
	}
}
