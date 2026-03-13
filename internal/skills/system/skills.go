package system

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
	"openlight/internal/utils"
)

type statusSkill struct {
	provider Provider
}

func NewStatusSkill(provider Provider) skills.Skill {
	return &statusSkill{provider: provider}
}

func (s *statusSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "status",
		Description: "Show a compact system overview.",
		Aliases:     []string{"system status", "overall status"},
		Usage:       "/status",
	}
}

func (s *statusSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	lines := make([]string, 0, 6)

	if hostname, err := s.provider.Hostname(ctx); err == nil {
		lines = append(lines, "Hostname: "+hostname)
	}
	if cpu, err := s.provider.CPUUsage(ctx); err == nil {
		lines = append(lines, "CPU: "+utils.FormatPercent(cpu))
	}
	if memory, err := s.provider.MemoryStats(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("Memory: %s used / %s total", utils.FormatBytes(memory.Used), utils.FormatBytes(memory.Total)))
	}
	if disk, err := s.provider.DiskStats(ctx, "/"); err == nil {
		lines = append(lines, fmt.Sprintf("Disk: %s free / %s total", utils.FormatBytes(disk.Free), utils.FormatBytes(disk.Total)))
	}
	if uptime, err := s.provider.Uptime(ctx); err == nil {
		lines = append(lines, "Uptime: "+utils.FormatDuration(uptime))
	}
	if temperature, err := s.provider.Temperature(ctx); err == nil {
		lines = append(lines, fmt.Sprintf("Temperature: %.1fC", temperature))
	}

	if len(lines) == 0 {
		return skills.Result{}, fmt.Errorf("%w: no system metrics available", skills.ErrUnavailable)
	}

	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type cpuSkill struct {
	provider Provider
}

func NewCPUSkill(provider Provider) skills.Skill {
	return &cpuSkill{provider: provider}
}

func (s *cpuSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "cpu",
		Description: "Show CPU usage.",
		Aliases:     []string{"processor usage", "show cpu"},
		Usage:       "/cpu",
	}
}

func (s *cpuSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	usage, err := s.provider.CPUUsage(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "CPU usage: " + utils.FormatPercent(usage)}, nil
}

type memorySkill struct {
	provider Provider
}

func NewMemorySkill(provider Provider) skills.Skill {
	return &memorySkill{provider: provider}
}

func (s *memorySkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory",
		Description: "Show RAM usage.",
		Aliases:     []string{"ram", "show memory usage"},
		Usage:       "/memory",
	}
}

func (s *memorySkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	stats, err := s.provider.MemoryStats(ctx)
	if err != nil {
		return skills.Result{}, err
	}

	text := fmt.Sprintf(
		"Memory usage: %s used / %s total (%s free)",
		utils.FormatBytes(stats.Used),
		utils.FormatBytes(stats.Total),
		utils.FormatBytes(stats.Available),
	)
	return skills.Result{Text: text}, nil
}

type diskSkill struct {
	provider Provider
}

func NewDiskSkill(provider Provider) skills.Skill {
	return &diskSkill{provider: provider}
}

func (s *diskSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "disk",
		Description: "Show disk usage for the root filesystem.",
		Aliases:     []string{"storage", "disk space"},
		Usage:       "/disk",
	}
}

func (s *diskSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	stats, err := s.provider.DiskStats(ctx, "/")
	if err != nil {
		return skills.Result{}, err
	}

	text := fmt.Sprintf(
		"Disk usage: %s used / %s total (%s free)",
		utils.FormatBytes(stats.Used),
		utils.FormatBytes(stats.Total),
		utils.FormatBytes(stats.Free),
	)
	return skills.Result{Text: text}, nil
}

type uptimeSkill struct {
	provider Provider
}

func NewUptimeSkill(provider Provider) skills.Skill {
	return &uptimeSkill{provider: provider}
}

func (s *uptimeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "uptime",
		Description: "Show system uptime.",
		Aliases:     []string{"running for"},
		Usage:       "/uptime",
	}
}

func (s *uptimeSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	uptime, err := s.provider.Uptime(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "Uptime: " + utils.FormatDuration(uptime)}, nil
}

type hostnameSkill struct {
	provider Provider
}

func NewHostnameSkill(provider Provider) skills.Skill {
	return &hostnameSkill{provider: provider}
}

func (s *hostnameSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "hostname",
		Description: "Show the system hostname.",
		Aliases:     []string{"host"},
		Usage:       "/hostname",
	}
}

func (s *hostnameSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	hostname, err := s.provider.Hostname(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "Hostname: " + hostname}, nil
}

type ipSkill struct {
	provider Provider
}

func NewIPSkill(provider Provider) skills.Skill {
	return &ipSkill{provider: provider}
}

func (s *ipSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "ip",
		Description: "Show local IPv4 addresses.",
		Aliases:     []string{"ip address", "network ip"},
		Usage:       "/ip",
	}
}

func (s *ipSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	addresses, err := s.provider.IPAddresses(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: "IP addresses: " + strings.Join(addresses, ", ")}, nil
}

type temperatureSkill struct {
	provider Provider
}

func NewTemperatureSkill(provider Provider) skills.Skill {
	return &temperatureSkill{provider: provider}
}

func (s *temperatureSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "temperature",
		Description: "Show system temperature when available.",
		Aliases:     []string{"temp", "cpu temp"},
		Usage:       "/temperature",
	}
}

func (s *temperatureSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	temperature, err := s.provider.Temperature(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	return skills.Result{Text: fmt.Sprintf("Temperature: %.1fC", temperature)}, nil
}
