package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"openlight/internal/skills"
)

type Action string

const (
	ActionTitle      Action = "title"
	ActionText       Action = "text"
	ActionScreenshot Action = "screenshot"
	ActionCheck      Action = "check"
)

type Request struct {
	Action         Action `json:"action"`
	URL            string `json:"url"`
	ExpectedText   string `json:"expectedText,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	ScreenshotPath string `json:"screenshotPath,omitempty"`
}

type Response struct {
	OK             bool   `json:"ok"`
	Title          string `json:"title,omitempty"`
	TextPreview    string `json:"textPreview,omitempty"`
	ScreenshotPath string `json:"screenshotPath,omitempty"`
	ContainsText   bool   `json:"containsText,omitempty"`
	Error          string `json:"error,omitempty"`
}

type Manager interface {
	Enabled() bool
	Title(ctx context.Context, rawURL string) (Response, error)
	Text(ctx context.Context, rawURL string) (Response, error)
	Screenshot(ctx context.Context, rawURL string) (Response, error)
	Check(ctx context.Context, rawURL, expectedText string) (Response, error)
}

type Runner interface {
	Run(ctx context.Context, request Request) (Response, error)
}

type CommandRunner struct {
	nodePath   string
	helperPath string
}

func NewCommandRunner(nodePath, helperPath string) CommandRunner {
	return CommandRunner{
		nodePath:   strings.TrimSpace(nodePath),
		helperPath: strings.TrimSpace(helperPath),
	}
}

func (r CommandRunner) Run(ctx context.Context, request Request) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("marshal browser request: %w", err)
	}

	cmd := exec.CommandContext(ctx, r.nodePath, r.helperPath)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Response{}, skills.NewUserError(skills.ErrUnavailable, "browser helper failed")
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return Response{}, fmt.Errorf("decode browser response: %w", err)
	}
	if !response.OK {
		return Response{}, skills.NewUserError(skills.ErrUnavailable, "browser request failed")
	}

	return response, nil
}

type LocalManager struct {
	enabled             bool
	allowedDomains      []string
	allowAllDomains     bool
	allowPrivateNetwork bool
	artifactsDir        string
	timeoutSeconds      int
	runner              Runner
}

func NewLocalManager(enabled bool, allowedDomains []string, allowAllDomains, allowPrivateNetwork bool, artifactsDir string, timeoutSeconds int, runner Runner) *LocalManager {
	return &LocalManager{
		enabled:             enabled,
		allowedDomains:      normalizeDomains(allowedDomains),
		allowAllDomains:     allowAllDomains,
		allowPrivateNetwork: allowPrivateNetwork,
		artifactsDir:        strings.TrimSpace(artifactsDir),
		timeoutSeconds:      timeoutSeconds,
		runner:              runner,
	}
}

func (m *LocalManager) Enabled() bool {
	return m.enabled
}

func (m *LocalManager) Title(ctx context.Context, rawURL string) (Response, error) {
	return m.run(ctx, Request{
		Action: ActionTitle,
		URL:    rawURL,
	})
}

func (m *LocalManager) Text(ctx context.Context, rawURL string) (Response, error) {
	return m.run(ctx, Request{
		Action: ActionText,
		URL:    rawURL,
	})
}

func (m *LocalManager) Screenshot(ctx context.Context, rawURL string) (Response, error) {
	screenshotPath, err := m.prepareScreenshotPath(rawURL)
	if err != nil {
		return Response{}, err
	}
	return m.run(ctx, Request{
		Action:         ActionScreenshot,
		URL:            rawURL,
		ScreenshotPath: screenshotPath,
	})
}

func (m *LocalManager) Check(ctx context.Context, rawURL, expectedText string) (Response, error) {
	expectedText = strings.TrimSpace(expectedText)
	if expectedText == "" {
		return Response{}, fmt.Errorf("%w: expected text is required", skills.ErrInvalidArguments)
	}
	return m.run(ctx, Request{
		Action:       ActionCheck,
		URL:          rawURL,
		ExpectedText: expectedText,
	})
}

func (m *LocalManager) run(ctx context.Context, request Request) (Response, error) {
	if !m.enabled {
		return Response{}, skills.NewUserError(skills.ErrUnavailable, "browser automation is disabled")
	}
	if m.runner == nil {
		return Response{}, skills.NewUserError(skills.ErrUnavailable, "browser helper is not configured")
	}

	normalizedURL, err := m.validateURL(request.URL)
	if err != nil {
		return Response{}, err
	}
	request.URL = normalizedURL
	request.TimeoutSeconds = m.timeoutSeconds

	return m.runner.Run(ctx, request)
}

func (m *LocalManager) prepareScreenshotPath(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("%w: invalid url", skills.ErrInvalidArguments)
	}
	if strings.TrimSpace(m.artifactsDir) == "" {
		return "", skills.NewUserError(skills.ErrUnavailable, "browser artifacts directory is not configured")
	}
	if err := os.MkdirAll(m.artifactsDir, 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}

	host := sanitizeFilename(parsed.Hostname())
	if host == "" {
		host = "page"
	}
	name := fmt.Sprintf("%s-%s.png", host, time.Now().UTC().Format("20060102T150405"))
	return filepath.Join(m.artifactsDir, name), nil
}

func (m *LocalManager) validateURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed != "" && !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: invalid url", skills.ErrInvalidArguments)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: only http and https urls are supported", skills.ErrInvalidArguments)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return "", fmt.Errorf("%w: url host is required", skills.ErrInvalidArguments)
	}
	if !m.allowAllDomains && !domainAllowed(host, m.allowedDomains) {
		return "", fmt.Errorf("%w: %s", skills.ErrAccessDenied, host)
	}
	if !m.allowPrivateNetwork && isPrivateHost(host) {
		return "", fmt.Errorf("%w: private network targets are disabled", skills.ErrAccessDenied)
	}
	return parsed.String(), nil
}

func normalizeDomains(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func domainAllowed(host string, allowed []string) bool {
	for _, candidate := range allowed {
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}

func isPrivateHost(host string) bool {
	switch host {
	case "localhost":
		return true
	}
	if strings.HasSuffix(host, ".local") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func sanitizeFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}
