// Package network provides four small, deterministic checks that every
// homelab operator ends up writing by hand: TCP port reachability, HTTP
// status, TLS certificate expiry, and DNS resolution. The package is
// intentionally tiny — pure stdlib, allowlist-gated, no fancy retries
// or backoff. The goal is "did this thing answer or not", not a proper
// blackbox exporter.
package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"openlight/internal/skills"
)

// Target describes one allowed (host, port) pair. A zero Port means
// "any port on this host is fine"; explicit ports lock it down to a
// single endpoint.
type Target struct {
	Host string
	Port int
}

// Manager is the small, mockable surface the skills depend on. The
// real implementation is LocalManager; tests inject their own.
type Manager interface {
	Enabled() bool
	Targets() []Target
	PortCheck(ctx context.Context, host string, port int) (PortResult, error)
	HTTPCheck(ctx context.Context, rawURL, expect string) (HTTPResult, error)
	CertCheck(ctx context.Context, host string, port int) (CertResult, error)
	DNSCheck(ctx context.Context, host string) (DNSResult, error)
}

type PortResult struct {
	Host    string
	Port    int
	Open    bool
	Latency time.Duration
}

type HTTPResult struct {
	URL          string
	StatusCode   int
	Latency      time.Duration
	BodyMatch    bool
	BodySnippet  string
	ExpectedText string
}

type CertResult struct {
	Host        string
	Port        int
	Subject     string
	Issuer      string
	NotAfter    time.Time
	DaysLeft    int
	DNSNames    []string
}

type DNSResult struct {
	Host    string
	IPv4    []string
	IPv6    []string
	Latency time.Duration
}

type LocalManager struct {
	enabled bool
	targets []Target
	timeout time.Duration
	client  *http.Client
}

func NewLocalManager(enabled bool, allowed []string, timeout time.Duration) (*LocalManager, error) {
	targets, err := parseTargets(allowed)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &LocalManager{
		enabled: enabled,
		targets: targets,
		timeout: timeout,
		// Bound the HTTP client at the same horizon as the manager so a
		// stuck server can't pin a goroutine for longer than the watch
		// poll interval. We set CheckRedirect to limit redirect chains
		// (http.DefaultClient otherwise allows 10).
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}, nil
}

func (m *LocalManager) Enabled() bool { return m.enabled }

func (m *LocalManager) Targets() []Target {
	out := make([]Target, len(m.targets))
	copy(out, m.targets)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Host == out[j].Host {
			return out[i].Port < out[j].Port
		}
		return out[i].Host < out[j].Host
	})
	return out
}

func (m *LocalManager) PortCheck(ctx context.Context, host string, port int) (PortResult, error) {
	if err := m.guard(host, port); err != nil {
		return PortResult{}, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: m.timeout}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	latency := time.Since(start)
	if err != nil {
		return PortResult{Host: host, Port: port, Open: false, Latency: latency}, nil
	}
	_ = conn.Close()
	return PortResult{Host: host, Port: port, Open: true, Latency: latency}, nil
}

func (m *LocalManager) HTTPCheck(ctx context.Context, rawURL, expect string) (HTTPResult, error) {
	if !m.enabled {
		return HTTPResult{}, skills.NewUserError(skills.ErrUnavailable, "network skills are disabled")
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return HTTPResult{}, fmt.Errorf("%w: url must include http(s) scheme", skills.ErrInvalidArguments)
	}
	host := parsed.Hostname()
	port, _ := strconv.Atoi(parsed.Port())
	if port == 0 {
		if parsed.Scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	if err := m.guard(host, port); err != nil {
		return HTTPResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return HTTPResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "openlight-net-check/1")

	start := time.Now()
	resp, err := m.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return HTTPResult{URL: parsed.String(), Latency: latency}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	const snippetCap = 512
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, snippetCap))
	snippet := string(buf)

	expect = strings.TrimSpace(expect)
	matched := expect == "" || strings.Contains(snippet, expect)

	return HTTPResult{
		URL:          parsed.String(),
		StatusCode:   resp.StatusCode,
		Latency:      latency,
		BodyMatch:    matched,
		BodySnippet:  snippet,
		ExpectedText: expect,
	}, nil
}

