package services

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"openlight/internal/skills"
)

var execCommandContext = exec.CommandContext

type Info struct {
	Name        string
	LoadState   string
	ActiveState string
	SubState    string
	Description string
}

type Manager interface {
	List(ctx context.Context) ([]Info, error)
	Status(ctx context.Context, service string) (Info, error)
	Restart(ctx context.Context, service string) error
	Logs(ctx context.Context, service string, lines int) (string, error)
}

type SystemdManager struct {
	allowed map[string]struct{}
	logger  *slog.Logger
}

var serviceAliases = map[string]string{
	"tailscale": "tailscaled",
}

var displayServiceAliases = map[string]string{
	"tailscaled": "tailscale",
}

func NewSystemdManager(allowed []string, logger *slog.Logger) Manager {
	set := make(map[string]struct{}, len(allowed))
	for _, service := range allowed {
		normalized := canonicalServiceName(service)
		if normalized == "" {
			continue
		}
		set[normalized] = struct{}{}
	}

	return &SystemdManager{
		allowed: set,
		logger:  logger,
	}
}

func (m *SystemdManager) List(ctx context.Context) ([]Info, error) {
	names := make([]string, 0, len(m.allowed))
	for name := range m.allowed {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]Info, 0, len(names))
	for _, name := range names {
		info, err := m.Status(ctx, name)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("failed to fetch service status", "service", name, "error", err)
			}
			results = append(results, Info{
				Name:        name,
				ActiveState: "unknown",
				Description: err.Error(),
			})
			continue
		}
		results = append(results, info)
	}

	return results, nil
}

func (m *SystemdManager) Status(ctx context.Context, service string) (Info, error) {
	service, err := m.normalizeService(service)
	if err != nil {
		return Info{}, err
	}
	if err := ensureLinux(); err != nil {
		return Info{}, err
	}

	output, err := runCombinedOutput(
		ctx,
		"systemctl",
		"show",
		service,
		"--property=Id,LoadState,ActiveState,SubState,Description",
	)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %s", skills.ErrUnavailable, strings.TrimSpace(string(output)))
	}

	info := parseSystemctlShow(string(output))
	if info.Name == "" {
		info.Name = service
	}
	return info, nil
}

func (m *SystemdManager) Restart(ctx context.Context, service string) error {
	service, err := m.normalizeService(service)
	if err != nil {
		return err
	}
	if err := ensureLinux(); err != nil {
		return err
	}

	output, err := runCombinedOutput(ctx, "systemctl", "restart", service)
	if err != nil {
		if shouldRetryWithSudo(output) {
			sudoOutput, sudoErr := runCombinedOutput(ctx, "sudo", "-n", "systemctl", "restart", service)
			if sudoErr == nil {
				return nil
			}
			output = sudoOutput
		}
		return fmt.Errorf("%w: %s", skills.ErrUnavailable, strings.TrimSpace(string(output)))
	}

	return nil
}

func (m *SystemdManager) Logs(ctx context.Context, service string, lines int) (string, error) {
	service, err := m.normalizeService(service)
	if err != nil {
		return "", err
	}
	if err := ensureLinux(); err != nil {
		return "", err
	}

	output, err := runCombinedOutput(
		ctx,
		"journalctl",
		"-u",
		service,
		"-n",
		strconv.Itoa(lines),
		"--no-pager",
		"--output=cat",
	)
	if err != nil {
		return "", fmt.Errorf("%w: %s", skills.ErrUnavailable, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

func (m *SystemdManager) normalizeService(service string) (string, error) {
	service = canonicalServiceName(service)
	if service == "" {
		return "", fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
	}
	if _, ok := m.allowed[service]; !ok {
		return "", fmt.Errorf("%w: %s", skills.ErrAccessDenied, service)
	}
	return service + ".service", nil
}

func canonicalServiceName(service string) string {
	service = strings.ToLower(strings.TrimSpace(service))
	service = strings.TrimSuffix(service, ".service")
	if alias, ok := serviceAliases[service]; ok {
		return alias
	}
	return service
}

func displayServiceName(service string) string {
	service = strings.ToLower(strings.TrimSpace(service))
	service = strings.TrimSuffix(service, ".service")
	if alias, ok := displayServiceAliases[service]; ok {
		return alias
	}
	return service
}

func parseSystemctlShow(output string) Info {
	info := Info{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "Id":
			info.Name = strings.TrimSpace(parts[1])
		case "LoadState":
			info.LoadState = strings.TrimSpace(parts[1])
		case "ActiveState":
			info.ActiveState = strings.TrimSpace(parts[1])
		case "SubState":
			info.SubState = strings.TrimSpace(parts[1])
		case "Description":
			info.Description = strings.TrimSpace(parts[1])
		}
	}
	return info
}

func ensureLinux() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%w: systemd management is supported on linux only", skills.ErrUnavailable)
	}
	return nil
}

func runCombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return execCommandContext(ctx, name, args...).CombinedOutput()
}

func shouldRetryWithSudo(output []byte) bool {
	text := strings.ToLower(strings.TrimSpace(string(output)))
	if text == "" {
		return false
	}

	for _, fragment := range []string{
		"interactive authentication required",
		"authentication is required",
		"access denied",
		"permission denied",
		"operation not permitted",
		"failed to connect to bus: permission denied",
	} {
		if strings.Contains(text, fragment) {
			return true
		}
	}

	return false
}
