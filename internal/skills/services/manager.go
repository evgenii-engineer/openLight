package services

import (
	"bytes"
	"context"
	"encoding/json"
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

type serviceBackend string

const (
	backendSystemd serviceBackend = "systemd"
	backendCompose serviceBackend = "compose"
)

type serviceTarget struct {
	Name           string
	Backend        serviceBackend
	SystemdUnit    string
	ComposeFile    string
	ComposeService string
}

type SystemdManager struct {
	services map[string]serviceTarget
	logger   *slog.Logger
}

type composePSRecord struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Health  string `json:"Health"`
	Image   string `json:"Image"`
}

var serviceAliases = map[string]string{
	"tailscale": "tailscaled",
}

var displayServiceAliases = map[string]string{
	"tailscaled": "tailscale",
}

func NewManager(allowed []string, logger *slog.Logger) (Manager, error) {
	targets, err := parseAllowedServices(allowed)
	if err != nil {
		return nil, err
	}

	return &SystemdManager{
		services: targets,
		logger:   logger,
	}, nil
}

func AllowedServiceNames(allowed []string) ([]string, error) {
	targets, err := parseAllowedServices(allowed)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (m *SystemdManager) List(ctx context.Context) ([]Info, error) {
	names := make([]string, 0, len(m.services))
	for name := range m.services {
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
	target, err := m.resolveTarget(service)
	if err != nil {
		return Info{}, err
	}
	if err := ensureLinux(); err != nil {
		return Info{}, err
	}

	switch target.Backend {
	case backendCompose:
		return m.composeStatus(ctx, target)
	default:
		return m.systemdStatus(ctx, target)
	}
}

func (m *SystemdManager) Restart(ctx context.Context, service string) error {
	target, err := m.resolveTarget(service)
	if err != nil {
		return err
	}
	if err := ensureLinux(); err != nil {
		return err
	}

	switch target.Backend {
	case backendCompose:
		return m.composeRestart(ctx, target)
	default:
		return m.systemdRestart(ctx, target)
	}
}

func (m *SystemdManager) Logs(ctx context.Context, service string, lines int) (string, error) {
	target, err := m.resolveTarget(service)
	if err != nil {
		return "", err
	}
	if err := ensureLinux(); err != nil {
		return "", err
	}

	switch target.Backend {
	case backendCompose:
		return m.composeLogs(ctx, target, lines)
	default:
		return m.systemdLogs(ctx, target, lines)
	}
}

func (m *SystemdManager) resolveTarget(service string) (serviceTarget, error) {
	name := normalizeLookupName(service)
	if name == "" {
		return serviceTarget{}, fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
	}

	target, ok := m.services[name]
	if !ok {
		return serviceTarget{}, fmt.Errorf("%w: %s", skills.ErrAccessDenied, name)
	}
	return target, nil
}

func (m *SystemdManager) normalizeService(service string) (string, error) {
	target, err := m.resolveTarget(service)
	if err != nil {
		return "", err
	}

	switch target.Backend {
	case backendCompose:
		return target.ComposeService, nil
	default:
		return target.SystemdUnit, nil
	}
}

func (m *SystemdManager) systemdStatus(ctx context.Context, target serviceTarget) (Info, error) {
	output, err := runCombinedOutput(
		ctx,
		"systemctl",
		"show",
		target.SystemdUnit,
		"--property=Id,LoadState,ActiveState,SubState,Description",
	)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %s", skills.ErrUnavailable, strings.TrimSpace(string(output)))
	}

	info := parseSystemctlShow(string(output))
	info.Name = target.Name
	return info, nil
}

func (m *SystemdManager) systemdRestart(ctx context.Context, target serviceTarget) error {
	output, err := runCombinedOutput(ctx, "systemctl", "restart", target.SystemdUnit)
	if err != nil {
		if shouldRetryWithSudo(output) {
			sudoOutput, sudoErr := runCombinedOutput(ctx, "sudo", "-n", "systemctl", "restart", target.SystemdUnit)
			if sudoErr == nil {
				return nil
			}
			output = sudoOutput
		}
		return fmt.Errorf("%w: %s", skills.ErrUnavailable, strings.TrimSpace(string(output)))
	}

	return nil
}

func (m *SystemdManager) systemdLogs(ctx context.Context, target serviceTarget, lines int) (string, error) {
	output, err := runCombinedOutput(
		ctx,
		"journalctl",
		"-u",
		target.SystemdUnit,
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

func (m *SystemdManager) composeStatus(ctx context.Context, target serviceTarget) (Info, error) {
	stdout, stderr, err := runOutput(ctx, "docker", "compose", "-f", target.ComposeFile, "ps", "--format", "json", target.ComposeService)
	if err != nil {
		return Info{}, unavailableError(stdout, stderr, err)
	}

	record, err := parseComposePS(stdout)
	if err != nil {
		message := strings.TrimSpace(string(stderr))
		if message == "" {
			message = err.Error()
		}
		return Info{}, fmt.Errorf("%w: %s", skills.ErrUnavailable, message)
	}

	return Info{
		Name:        target.Name,
		LoadState:   "compose",
		ActiveState: composeActiveState(record.State),
		SubState:    composeSubState(record),
		Description: composeDescription(target, record),
	}, nil
}

func (m *SystemdManager) composeRestart(ctx context.Context, target serviceTarget) error {
	stdout, stderr, err := runOutput(ctx, "docker", "compose", "-f", target.ComposeFile, "restart", target.ComposeService)
	if err != nil {
		return unavailableError(stdout, stderr, err)
	}
	return nil
}

func (m *SystemdManager) composeLogs(ctx context.Context, target serviceTarget, lines int) (string, error) {
	stdout, stderr, err := runOutput(ctx, "docker", "compose", "-f", target.ComposeFile, "logs", "--tail", strconv.Itoa(lines), target.ComposeService)
	if err != nil {
		return "", unavailableError(stdout, stderr, err)
	}
	return strings.TrimSpace(string(stdout)), nil
}

func parseAllowedServices(allowed []string) (map[string]serviceTarget, error) {
	targets := make(map[string]serviceTarget, len(allowed))
	for _, raw := range allowed {
		target, err := parseAllowedService(raw)
		if err != nil {
			return nil, err
		}
		if target.Name == "" {
			continue
		}
		if _, exists := targets[target.Name]; exists {
			return nil, fmt.Errorf("duplicate allowed service name: %s", target.Name)
		}
		targets[target.Name] = target
	}
	return targets, nil
}

func parseAllowedService(raw string) (serviceTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return serviceTarget{}, nil
	}

	name, spec, hasAlias := strings.Cut(raw, "=")
	if !hasAlias {
		unit := canonicalServiceName(raw)
		if unit == "" {
			return serviceTarget{}, fmt.Errorf("invalid service entry: %q", raw)
		}
		return serviceTarget{
			Name:        displayServiceName(unit),
			Backend:     backendSystemd,
			SystemdUnit: unit + ".service",
		}, nil
	}

	name = normalizeLookupName(name)
	if name == "" {
		return serviceTarget{}, fmt.Errorf("invalid service entry: %q", raw)
	}

	spec = strings.TrimSpace(spec)
	if spec == "" {
		return serviceTarget{}, fmt.Errorf("invalid service entry: %q", raw)
	}

	lowerSpec := strings.ToLower(spec)
	switch {
	case strings.HasPrefix(lowerSpec, "compose:"):
		composeFile, composeService, err := parseComposeSpec(strings.TrimSpace(spec[len("compose:"):]), name)
		if err != nil {
			return serviceTarget{}, err
		}
		return serviceTarget{
			Name:           name,
			Backend:        backendCompose,
			ComposeFile:    composeFile,
			ComposeService: composeService,
		}, nil
	case strings.HasPrefix(lowerSpec, "systemd:"):
		unit := canonicalServiceName(strings.TrimSpace(spec[len("systemd:"):]))
		if unit == "" {
			return serviceTarget{}, fmt.Errorf("invalid systemd service entry: %q", raw)
		}
		return serviceTarget{
			Name:        name,
			Backend:     backendSystemd,
			SystemdUnit: unit + ".service",
		}, nil
	default:
		unit := canonicalServiceName(spec)
		if unit == "" {
			return serviceTarget{}, fmt.Errorf("invalid systemd service entry: %q", raw)
		}
		return serviceTarget{
			Name:        name,
			Backend:     backendSystemd,
			SystemdUnit: unit + ".service",
		}, nil
	}
}

func parseComposeSpec(spec string, fallbackService string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("invalid compose service entry: empty compose spec")
	}

	lowerSpec := strings.ToLower(spec)
	switch {
	case strings.HasSuffix(lowerSpec, ".yml"):
		return spec, fallbackService, nil
	case strings.HasSuffix(lowerSpec, ".yaml"):
		return spec, fallbackService, nil
	}

	for _, marker := range []string{".yaml:", ".yml:"} {
		idx := strings.LastIndex(lowerSpec, marker)
		if idx < 0 {
			continue
		}

		file := strings.TrimSpace(spec[:idx+len(marker)-1])
		service := normalizeLookupName(spec[idx+len(marker):])
		if file == "" || service == "" {
			break
		}
		return file, service, nil
	}

	return "", "", fmt.Errorf("invalid compose service entry: %q", spec)
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

func normalizeLookupName(service string) string {
	service = strings.ToLower(strings.TrimSpace(service))
	service = strings.TrimSuffix(service, ".service")
	return displayServiceName(service)
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

func parseComposePS(output []byte) (composePSRecord, error) {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return composePSRecord{}, fmt.Errorf("compose service not found")
	}

	if strings.HasPrefix(text, "[") {
		var records []composePSRecord
		if err := json.Unmarshal([]byte(text), &records); err != nil {
			return composePSRecord{}, err
		}
		if len(records) == 0 {
			return composePSRecord{}, fmt.Errorf("compose service not found")
		}
		return records[0], nil
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var record composePSRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return composePSRecord{}, err
		}
		return record, nil
	}

	return composePSRecord{}, fmt.Errorf("compose service not found")
}

func composeActiveState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return "active"
	case "created", "dead", "exited", "paused", "removing", "restarting":
		return "inactive"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func composeSubState(record composePSRecord) string {
	if health := strings.ToLower(strings.TrimSpace(record.Health)); health != "" {
		return health
	}
	if status := strings.TrimSpace(record.Status); status != "" {
		return status
	}
	return strings.ToLower(strings.TrimSpace(record.State))
}

func composeDescription(target serviceTarget, record composePSRecord) string {
	container := strings.TrimSpace(record.Name)
	image := strings.TrimSpace(record.Image)
	switch {
	case container != "" && image != "":
		return fmt.Sprintf("docker compose service %s (%s, %s)", target.ComposeService, container, image)
	case container != "":
		return fmt.Sprintf("docker compose service %s (%s)", target.ComposeService, container)
	default:
		return "docker compose service " + target.ComposeService
	}
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

func runOutput(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	command := execCommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func unavailableError(stdout []byte, stderr []byte, err error) error {
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		message = strings.TrimSpace(string(stdout))
	}
	if message == "" && err != nil {
		message = err.Error()
	}
	return fmt.Errorf("%w: %s", skills.ErrUnavailable, message)
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
