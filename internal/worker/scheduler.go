package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var RealClock Clock = realClock{}

type Job struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

type Scheduler struct {
	Hour, Minute int
	Jobs         []Job
	Clock        Clock
	Log          *slog.Logger
}

func NextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {

		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return next
}

func (s *Scheduler) Run(ctx context.Context) {
	clock := s.Clock
	if clock == nil {
		clock = RealClock
	}
	logger := s.Log
	if logger == nil {
		logger = slog.Default()
	}

	for {
		next := NextRun(clock.Now(), s.Hour, s.Minute)
		logger.Info("next background cleanup", "at", next.Format(time.RFC3339))

		select {
		case <-ctx.Done():
			return
		case <-clock.After(next.Sub(clock.Now())):
		}
		s.RunAll(ctx, logger)
	}
}

func (s *Scheduler) RunAll(ctx context.Context, logger *slog.Logger) {
	for _, job := range s.Jobs {
		if ctx.Err() != nil {
			return
		}
		runJob(ctx, logger, job)
	}
}

func runJob(ctx context.Context, logger *slog.Logger, job Job) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("job panicked", "job", job.Name, "panic", fmt.Sprint(r))
		}
	}()

	summary, err := job.Run(ctx)
	if err != nil {
		logger.Error("job failed", "job", job.Name, "err", err, "took", time.Since(start).Round(time.Millisecond))
		return
	}
	logger.Info("job done", "job", job.Name, "result", summary, "took", time.Since(start).Round(time.Millisecond))
}
