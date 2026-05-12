package runtime

import (
	"testing"
	"time"
)

func TestFormatExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"epoch zero is forever sentinel", time.Unix(0, 0), "forever"},
		{"negative unix is forever", time.Unix(-1, 0), "forever"},
		{"ollama saturated future is forever",
			time.Date(2318, 8, 22, 22, 51, 40, 0, time.UTC), "forever"},
		{"5 minutes from now", now.Add(5 * time.Minute), "in 5m"},
		{"90 minutes from now rounds to 1h 30m",
			now.Add(90 * time.Minute), "in 1h 30m"},
		{"3 hours from now", now.Add(3 * time.Hour), "in 3h"},
		{"45 seconds from now keeps second precision",
			now.Add(45 * time.Second), "in 45s"},
		{"already past is expired",
			now.Add(-1 * time.Minute), "expired"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatExpiry(tc.in, now); got != tc.want {
				t.Fatalf("formatExpiry(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestShortDurationRoundsToMinute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{1 * time.Minute, "1m"},
		{59*time.Minute + 29*time.Second, "59m"},
		{59*time.Minute + 31*time.Second, "1h"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{25 * time.Hour, "25h"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := shortDuration(tc.in); got != tc.want {
				t.Fatalf("shortDuration(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
