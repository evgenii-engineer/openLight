package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"openlight/internal/config"
	"openlight/internal/skills"
)

var execCommandContext = exec.CommandContext

type Info struct {
	Name        string
	Host        string
	Backend     string
	LoadState   string
	ActiveState string
	SubState    string
	Description string
}

type Manager interface {
	Targets() []Info
	List(ctx context.Context) ([]Info, error)
	Status(ctx context.Context, service string) (Info, error)
	Restart(ctx context.Context, service string) error
	Logs(ctx context.Context, service string, lines int) (string, error)
	Exec(ctx context.Context, service string, args ...string) (string, error)
}

type serviceBackend string

const (
	backendSystemd serviceBackend = "systemd"
	backendCompose serviceBackend = "compose"
	backendDocker  serviceBackend = "docker"
)

const (
	BackendSystemd = "systemd"
	BackendCompose = "compose"
	BackendDocker  = "docker"
)

type serviceTarget struct {
	Name            string
	Host            string
	Backend         serviceBackend
	SystemdUnit     string
	ComposeFile     string
	ComposeService  string
	DockerContainer string
}

type SystemdManager struct {
	services  map[string]serviceTarget
	executors map[string]commandExecutor
	logger    *slog.Logger
}

type composePSRecord struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Health  string `json:"Health"`
	Image   string `json:"Image"`
}

