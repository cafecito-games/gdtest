package app

import (
	"context"
	"errors"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
)

// foregroundShutdownGrace bounds how long a retired foreground worker may take
// to exit before it is killed.
const foregroundShutdownGrace = 3 * time.Second

// foregroundWorker is the part of a worker process the foreground runner uses.
// It exists so the runner's worker-replacement policy is testable without an
// engine.
type foregroundWorker interface {
	Run(context.Context, model.TestPlan, executor.EventSink) error
	Reset(context.Context) error
	Shutdown(context.Context) error
	TakeStartupTimings() map[string]int64
}

// foregroundWorkerFactory starts one worker for the plan's project, godot, and
// generation.
type foregroundWorkerFactory func(context.Context) (foregroundWorker, error)

// runPlanInForeground executes one plan in the caller's own process, one file
// at a time, and folds the per-file worker events into a single run.
//
// The worker is reused between files and retired whenever a file taints it, so
// a case timeout costs that worker and the remainder of its own file — never
// the files still queued behind it, which continue on a replacement worker.
func runPlanInForeground(
	ctx context.Context,
	start foregroundWorkerFactory,
	plan model.TestPlan,
	sink executor.EventSink,
) (map[string]time.Duration, error) {
	timings := make(map[string]time.Duration)
	if err := sink.Emit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted,
		RunID: plan.RunID, Generation: plan.Generation,
	}); err != nil {
		return timings, err
	}
	runner := &foregroundRunner{start: start, plan: plan, sink: sink, timings: timings}
	runErr := runner.executeFiles(ctx)
	runErr = errors.Join(runErr, runner.retireWorker())
	status := runner.status
	if runErr != nil {
		status = "error"
	}
	emitErr := sink.Emit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
		RunID: plan.RunID, Status: status, Generation: plan.Generation,
		Metadata: map[string]any{"files": len(plan.Files)},
	})
	return timings, errors.Join(runErr, emitErr)
}

type foregroundRunner struct {
	start   foregroundWorkerFactory
	plan    model.TestPlan
	sink    executor.EventSink
	timings map[string]time.Duration
	process foregroundWorker
	status  string
	tainted bool
}

func (r *foregroundRunner) executeFiles(ctx context.Context) error {
	r.status = "pass"
	files := r.plan.Files
	if len(files) == 0 {
		return nil
	}
	for _, file := range files {
		filePlan := r.plan
		filePlan.Files = []model.FilePlan{file}
		if err := r.executeFile(ctx, filePlan); err != nil {
			return err
		}
	}
	return nil
}

func (r *foregroundRunner) executeFile(ctx context.Context, filePlan model.TestPlan) error {
	process, err := r.ensureWorker(ctx)
	if err != nil {
		return err
	}
	r.tainted = false
	executionStarted := time.Now()
	runErr := process.Run(ctx, filePlan, r.fileSink())
	r.timings["foreground_worker_execution"] += time.Since(executionStarted)
	if runErr != nil {
		// The worker is already gone or untrustworthy; retiring it here keeps
		// the error from being reported twice.
		return errors.Join(runErr, r.retireWorker())
	}
	if r.tainted {
		return r.retireWorker()
	}
	resetStarted := time.Now()
	if err := process.Reset(ctx); err != nil {
		return errors.Join(err, r.retireWorker())
	}
	r.timings["foreground_worker_reset"] += time.Since(resetStarted)
	return nil
}

// fileSink folds one file's events into the single enclosing run: the worker's
// own run boundary events are absorbed, and its run status is promoted into
// the aggregate.
func (r *foregroundRunner) fileSink() executor.EventSink {
	return executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerReady, model.EventRunStarted:
			return nil
		case model.EventRunFinished:
			r.status = model.PromoteRunStatus(r.status, event.Status)
			return nil
		case model.EventWorkerTainted:
			r.tainted = true
		case model.EventCaseFinished, model.EventSuiteFinished:
			r.status = model.PromoteRunStatus(r.status, event.Status)
		case model.EventWorkerError:
			r.status = "error"
		}
		return r.sink.Emit(event)
	})
}

func (r *foregroundRunner) ensureWorker(ctx context.Context) (foregroundWorker, error) {
	if r.process != nil {
		return r.process, nil
	}
	workerStarted := time.Now()
	process, err := r.start(ctx)
	r.timings["worker_acquisition_startup"] += time.Since(workerStarted)
	if err != nil {
		return nil, err
	}
	mergeNanosecondTimings(r.timings, process.TakeStartupTimings())
	r.process = process
	return process, nil
}

func (r *foregroundRunner) retireWorker() error {
	process := r.process
	if process == nil {
		return nil
	}
	r.process = nil
	// Not ctx: a canceled run still has to collect its own child process.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), foregroundShutdownGrace)
	defer cancel()
	return process.Shutdown(shutdownCtx)
}
