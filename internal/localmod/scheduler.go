package localmod

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler lets modules register recurring background work. Jobs run on
// goroutines bound to the loader's run context, so they stop cleanly on
// shutdown. A panicking job is recovered and logged; it never brings down the
// host or the scheduler.
type Scheduler interface {
	// Every runs job immediately-ish on the given interval. name is used for
	// logging only. interval <= 0 is ignored.
	Every(name string, interval time.Duration, job func(ctx context.Context))
	// DailyAt runs job once per day at hh:mm in loc. hhmm is "HH:MM" (24h). A
	// nil loc means time.Local. Invalid hhmm is ignored with a warning.
	DailyAt(name string, hhmm string, loc *time.Location, job func(ctx context.Context))
}

// goScheduler is the default goroutine-backed Scheduler.
type goScheduler struct {
	ctx    context.Context
	logger *slog.Logger
}

func newScheduler(ctx context.Context, logger *slog.Logger) *goScheduler {
	return &goScheduler{ctx: ctx, logger: logger}
}

func (s *goScheduler) Every(name string, interval time.Duration, job func(ctx context.Context)) {
	if job == nil || interval <= 0 {
		s.logger.Warn("scheduler: ignoring invalid interval job", "job", name, "interval", interval)
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.runSafely(name, job)
			}
		}
	}()
	s.logger.Info("scheduler: interval job registered", "job", name, "interval", interval.String())
}

func (s *goScheduler) DailyAt(name string, hhmm string, loc *time.Location, job func(ctx context.Context)) {
	if job == nil {
		return
	}
	if loc == nil {
		loc = time.Local
	}
	parsed, err := time.ParseInLocation("15:04", hhmm, loc)
	if err != nil {
		s.logger.Warn("scheduler: invalid daily time, job not scheduled", "job", name, "value", hhmm, "error", err)
		return
	}
	hour, minute := parsed.Hour(), parsed.Minute()
	go func() {
		for {
			next := nextDaily(time.Now().In(loc), hour, minute, loc)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.runSafely(name, job)
			}
		}
	}()
	s.logger.Info("scheduler: daily job registered", "job", name, "at", hhmm, "tz", loc.String())
}

// nextDaily returns the next occurrence of hour:minute strictly after now.
func nextDaily(now time.Time, hour, minute int, loc *time.Location) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

func (s *goScheduler) runSafely(name string, job func(ctx context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler: job panicked (recovered)", "job", name, "panic", r)
		}
	}()
	job(s.ctx)
}
