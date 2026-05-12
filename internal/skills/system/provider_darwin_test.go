//go:build darwin

package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"openlight/internal/skills"
)

func TestParseTopCPUUsageReturnsLastSample(t *testing.T) {
	t.Parallel()

	output := `Processes: 500 total, 2 running, 498 sleeping, 2900 threads
2026/05/08 12:00:00
Load Avg: 1.50, 1.25, 1.10
CPU usage: 50.00% user, 25.00% sys, 25.00% idle
SharedLibs: 200M resident
Processes: 501 total
2026/05/08 12:00:00
Load Avg: 1.50, 1.25, 1.10
CPU usage: 12.34% user, 5.66% sys, 82.00% idle
`
	usage, ok := parseTopCPUUsage(output)
	if !ok {
		t.Fatal("expected parseTopCPUUsage to succeed")
	}
	if got := round2(usage); got != 18.00 {
		t.Fatalf("expected last sample (12.34+5.66=18.00), got %.2f", got)
	}
}

func TestParseTopCPUUsageRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, ok := parseTopCPUUsage("nothing useful here\n"); ok {
		t.Fatal("expected failure on missing CPU line")
	}
}

func TestParseVMStatComputesAvailable(t *testing.T) {
	t.Parallel()

	output := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                100.
Pages active:                              500.
Pages inactive:                            200.
Pages speculative:                         50.
Pages throttled:                           0.
Pages wired down:                          1000.
`
	pageSize, available, ok := parseVMStat(output)
	if !ok {
		t.Fatal("expected parseVMStat to succeed")
	}
	if pageSize != 16384 {
		t.Fatalf("expected page size 16384, got %d", pageSize)
	}
	// free + inactive + speculative = 100 + 200 + 50 = 350
	if available != 350 {
		t.Fatalf("expected 350 available pages, got %d", available)
	}
}

func TestParseSwapUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                    string
		in                      string
		total, used, free       uint64
		ok                      bool
	}{
		{
			name:  "real macOS output",
			in:    "total = 3072.00M  used = 512.00M  free = 2560.00M  (encrypted)\n",
			total: 3072 * 1024 * 1024,
			used:  512 * 1024 * 1024,
			free:  2560 * 1024 * 1024,
			ok:    true,
		},
		{
			name:  "no swap configured",
			in:    "total = 0.00M  used = 0.00M  free = 0.00M\n",
			total: 0,
			used:  0,
			free:  0,
			ok:    true,
		},
		{
			name: "missing fields",
			in:   "total = 512.00M\n",
			ok:   false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			total, used, free, ok := parseSwapUsage(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if total != tc.total || used != tc.used || free != tc.free {
				t.Fatalf("got total=%d used=%d free=%d want total=%d used=%d free=%d",
					total, used, free, tc.total, tc.used, tc.free)
			}
		})
	}
}

func TestParseBoottimeSec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"with usec", "{ sec = 1700000000, usec = 0 } Mon Nov 14 12:00:00 2023\n", 1700000000},
		{"without trailing", "{ sec = 1700000000, usec = 0 }", 1700000000},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseBoottimeSec(tc.in)
			if !ok {
				t.Fatalf("parseBoottimeSec(%q) failed", tc.in)
			}
			if got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

// TestLocalProviderDarwinLive exercises the real macOS providers end-to-end
// using the host's /usr/sbin/sysctl, vm_stat, and top binaries. It catches
// regressions where parsing keeps working on canned strings but breaks on
// real OS output (e.g. when Apple changes wording).
func TestLocalProviderDarwinLive(t *testing.T) {
	t.Parallel()

	p := NewLocalProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if usage, err := p.CPUUsage(ctx); err != nil {
		t.Errorf("CPUUsage: %v", err)
	} else if usage < 0 || usage > 100 {
		t.Errorf("CPUUsage out of range: %.2f", usage)
	}

	mem, err := p.MemoryStats(ctx)
	if err != nil {
		t.Errorf("MemoryStats: %v", err)
	} else if mem.Total == 0 || mem.Available > mem.Total {
		t.Errorf("MemoryStats invalid: %+v", mem)
	}

	// vm.swapusage exists on every modern Mac. Live test just checks the
	// call shape — used+free should not exceed total by more than a small
	// rounding margin.
	if swap, err := p.SwapStats(ctx); err != nil {
		t.Errorf("SwapStats: %v", err)
	} else if swap.Total > 0 && swap.Used+swap.Free > swap.Total+1024*1024 {
		t.Errorf("SwapStats invariant violated: %+v", swap)
	}

	// kern.memorystatus_vm_pressure_level should always be set; the value
	// may legitimately be green/yellow/red. We only require it to parse.
	if level, err := p.MemoryPressure(ctx); err != nil {
		t.Errorf("MemoryPressure: %v", err)
	} else {
		switch level {
		case PressureGreen, PressureYellow, PressureRed:
		default:
			t.Errorf("unexpected memory pressure level %q", level)
		}
	}

	if up, err := p.Uptime(ctx); err != nil {
		t.Errorf("Uptime: %v", err)
	} else if up <= 0 {
		t.Errorf("Uptime non-positive: %v", up)
	}

	if _, err := p.Temperature(ctx); !errors.Is(err, skills.ErrUnavailable) {
		t.Errorf("Temperature: expected ErrUnavailable on macOS, got %v", err)
	}
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
