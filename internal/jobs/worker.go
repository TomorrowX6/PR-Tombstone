package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pr-tombstone/internal/ingest"
	"pr-tombstone/internal/repository"
)

type Worker struct {
	Store            *repository.Store
	Service          *ingest.Service
	PollInterval     time.Duration
	Logger           *slog.Logger
	RetentionDays    int
	lastHousekeeping time.Time
	lastPayloadSweep time.Time
}

type BatchResult struct {
	Claimed           int   `json:"claimed"`
	Completed         int   `json:"completed"`
	Failed            int   `json:"failed"`
	Recovered         int64 `json:"recovered"`
	MaintenanceErrors int   `json:"maintenance_errors"`
	TimedOut          bool  `json:"timed_out"`
}

func (w *Worker) Run(ctx context.Context) error {
	if w.PollInterval <= 0 {
		w.PollInterval = 2 * time.Second
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	recovered, err := w.Store.RecoverStaleJobs(ctx, 15*time.Minute)
	if err != nil {
		return err
	}
	if recovered > 0 {
		w.Logger.Warn("recovered stale analysis jobs", "count", recovered)
	}
	for {
		if _, err := w.runOne(ctx); err != nil {
			w.Logger.Error("analysis job failed", "error", err)
		}
		if time.Since(w.lastPayloadSweep) > time.Hour {
			if err := w.Store.DeleteExpiredPayloads(ctx); err != nil {
				w.Logger.Warn("payload retention cleanup failed", "error", err)
			}
			w.lastPayloadSweep = time.Now()
		}
		if time.Since(w.lastHousekeeping) > time.Hour {
			if err := w.Store.PruneExpiredData(ctx, w.RetentionDays); err != nil {
				w.Logger.Warn("retention cleanup failed", "error", err)
			}
			if err := w.Store.PruneOperationalData(ctx); err != nil {
				w.Logger.Warn("operational cleanup failed", "error", err)
			}
			w.lastHousekeeping = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.PollInterval):
		}
	}
}

// RunBatch processes a bounded amount of queue work. It is used by request-
// driven runtimes such as Vercel where an infinite polling loop is not valid.
// Individual analysis failures are persisted and counted without aborting the
// remaining batch; infrastructure failures are returned to the caller.
func (w *Worker) RunBatch(ctx context.Context, maxJobs int, budget time.Duration) (BatchResult, error) {
	var result BatchResult
	if w.Store == nil || w.Service == nil {
		return result, errors.New("worker store and service are required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if maxJobs < 1 {
		maxJobs = 1
	}
	if budget <= 0 {
		budget = 50 * time.Second
	}
	batchCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	recovered, err := w.Store.RecoverStaleJobs(batchCtx, 15*time.Minute)
	if err != nil {
		return result, err
	}
	result.Recovered = recovered
	result.MaintenanceErrors = w.runServerlessHousekeeping(batchCtx)

	for result.Claimed < maxJobs {
		if err := batchCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				result.TimedOut = true
				return result, nil
			}
			return result, err
		}
		processed, err := w.runOne(batchCtx)
		if !processed {
			if err != nil {
				return result, err
			}
			break
		}
		result.Claimed++
		if err != nil {
			result.Failed++
			w.Logger.Error("analysis job failed", "error", err)
			continue
		}
		result.Completed++
	}
	return result, nil
}

func (w *Worker) runServerlessHousekeeping(ctx context.Context) int {
	due, err := w.Store.ClaimMaintenance(ctx, "worker-housekeeping", time.Hour)
	if err != nil {
		w.Logger.Warn("claim maintenance lease failed", "error", err)
		return 1
	}
	if !due {
		return 0
	}
	errorsCount := 0
	for name, cleanup := range map[string]func(context.Context) error{
		"payload retention":   w.Store.DeleteExpiredPayloads,
		"data retention":      func(ctx context.Context) error { return w.Store.PruneExpiredData(ctx, w.RetentionDays) },
		"operational cleanup": w.Store.PruneOperationalData,
	} {
		if err := cleanup(ctx); err != nil {
			errorsCount++
			w.Logger.Warn(name+" failed", "error", err)
		}
	}
	return errorsCount
}

func (w *Worker) runOne(ctx context.Context) (bool, error) {
	job, err := w.Store.ClaimJob(ctx)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := w.Service.Process(jobCtx, job); err != nil {
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelPersist()
		_ = w.Store.FailJob(persistCtx, job.ID, err)
		return true, &JobError{JobID: job.ID, Kind: job.Kind, PRNumber: job.PRNumber, Err: err}
	}
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelPersist()
	if err := w.Store.CompleteJob(persistCtx, job.ID); err != nil {
		return true, &JobError{JobID: job.ID, Kind: job.Kind, PRNumber: job.PRNumber, Err: err}
	}
	return true, nil
}

type JobError struct {
	JobID    int64
	Kind     string
	PRNumber int
	Err      error
}

func (e *JobError) Error() string {
	return fmt.Sprintf("job %d (%s PR #%d): %v", e.JobID, e.Kind, e.PRNumber, e.Err)
}

func (e *JobError) Unwrap() error { return e.Err }