func (m *LocalManager) CertCheck(ctx context.Context, host string, port int) (CertResult, error) {
	if port == 0 {
		port = 443
	}
	if err := m.guard(host, port); err != nil {
		return CertResult{}, err
	}

	dialer := &net.Dialer{Timeout: m.timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return CertResult{}, fmt.Errorf("%w: tls handshake: %v", skills.ErrUnavailable, err)
	}
	defer conn.Close()

	chains := conn.ConnectionState().PeerCertificates
	if len(chains) == 0 {
		return CertResult{}, fmt.Errorf("%w: server returned no certificates", skills.ErrUnavailable)
	}
	leaf := chains[0]
	daysLeft := int(time.Until(leaf.NotAfter).Hours() / 24)
	return CertResult{
		Host:     host,
		Port:     port,
		Subject:  leaf.Subject.CommonName,
		Issuer:   leaf.Issuer.CommonName,
		NotAfter: leaf.NotAfter,
		DaysLeft: daysLeft,
		DNSNames: append([]string{}, leaf.DNSNames...),
	}, nil
}

func (m *LocalManager) DNSCheck(ctx context.Context, host string) (DNSResult, error) {
	if !m.enabled {
		return DNSResult{}, skills.NewUserError(skills.ErrUnavailable, "network skills are disabled")
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if !m.hostAllowed(host) {
		return DNSResult{}, fmt.Errorf("%w: host %q is not in network.allowed", skills.ErrAccessDenied, host)
	}

	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	start := time.Now()
	addrs, err := net.DefaultResolver.LookupIPAddr(cctx, host)
	latency := time.Since(start)
	if err != nil {
		return DNSResult{Host: host, Latency: latency}, fmt.Errorf("%w: dns lookup: %v", skills.ErrUnavailable, err)
	}

	var v4, v6 []string
	for _, a := range addrs {
		if a.IP.To4() != nil {
			v4 = append(v4, a.IP.String())
		} else {
			v6 = append(v6, a.IP.String())
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return DNSResult{Host: host, IPv4: v4, IPv6: v6, Latency: latency}, nil
}

func (m *LocalManager) guard(host string, port int) error {
	if !m.enabled {
		return skills.NewUserError(skills.ErrUnavailable, "network skills are disabled")
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return fmt.Errorf("%w: host is required", skills.ErrInvalidArguments)
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", skills.ErrInvalidArguments)
	}
	if !m.targetAllowed(host, port) {
		return fmt.Errorf("%w: %s:%d is not in network.allowed", skills.ErrAccessDenied, host, port)
	}
	return nil
}

func (m *LocalManager) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, t := range m.targets {
		if t.Host == host {
			return true
		}
	}
	return false
}

func (m *LocalManager) targetAllowed(host string, port int) bool {
	for _, t := range m.targets {
		if t.Host != host {
			continue
		}
		if t.Port == 0 || t.Port == port {
			return true
		}
	}
	return false
}

func parseTargets(specs []string) ([]Target, error) {
	seen := make(map[Target]struct{}, len(specs))
	out := make([]Target, 0, len(specs))
	for _, raw := range specs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		t, err := parseTarget(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[t]; exists {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out, nil
}

func parseTarget(raw string) (Target, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return Target{}, errors.New("empty network target")
	}
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil {
		// No port specified; allow all ports on this host.
		return Target{Host: raw}, nil
	}
	port, perr := strconv.Atoi(portStr)
	if perr != nil || port <= 0 || port > 65535 {
		return Target{}, fmt.Errorf("invalid port in network target %q", raw)
	}
	return Target{Host: host, Port: port}, nil
}