type dockerInspectRecord struct {
	Name   string `json:"Name"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
	State struct {
		Status string `json:"Status"`
		Health struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

var serviceAliases = map[string]string{
	"tailscale": "tailscaled",
}

var displayServiceAliases = map[string]string{
	"tailscaled": "tailscale",
}

func NewManager(allowed []string, hosts map[string]config.RemoteHostConfig, logger *slog.Logger) (Manager, error) {
	targets, err := parseAllowedServices(allowed)
	if err != nil {
		return nil, err
	}
	if err := validateTargetHosts(targets, hosts); err != nil {
		return nil, err
	}
	executors, err := newExecutors(hosts)
	if err != nil {
		return nil, err
	}

	return &SystemdManager{
		services:  targets,
		executors: executors,
		logger:    logger,
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
	targets := m.Targets()
	results := make([]Info, 0, len(targets))
	for _, target := range targets {
		name := target.Name
		info, err := m.Status(ctx, name)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("failed to fetch service status", "service", name, "error", err)
			}
			results = append(results, Info{
				Name:        name,
				Host:        target.Host,
				Backend:     target.Backend,
				ActiveState: "unknown",
				Description: err.Error(),
			})
			continue
		}
		results = append(results, info)
	}

	return results, nil
}

func (m *SystemdManager) Targets() []Info {
	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]Info, 0, len(names))
	for _, name := range names {
		target := m.services[name]
		results = append(results, Info{
			Name:    target.Name,
			Host:    target.Host,
			Backend: string(target.Backend),
		})
	}

	return results
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
	case backendDocker:
		return m.dockerStatus(ctx, target)
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
	case backendDocker:
		return m.dockerRestart(ctx, target)
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
	case backendDocker:
		return m.dockerLogs(ctx, target, lines)
	default:
		return m.systemdLogs(ctx, target, lines)
	}
}

func (m *SystemdManager) Exec(ctx context.Context, service string, args ...string) (string, error) {
	target, err := m.resolveTarget(service)
	if err != nil {
		return "", err
	}
	if err := ensureLinux(); err != nil {
		return "", err
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("%w: command is required", skills.ErrInvalidArguments)
	}

	executor, err := m.executor(target)
	if err != nil {
		return "", err
	}

	switch target.Backend {
	case backendCompose:
		return runComposeExec(ctx, executor, target, args...)
	case backendDocker:
		return runDockerExec(ctx, executor, target, args...)
	default:
		output, execErr := executor.CombinedOutput(ctx, args[0], args[1:]...)
		if execErr != nil {
			return "", unavailableCombinedError(output, execErr)
		}
		return strings.TrimSpace(string(output)), nil
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

func (m *SystemdManager) executor(target serviceTarget) (commandExecutor, error) {
	executor, ok := m.executors[target.Host]
	if !ok {
		return nil, fmt.Errorf("%w: unknown host %q", skills.ErrUnavailable, target.Host)
	}
	return executor, nil
}

func (m *SystemdManager) normalizeService(service string) (string, error) {
	target, err := m.resolveTarget(service)
	if err != nil {
		return "", err
	}

	switch target.Backend {
	case backendCompose:
		return target.ComposeService, nil
	case backendDocker:
		return target.DockerContainer, nil
	default:
		return target.SystemdUnit, nil
	}
}

func (m *SystemdManager) systemdStatus(ctx context.Context, target serviceTarget) (Info, error) {
	executor, err := m.executor(target)
	if err != nil {
		return Info{}, err
	}

	output, err := executor.CombinedOutput(
		ctx,
		"systemctl",
		"show",
		target.SystemdUnit,
		"--property=Id,LoadState,ActiveState,SubState,Description",
	)
	if err != nil {
		return Info{}, unavailableCombinedError(output, err)
	}

	info := parseSystemctlShow(string(output))
	info.Name = target.Name
	info.Host = target.Host
	info.Backend = string(target.Backend)
	return info, nil
}

func (m *SystemdManager) systemdRestart(ctx context.Context, target serviceTarget) error {
	executor, err := m.executor(target)
	if err != nil {
		return err
	}

	output, err := executor.CombinedOutput(ctx, "systemctl", "restart", target.SystemdUnit)
	if err != nil {
		if target.Host == "" && shouldRetryWithSudo(output) {
			sudoOutput, sudoErr := runCombinedOutput(ctx, "sudo", "-n", "systemctl", "restart", target.SystemdUnit)
			if sudoErr == nil {
				return nil
			}
			output = sudoOutput
		}
		return unavailableCombinedError(output, err)
	}

	return nil
}

func (m *SystemdManager) systemdLogs(ctx context.Context, target serviceTarget, lines int) (string, error) {
	executor, err := m.executor(target)
	if err != nil {
		return "", err
	}

	output, err := executor.CombinedOutput(
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
		return "", unavailableCombinedError(output, err)
	}

	return strings.TrimSpace(string(output)), nil
}

func (m *SystemdManager) composeStatus(ctx context.Context, target serviceTarget) (Info, error) {
	executor, err := m.executor(target)
	if err != nil {
		return Info{}, err
	}

	stdout, stderr, err := runComposeCommand(ctx, executor, target, "ps", "--format", "json", target.ComposeService)
	if err != nil {
		if shouldRetryWithLegacyComposePS(stdout, stderr, err) {
			return m.composeStatusFromLegacyPS(ctx, executor, target)
		}
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
		Host:        target.Host,
		Backend:     string(target.Backend),
		LoadState:   "compose",
		ActiveState: composeActiveState(record.State),
		SubState:    composeSubState(record),
		Description: composeDescription(target, record),
	}, nil
}

func (m *SystemdManager) composeStatusFromLegacyPS(ctx context.Context, executor commandExecutor, target serviceTarget) (Info, error) {
	record, err := legacyComposePSRecord(ctx, executor, target)
	if err == nil {
		return Info{
			Name:        target.Name,
			Host:        target.Host,
			Backend:     string(target.Backend),
			LoadState:   "compose",
			ActiveState: composeActiveState(record.State),
			SubState:    composeSubState(record),
			Description: composeDescription(target, record),
		}, nil
	}

	containerID, err := legacyComposeContainerID(ctx, executor, target, false)
	if err != nil {
		return Info{}, err
	}
	if containerID == "" {
		containerID, err = legacyComposeContainerID(ctx, executor, target, true)
		if err != nil {
			return Info{}, err
		}
	}
	if containerID == "" {
		return Info{}, fmt.Errorf("%w: compose service not found", skills.ErrUnavailable)
	}

	stdout, stderr, err := executor.Output(ctx, "docker", "inspect", containerID)
	if err != nil {
		return Info{}, unavailableError(stdout, stderr, err)
	}

	record, err = parseDockerInspect(stdout)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %s", skills.ErrUnavailable, err.Error())
	}

	return Info{
		Name:        target.Name,
		Host:        target.Host,
		Backend:     string(target.Backend),
		LoadState:   "compose",
		ActiveState: composeActiveState(record.State),
		SubState:    composeSubState(record),
		Description: composeDescription(target, record),
	}, nil
}

func (m *SystemdManager) composeRestart(ctx context.Context, target serviceTarget) error {
	executor, err := m.executor(target)
	if err != nil {
		return err
	}

	stdout, stderr, err := runComposeCommand(ctx, executor, target, "restart", target.ComposeService)
	if err != nil {
		return unavailableError(stdout, stderr, err)
	}
	return nil
}

func (m *SystemdManager) composeLogs(ctx context.Context, target serviceTarget, lines int) (string, error) {
	executor, err := m.executor(target)
	if err != nil {
		return "", err
	}

	stdout, stderr, err := runComposeCommand(ctx, executor, target, "logs", "--tail", strconv.Itoa(lines), target.ComposeService)
	if err != nil {
		return "", unavailableError(stdout, stderr, err)
	}
	return strings.TrimSpace(string(stdout)), nil
}

func (m *SystemdManager) dockerStatus(ctx context.Context, target serviceTarget) (Info, error) {
	executor, err := m.executor(target)
	if err != nil {
		return Info{}, err
	}

	stdout, stderr, err := executor.Output(ctx, "docker", "inspect", target.DockerContainer)
	if err != nil {
		return Info{}, unavailableError(stdout, stderr, err)
	}

	record, err := parseDockerInspect(stdout)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %s", skills.ErrUnavailable, err.Error())
	}

	return Info{
		Name:        target.Name,
		Host:        target.Host,
		Backend:     string(target.Backend),
		LoadState:   "docker",
		ActiveState: composeActiveState(record.State),
		SubState:    composeSubState(record),
		Description: dockerDescription(target, record),
	}, nil
}

func (m *SystemdManager) dockerRestart(ctx context.Context, target serviceTarget) error {
	executor, err := m.executor(target)
	if err != nil {
		return err
	}

	output, err := executor.CombinedOutput(ctx, "docker", "restart", target.DockerContainer)
	if err != nil {
		return unavailableCombinedError(output, err)
	}
	return nil
}

func (m *SystemdManager) dockerLogs(ctx context.Context, target serviceTarget, lines int) (string, error) {
	executor, err := m.executor(target)
	if err != nil {
		return "", err
	}

	output, err := executor.CombinedOutput(ctx, "docker", "logs", "--tail", strconv.Itoa(lines), target.DockerContainer)
	if err != nil {
		return "", unavailableCombinedError(output, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func runComposeCommand(ctx context.Context, executor commandExecutor, target serviceTarget, args ...string) ([]byte, []byte, error) {
	commandArgs := make([]string, 0, len(args)+3)
	commandArgs = append(commandArgs, "compose", "-f", target.ComposeFile)
	commandArgs = append(commandArgs, args...)

	stdout, stderr, err := executor.Output(ctx, "docker", commandArgs...)
	if err == nil || !shouldRetryWithLegacyCompose(stdout, stderr, err) {
		return stdout, stderr, err
	}

	legacyArgs := make([]string, 0, len(args)+2)
	legacyArgs = append(legacyArgs, "-f", target.ComposeFile)
	legacyArgs = append(legacyArgs, args...)
	return executor.Output(ctx, "docker-compose", legacyArgs...)
}

func runComposeExec(ctx context.Context, executor commandExecutor, target serviceTarget, args ...string) (string, error) {
	commandArgs := make([]string, 0, len(args)+5)
	commandArgs = append(commandArgs, "compose", "-f", target.ComposeFile, "exec", "-T", target.ComposeService)
	commandArgs = append(commandArgs, args...)

	output, err := executor.CombinedOutput(ctx, "docker", commandArgs...)
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}
	if !shouldRetryWithLegacyCompose(output, nil, err) {
		return "", unavailableCombinedError(output, err)
	}

	legacyArgs := make([]string, 0, len(args)+4)
	legacyArgs = append(legacyArgs, "-f", target.ComposeFile, "exec", "-T", target.ComposeService)
	legacyArgs = append(legacyArgs, args...)
	legacyOutput, legacyErr := executor.CombinedOutput(ctx, "docker-compose", legacyArgs...)
	if legacyErr != nil {
		return "", unavailableCombinedError(legacyOutput, legacyErr)
	}
	return strings.TrimSpace(string(legacyOutput)), nil
}

func runDockerExec(ctx context.Context, executor commandExecutor, target serviceTarget, args ...string) (string, error) {
	commandArgs := make([]string, 0, len(args)+2)
	commandArgs = append(commandArgs, "exec", target.DockerContainer)
	commandArgs = append(commandArgs, args...)

	output, err := executor.CombinedOutput(ctx, "docker", commandArgs...)
	if err != nil {
		return "", unavailableCombinedError(output, err)
	}
	return strings.TrimSpace(string(output)), nil
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

	host, backendSpec, err := parseHostSpec(spec)
	if err != nil {
		return serviceTarget{}, err
	}

	lowerSpec := strings.ToLower(backendSpec)
	switch {
	case strings.HasPrefix(lowerSpec, "compose:"):
		composeFile, composeService, err := parseComposeSpec(strings.TrimSpace(backendSpec[len("compose:"):]), name)
		if err != nil {
			return serviceTarget{}, err
		}
		return serviceTarget{
			Name:           name,
			Host:           host,
			Backend:        backendCompose,
			ComposeFile:    composeFile,
			ComposeService: composeService,
		}, nil
	case strings.HasPrefix(lowerSpec, "docker:"):
		container := strings.TrimSpace(backendSpec[len("docker:"):])
		if container == "" {
			return serviceTarget{}, fmt.Errorf("invalid docker service entry: %q", raw)
		}
		return serviceTarget{
			Name:            name,
			Host:            host,
			Backend:         backendDocker,
			DockerContainer: container,
		}, nil
	case strings.HasPrefix(lowerSpec, "systemd:"):
		unit := canonicalServiceName(strings.TrimSpace(backendSpec[len("systemd:"):]))
		if unit == "" {
			return serviceTarget{}, fmt.Errorf("invalid systemd service entry: %q", raw)
		}
		return serviceTarget{
			Name:        name,
			Host:        host,
			Backend:     backendSystemd,
			SystemdUnit: unit + ".service",
		}, nil
	default:
		unit := canonicalServiceName(backendSpec)
		if unit == "" {
			return serviceTarget{}, fmt.Errorf("invalid systemd service entry: %q", raw)
		}
		return serviceTarget{
			Name:        name,
			Host:        host,
			Backend:     backendSystemd,
			SystemdUnit: unit + ".service",
		}, nil
	}
}

func parseHostSpec(spec string) (string, string, error) {
	lowerSpec := strings.ToLower(strings.TrimSpace(spec))
	if !strings.HasPrefix(lowerSpec, "host:") {
		return "", spec, nil
	}

	rest := strings.TrimSpace(spec[len("host:"):])
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", fmt.Errorf("invalid host service entry: %q", spec)
	}

	host := strings.ToLower(strings.TrimSpace(rest[:idx]))
	backendSpec := strings.TrimSpace(rest[idx+1:])
	if host == "" || backendSpec == "" {
		return "", "", fmt.Errorf("invalid host service entry: %q", spec)
	}
	return host, backendSpec, nil
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

func parseDockerInspect(output []byte) (composePSRecord, error) {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return composePSRecord{}, fmt.Errorf("docker inspect returned empty output")
	}

	var records []dockerInspectRecord
	if err := json.Unmarshal([]byte(text), &records); err != nil {
		return composePSRecord{}, err
	}
	if len(records) == 0 {
		return composePSRecord{}, fmt.Errorf("docker inspect returned no containers")
	}

	record := records[0]
	return composePSRecord{
		Name:   strings.TrimPrefix(strings.TrimSpace(record.Name), "/"),
		State:  strings.TrimSpace(record.State.Status),
		Status: strings.TrimSpace(record.State.Status),
		Health: strings.TrimSpace(record.State.Health.Status),
		Image:  strings.TrimSpace(record.Config.Image),
	}, nil
}

var legacyComposeColumnSplit = regexp.MustCompile(`\s{2,}`)

func legacyComposePSRecord(ctx context.Context, executor commandExecutor, target serviceTarget) (composePSRecord, error) {
	stdout, stderr, err := executor.Output(ctx, "docker-compose", "-f", target.ComposeFile, "ps", target.ComposeService)
	if err != nil {
		return composePSRecord{}, unavailableError(stdout, stderr, err)
	}

	return parseLegacyComposePS(stdout)
}

func parseLegacyComposePS(output []byte) (composePSRecord, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "name") {
			continue
		}
		if strings.Trim(line, "-") == "" {
			continue
		}

		parts := legacyComposeColumnSplit.Split(line, -1)
		if len(parts) < 3 {
			continue
		}

		status := strings.TrimSpace(parts[2])
		return composePSRecord{
			Name:   strings.TrimSpace(parts[0]),
			State:  legacyComposeState(status),
			Status: status,
		}, nil
	}

	return composePSRecord{}, fmt.Errorf("compose service not found")
}

func legacyComposeState(status string) string {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.HasPrefix(lower, "up"):
		return "running"
	case strings.HasPrefix(lower, "restart"):
		return "restarting"
	case strings.HasPrefix(lower, "exit"), strings.HasPrefix(lower, "stop"):
		return "exited"
	default:
		return lower
	}
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

func dockerDescription(target serviceTarget, record composePSRecord) string {
	container := strings.TrimSpace(record.Name)
	if container == "" {
		container = strings.TrimSpace(target.DockerContainer)
	}
	image := strings.TrimSpace(record.Image)
	switch {
	case container != "" && image != "":
		return fmt.Sprintf("docker container %s (%s)", container, image)
	case container != "":
		return "docker container " + container
	default:
		return "docker container " + target.DockerContainer
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

func unavailableCombinedError(output []byte, err error) error {
	message := strings.TrimSpace(string(output))
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

func shouldRetryWithLegacyCompose(stdout []byte, stderr []byte, err error) bool {
	text := strings.ToLower(strings.TrimSpace(string(stderr)))
	if text == "" {
		text = strings.ToLower(strings.TrimSpace(string(stdout)))
	}
	if text == "" && err != nil {
		text = strings.ToLower(strings.TrimSpace(err.Error()))
	}
	if text == "" {
		return false
	}

	for _, fragment := range []string{
		"unknown shorthand flag: 'f' in -f",
		"'compose' is not a docker command",
		"unknown docker command: \"compose\"",
		"docker: 'compose' is not a docker command",
		"docker: unknown command: compose",
		"unknown command \"compose\"",
		"executable file not found",
		"command not found",
	} {
		if strings.Contains(text, fragment) {
			return true
		}
	}

	return false
}

func shouldRetryWithLegacyComposePS(stdout []byte, stderr []byte, err error) bool {
	text := strings.ToLower(strings.TrimSpace(string(stderr)))
	if text == "" {
		text = strings.ToLower(strings.TrimSpace(string(stdout)))
	}
	if text == "" && err != nil {
		text = strings.ToLower(strings.TrimSpace(err.Error()))
	}
	if text == "" {
		return false
	}

	for _, fragment := range []string{
		"list containers.",
		"usage: ps [options]",
		"unknown flag: --format",
		"unknown option: --format",
		"no such option: --format",
	} {
		if strings.Contains(text, fragment) {
			return true
		}
	}

	return false
}

func legacyComposeContainerID(ctx context.Context, executor commandExecutor, target serviceTarget, all bool) (string, error) {
	args := []string{"-f", target.ComposeFile, "ps"}
	if all {
		args = append(args, "-a")
	}
	args = append(args, "-q", target.ComposeService)

	stdout, stderr, err := executor.Output(ctx, "docker-compose", args...)
	if err != nil {
		return "", unavailableError(stdout, stderr, err)
	}

	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
	return "", nil
}

func validateTargetHosts(targets map[string]serviceTarget, hosts map[string]config.RemoteHostConfig) error {
	for _, target := range targets {
		if target.Host == "" {
			continue
		}
		if _, ok := hosts[target.Host]; !ok {
			return fmt.Errorf("unknown host %q for service %s", target.Host, target.Name)
		}
	}
	return nil
}
