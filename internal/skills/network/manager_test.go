package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"openlight/internal/skills"
)

func TestParseTargetsAcceptsHostAndHostPort(t *testing.T) {
	t.Parallel()

	targets, err := parseTargets([]string{"example.com", "1.2.3.4:53", "Example.com"})
	if err != nil {
		t.Fatalf("parseTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 unique targets, got %d (%#v)", len(targets), targets)
	}
}

func TestParseTargetsRejectsBadPort(t *testing.T) {
	t.Parallel()
	if _, err := parseTargets([]string{"example.com:abc"}); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
	if _, err := parseTargets([]string{"example.com:99999"}); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestPortCheckBlocksDisallowedTarget(t *testing.T) {
	t.Parallel()

	m, err := NewLocalManager(true, []string{"allowed.example.com:80"}, time.Second)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	_, err = m.PortCheck(context.Background(), "blocked.example.com", 80)
	if !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got %v", err)
	}
}

func TestPortCheckBlocksDisabled(t *testing.T) {
	t.Parallel()

	m, err := NewLocalManager(false, []string{"example.com"}, time.Second)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	_, err = m.PortCheck(context.Background(), "example.com", 80)
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when disabled, got %v", err)
	}
}

func TestPortCheckHitsLocalListener(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	m, err := NewLocalManager(true, []string{net.JoinHostPort(host, portStr)}, time.Second)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	res, err := m.PortCheck(context.Background(), host, port)
	if err != nil {
		t.Fatalf("PortCheck: %v", err)
	}
	if !res.Open {
		t.Fatalf("expected open, got %#v", res)
	}
}

func TestPortCheckReportsClosed(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	listener.Close() // close before the check

	m, err := NewLocalManager(true, []string{net.JoinHostPort(host, portStr)}, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	res, err := m.PortCheck(context.Background(), host, port)
	if err != nil {
		t.Fatalf("PortCheck unexpected error: %v", err)
	}
	if res.Open {
		t.Fatalf("expected closed, got %#v", res)
	}
}

func TestHTTPCheckHitsServer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	m, err := NewLocalManager(true, []string{net.JoinHostPort(host, portStr)}, time.Second)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	res, err := m.HTTPCheck(context.Background(), srv.URL, "world")
	if err != nil {
		t.Fatalf("HTTPCheck: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if !res.BodyMatch {
		t.Fatalf("expected BodyMatch=true for 'world' substring, got %#v", res)
	}
	_ = port
}

func TestHTTPCheckBlocksUnallowedURL(t *testing.T) {
	t.Parallel()

	m, _ := NewLocalManager(true, []string{"allowed.example.com:443"}, time.Second)
	_, err := m.HTTPCheck(context.Background(), "https://blocked.example.com", "")
	if !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got %v", err)
	}
}

func TestDNSCheckBlocksDisallowedHost(t *testing.T) {
	t.Parallel()

	m, _ := NewLocalManager(true, []string{"example.com"}, time.Second)
	_, err := m.DNSCheck(context.Background(), "blocked.example.com")
	if !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got %v", err)
	}
}

func TestTargetsHostOnlyMatchesAnyPort(t *testing.T) {
	t.Parallel()

	m, err := NewLocalManager(true, []string{"raspberrypi.local"}, time.Second)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	if !m.targetAllowed("raspberrypi.local", 22) {
		t.Fatal("host-only spec should allow port 22")
	}
	if !m.targetAllowed("raspberrypi.local", 8080) {
		t.Fatal("host-only spec should allow any port")
	}
	if m.targetAllowed("evil.example.com", 22) {
		t.Fatal("unrelated host must not be allowed")
	}
}

func TestTargetsHostPortOnlyMatchesExact(t *testing.T) {
	t.Parallel()

	m, _ := NewLocalManager(true, []string{"example.com:443"}, time.Second)
	if !m.targetAllowed("example.com", 443) {
		t.Fatal("exact host:port should be allowed")
	}
	if m.targetAllowed("example.com", 80) {
		t.Fatal("other ports must not be allowed when spec is host:port")
	}
}
