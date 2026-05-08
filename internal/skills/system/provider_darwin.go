//go:build darwin

package system

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS lacks /proc and /sys, so the Darwin provider shells out to the
// stock command-line tools that ship with every Mac (`top`, `vm_stat`,
// `sysctl`). All commands run with a context timeout and produce
// ErrUnavailable on failure rather than crashing the agent. CGO_ENABLED=0
// stays intact: nothing here depends on cgo or third-party packages.

func (LocalProvider) CPUUsage(ctx context.Context) (float64, error) {
	// `top -l 2 -s 0 -n 0` prints two samples back-to-back with no process
	// list. The first sample is cumulative-since-boot and meaningless; the
	// second is a true delta over the (effectively zero) interval, but
	// macOS still reports a recent-window value rather than a 0-division.
	out, err := runWithContext(ctx, 2*time.Second, "top", "-l", "2", "-s", "0", "-n", "0")
	if err != nil {
		return 0, wrapUnavailable("cpu", err)
	}

	usage, ok := parseTopCPUUsage(string(out))
	if !ok {
		return 0, wrapUnavailable("cpu", fmt.Errorf("could not parse top output"))
	}
	return usage, nil
}

func (LocalProvider) MemoryStats(ctx context.Context) (MemoryStats, error) {
	totalBytes, err := readSysctlUint(ctx, "hw.memsize")
	if err != nil {
		return MemoryStats{}, wrapUnavailable("memory", err)
	}

	out, err := runWithContext(ctx, 2*time.Second, "vm_stat")
	if err != nil {
		return MemoryStats{}, wrapUnavailable("memory", err)
	}

	pageSize, available, ok := parseVMStat(string(out))
	if !ok {
		return MemoryStats{}, wrapUnavailable("memory", fmt.Errorf("could not parse vm_stat output"))
	}

	availBytes := available * pageSize
	if availBytes > totalBytes {
		availBytes = totalBytes
	}
	return MemoryStats{
		Total:     totalBytes,
		Available: availBytes,
		Used:      totalBytes - availBytes,
	}, nil
}

func (LocalProvider) Uptime(ctx context.Context) (time.Duration, error) {
	// `sysctl -n kern.boottime` prints something like:
	//   { sec = 1700000000, usec = 0 } Mon Nov 14 12:00:00 2023
	out, err := runWithContext(ctx, 2*time.Second, "sysctl", "-n", "kern.boottime")
	if err != nil {
		return 0, wrapUnavailable("uptime", err)
	}

	bootSec, ok := parseBoottimeSec(string(out))
	if !ok {
		return 0, wrapUnavailable("uptime", fmt.Errorf("could not parse boottime"))
	}

	uptime := time.Since(time.Unix(bootSec, 0))
	if uptime < 0 {
		return 0, wrapUnavailable("uptime", fmt.Errorf("negative uptime"))
	}
	return uptime, nil
}

// Temperature on macOS is not available without privileged tools
// (powermetrics requires sudo) or third-party binaries (osx-cpu-temp).
// Return ErrUnavailable with a clear message; callers already handle this
// gracefully and the status skill simply omits the line.
func (LocalProvider) Temperature(_ context.Context) (float64, error) {
	return 0, wrapUnavailable("temperature", fmt.Errorf("not exposed on macOS without root"))
}

// ---- helpers --------------------------------------------------------------

func runWithContext(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cctx, name, args...).Output()
}

func readSysctlUint(ctx context.Context, key string) (uint64, error) {
	out, err := runWithContext(ctx, 2*time.Second, "sysctl", "-n", key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
}

// parseTopCPUUsage returns the LAST "CPU usage:" line's user+sys total.
// `top -l 2` yields two such lines; the second is the meaningful one.
func parseTopCPUUsage(output string) (float64, bool) {
	var lastUser, lastSys float64
	var found bool
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		// Format: "CPU usage: 12.34% user, 5.67% sys, 81.99% idle"
		body := strings.TrimPrefix(line, "CPU usage:")
		user, sys, ok := parseUserSysFromCPULine(body)
		if !ok {
			continue
		}
		lastUser, lastSys = user, sys
		found = true
	}
	if !found {
		return 0, false
	}
	usage := lastUser + lastSys
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, true
}

func parseUserSysFromCPULine(body string) (float64, float64, bool) {
	parts := strings.Split(body, ",")
	if len(parts) < 2 {
		return 0, 0, false
	}
	user, ok := parsePercentToken(parts[0], "user")
	if !ok {
		return 0, 0, false
	}
	sys, ok := parsePercentToken(parts[1], "sys")
	if !ok {
		return 0, 0, false
	}
	return user, sys, true
}

func parsePercentToken(token, suffix string) (float64, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasSuffix(token, suffix) {
		return 0, false
	}
	value := strings.TrimSpace(strings.TrimSuffix(token, suffix))
	value = strings.TrimSuffix(value, "%")
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseVMStat returns (pageSize, availablePages, ok). "Available" mirrors
// what /proc/meminfo's MemAvailable models on Linux: pages the kernel can
// hand out without thrashing, i.e. free + inactive + speculative.
func parseVMStat(output string) (uint64, uint64, bool) {
	var pageSize uint64
	var free, inactive, speculative uint64
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			// Extract "page size of NNNN bytes"
			if idx := strings.Index(line, "page size of "); idx >= 0 {
				rest := line[idx+len("page size of "):]
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						pageSize = v
					}
				}
			}
			continue
		}
		key, value, ok := splitVMStatLine(line)
		if !ok {
			continue
		}
		switch key {
		case "Pages free":
			free = value
		case "Pages inactive":
			inactive = value
		case "Pages speculative":
			speculative = value
		}
	}
	if pageSize == 0 {
		return 0, 0, false
	}
	return pageSize, free + inactive + speculative, true
}

func splitVMStatLine(line string) (string, uint64, bool) {
	colon := strings.Index(line, ":")
	if colon <= 0 {
		return "", 0, false
	}
	key := strings.TrimSpace(line[:colon])
	rest := strings.TrimSpace(line[colon+1:])
	rest = strings.TrimSuffix(rest, ".")
	rest = strings.TrimSpace(rest)
	v, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return key, v, true
}

func parseBoottimeSec(output string) (int64, bool) {
	// Find "sec = NNN"
	idx := strings.Index(output, "sec =")
	if idx < 0 {
		return 0, false
	}
	rest := output[idx+len("sec ="):]
	rest = strings.TrimLeft(rest, " ")
	end := strings.IndexAny(rest, ", }")
	if end < 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(rest[:end]), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
