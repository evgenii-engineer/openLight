package brain

import (
	"bufio"
	"bytes"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// SystemStats holds a snapshot of the host machine's resource usage.
type SystemStats struct {
	CPUPct     float64 `json:"cpu_pct"`
	MemUsedGB  float64 `json:"mem_used_gb"`
	MemTotalGB float64 `json:"mem_total_gb"`
	UptimeSec  int64   `json:"uptime_s"`
	OS         string  `json:"os"`
}

// CollectStats gathers host metrics. Best-effort: fields stay zero on failure.
func CollectStats() SystemStats {
	s := SystemStats{OS: runtime.GOOS}
	if runtime.GOOS == "darwin" {
		s.CPUPct = darwinCPU()
		darwinMem(&s)
		s.UptimeSec = darwinUptime()
	}
	return s
}

func darwinCPU() float64 {
	out, err := exec.Command("top", "-l", "2", "-n", "0", "-s", "1").Output()
	if err != nil || len(out) == 0 {
		return 0
	}
	var cpuLine string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "CPU usage:") {
			cpuLine = line
		}
	}
	for _, part := range strings.Split(cpuLine, ",") {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, "% idle") {
			idle, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(part, "% idle")), 64)
			if err == nil {
				return 100.0 - idle
			}
		}
	}
	return 0
}

func darwinMem(s *SystemStats) {
	const pageBytes = 16384.0
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return
	}
	pages := map[string]float64{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(v), "."), 64)
		if err == nil {
			pages[strings.TrimSpace(k)] = n
		}
	}
	free := (pages["Pages free"] + pages["Pages speculative"]) * pageBytes
	wired := pages["Pages wired down"] * pageBytes
	active := pages["Pages active"] * pageBytes
	inactive := pages["Pages inactive"] * pageBytes
	compressed := pages["Pages occupied by compressor"] * pageBytes

	const gb = 1 << 30
	s.MemUsedGB = (wired + active + compressed) / gb
	s.MemTotalGB = (free + wired + active + inactive + compressed) / gb
}

func darwinUptime() int64 {
	out, err := exec.Command("sysctl", "kern.boottime").Output()
	if err != nil {
		return 0
	}
	line := string(out)
	idx := strings.Index(line, "sec = ")
	if idx < 0 {
		return 0
	}
	rest := line[idx+6:]
	if end := strings.IndexAny(rest, ", }"); end > 0 {
		rest = rest[:end]
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
	if err != nil {
		return 0
	}
	return time.Now().Unix() - sec
}
