//go:build linux

package system

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func (LocalProvider) CPUUsage(ctx context.Context) (float64, error) {
	start, err := readCPUSample()
	if err != nil {
		return 0, wrapUnavailable("cpu", err)
	}

	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
	}

	end, err := readCPUSample()
	if err != nil {
		return 0, wrapUnavailable("cpu", err)
	}

	totalDiff := end.total - start.total
	idleDiff := end.idle - start.idle
	if totalDiff <= 0 {
		return 0, wrapUnavailable("cpu", fmt.Errorf("invalid cpu sample"))
	}

	usage := (float64(totalDiff-idleDiff) / float64(totalDiff)) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}

	return usage, nil
}

func (LocalProvider) MemoryStats(_ context.Context) (MemoryStats, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryStats{}, wrapUnavailable("memory", err)
	}
	defer file.Close()

	var total uint64
	var available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return MemoryStats{}, wrapUnavailable("memory", err)
	}
	if total == 0 {
		return MemoryStats{}, wrapUnavailable("memory", fmt.Errorf("MemTotal not found"))
	}

	used := total - available
	return MemoryStats{
		Total:     total,
		Available: available,
		Used:      used,
	}, nil
}

func (LocalProvider) SwapStats(_ context.Context) (SwapStats, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return SwapStats{}, wrapUnavailable("swap", err)
	}
	defer file.Close()

	var total, free uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "SwapTotal:":
			total = value * 1024
		case "SwapFree:":
			free = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return SwapStats{}, wrapUnavailable("swap", err)
	}
	var used uint64
	if total >= free {
		used = total - free
	}
	return SwapStats{Total: total, Used: used, Free: free}, nil
}

func (LocalProvider) MemoryPressure(_ context.Context) (string, error) {
	// /proc/pressure/memory (PSI) is available on Linux 4.20+. The file
	// contains two lines: "some" (any task stalled) and "full" (all tasks
	// stalled). We use the avg10 value from the "some" line as the proxy.
	content, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return "", wrapUnavailable("memory_pressure", err)
	}
	avg10, ok := parsePSIAvg10(string(content))
	if !ok {
		return "", wrapUnavailable("memory_pressure", fmt.Errorf("could not parse PSI"))
	}
	switch {
	case avg10 < 1.0:
		return PressureGreen, nil
	case avg10 < 10.0:
		return PressureYellow, nil
	default:
		return PressureRed, nil
	}
}

func parsePSIAvg10(content string) (float64, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "avg10=") {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimPrefix(field, "avg10="), 64)
			if err != nil {
				return 0, false
			}
			return value, true
		}
	}
	return 0, false
}

func (LocalProvider) Uptime(_ context.Context) (time.Duration, error) {
	content, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, wrapUnavailable("uptime", err)
	}

	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0, wrapUnavailable("uptime", fmt.Errorf("missing uptime value"))
	}

	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, wrapUnavailable("uptime", err)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func (LocalProvider) Temperature(_ context.Context) (float64, error) {
	content, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, wrapUnavailable("temperature", err)
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
	if err != nil {
		return 0, wrapUnavailable("temperature", err)
	}

	return value / 1000, nil
}

type cpuSample struct {
	total uint64
	idle  uint64
}

func readCPUSample() (cpuSample, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return cpuSample{}, err
		}
		return cpuSample{}, fmt.Errorf("missing cpu line")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("invalid cpu line")
	}

	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, err
		}
		values = append(values, value)
	}

	var total uint64
	for _, value := range values {
		total += value
	}

	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}

	return cpuSample{total: total, idle: idle}, nil
}
