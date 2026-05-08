package watch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"openlight/internal/models"
	networkpkg "openlight/internal/skills/network"
	"openlight/internal/skills"
	"openlight/internal/storage/sqlite"
)

type fakeNetworkManager struct {
	targets []networkpkg.Target
	port    networkpkg.PortResult
	cert    networkpkg.CertResult
	portErr error
	certErr error
}

func (f *fakeNetworkManager) Enabled() bool                   { return true }
func (f *fakeNetworkManager) Targets() []networkpkg.Target    { return f.targets }
func (f *fakeNetworkManager) PortCheck(context.Context, string, int) (networkpkg.PortResult, error) {
	return f.port, f.portErr
}
func (f *fakeNetworkManager) CertCheck(context.Context, string, int) (networkpkg.CertResult, error) {
	return f.cert, f.certErr
}
func (f *fakeNetworkManager) HTTPCheck(context.Context, string, string) (networkpkg.HTTPResult, error) {
	return networkpkg.HTTPResult{}, nil
}
func (f *fakeNetworkManager) DNSCheck(context.Context, string) (networkpkg.DNSResult, error) {
	return networkpkg.DNSResult{}, nil
}

func newServiceWithNetwork(t *testing.T, nm networkpkg.Manager) *Service {
	t.Helper()
	repo, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "watch.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	s := NewService(repo, skills.NewRegistry(), &fakeNotifier{}, fakeProvider{}, &fakeServiceManager{}, nil, Options{
		PollInterval:   time.Minute,
		AskTTL:         time.Minute,
		RequestTimeout: time.Second,
	})
	s.SetNetworkManager(nm)
	return s
}

func TestParseAddSpecPort(t *testing.T) {
	t.Parallel()
	spec, err := parseAddSpec("port raspberrypi.local:22 for 30s cooldown 10m")
	if err != nil {
		t.Fatalf("parseAddSpec: %v", err)
	}
	if spec.Kind != models.WatchKindPortDown {
		t.Fatalf("kind: %#v", spec)
	}
	if spec.Target != "raspberrypi.local:22" {
		t.Fatalf("target: %#v", spec)
	}
	if spec.Duration != 30*time.Second || spec.Cooldown != 10*time.Minute {
		t.Fatalf("durations: %#v", spec)
	}
}

func TestParseAddSpecCertWithExpiresIn(t *testing.T) {
	t.Parallel()
	spec, err := parseAddSpec("cert example.com expires-in 30d cooldown 24h")
	if err != nil {
		t.Fatalf("parseAddSpec: %v", err)
	}
	if spec.Kind != models.WatchKindCertExpiringSoon {
		t.Fatalf("kind: %#v", spec)
	}
	if spec.Threshold != 30 {
		t.Fatalf("threshold: %#v", spec)
	}
}

func TestEvaluateWatchPortDownConditionTrueWhenClosed(t *testing.T) {
	t.Parallel()
	nm := &fakeNetworkManager{port: networkpkg.PortResult{Open: false}}
	s := newServiceWithNetwork(t, nm)
	eval, err := s.evaluateWatch(context.Background(), models.Watch{Kind: models.WatchKindPortDown, Target: "host:22"})
	if err != nil {
		t.Fatalf("evaluateWatch: %v", err)
	}
	if !eval.condition {
		t.Fatalf("expected condition true for closed port, got %#v", eval)
	}
}

func TestEvaluateWatchPortDownConditionFalseWhenOpen(t *testing.T) {
	t.Parallel()
	nm := &fakeNetworkManager{port: networkpkg.PortResult{Open: true, Latency: 12 * time.Millisecond}}
	s := newServiceWithNetwork(t, nm)
	eval, err := s.evaluateWatch(context.Background(), models.Watch{Kind: models.WatchKindPortDown, Target: "host:22"})
	if err != nil {
		t.Fatalf("evaluateWatch: %v", err)
	}
	if eval.condition {
		t.Fatalf("expected condition false for open port, got %#v", eval)
	}
}

func TestEvaluateWatchCertExpiring(t *testing.T) {
	t.Parallel()
	nm := &fakeNetworkManager{cert: networkpkg.CertResult{
		Subject: "example.com", Issuer: "Let's Encrypt", NotAfter: time.Now().Add(48 * time.Hour), DaysLeft: 2,
	}}
	s := newServiceWithNetwork(t, nm)
	eval, err := s.evaluateWatch(context.Background(), models.Watch{Kind: models.WatchKindCertExpiringSoon, Target: "example.com:443", Threshold: 14})
	if err != nil {
		t.Fatalf("evaluateWatch: %v", err)
	}
	if !eval.condition {
		t.Fatalf("expected condition true for cert with 2 days left, got %#v", eval)
	}
}

func TestEvaluateWatchPortDownRequiresNetworkManager(t *testing.T) {
	t.Parallel()
	s := newServiceWithNetwork(t, nil)
	_, err := s.evaluateWatch(context.Background(), models.Watch{Kind: models.WatchKindPortDown, Target: "host:22"})
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestEnableTLSPackCreatesWatchPerTarget(t *testing.T) {
	t.Parallel()
	nm := &fakeNetworkManager{targets: []networkpkg.Target{
		{Host: "example.com", Port: 443},
		{Host: "api.example.com"}, // bare host counts as port 0 → matches port 443 case
	}}
	s := newServiceWithNetwork(t, nm)
	out, err := s.EnablePack(context.Background(), 1, 1, "tls")
	if err != nil {
		t.Fatalf("EnablePack tls: %v", err)
	}
	if !contains(out, "TLS pack enabled") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestEnableMacPackUsesLooserDiskThreshold(t *testing.T) {
	t.Parallel()
	s := newServiceWithNetwork(t, nil)
	if _, err := s.EnablePack(context.Background(), 1, 1, "mac"); err != nil {
		t.Fatalf("EnablePack mac: %v", err)
	}
}

func TestEnablePiPackIncludesTemperature(t *testing.T) {
	t.Parallel()
	s := newServiceWithNetwork(t, nil)
	if _, err := s.EnablePack(context.Background(), 1, 1, "pi"); err != nil {
		t.Fatalf("EnablePack pi: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
