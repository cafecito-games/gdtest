package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/scheduler"
	sourcewatch "github.com/cafecito-games/gdtest/internal/watch"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorker struct {
	pid            int
	generation     uint64
	runs           int
	benches        int
	discovers      int
	resets         int
	closed         atomic.Bool
	runHook        func(model.TestPlan, executor.EventSink) error
	runContextHook func(context.Context, model.TestPlan, executor.EventSink) error
	benchHook      func(model.BenchmarkPlan, executor.BenchmarkEventSink) error
	discoverHook   func(context.Context, model.TestPlan) (model.DiscoveryManifest, error)
	resetErr       error
	startupTime    map[string]int64
}

func (f *fakeWorker) Run(ctx context.Context, plan model.TestPlan, sink executor.EventSink) error {
	f.runs++
	if f.runContextHook != nil {
		return f.runContextHook(ctx, plan, sink)
	}
	if f.runHook != nil {
		return f.runHook(plan, sink)
	}
	return sink.Emit(model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished, RunID: plan.RunID})
}
func (f *fakeWorker) Bench(_ context.Context, plan model.BenchmarkPlan, sink executor.BenchmarkEventSink) error {
	f.benches++
	if f.benchHook != nil {
		return f.benchHook(plan, sink)
	}
	return sink.EmitBenchmark(model.BenchmarkEvent{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventBenchmarkRunFinished,
		RunID: plan.RunID, Status: "pass", Generation: plan.Generation,
	})
}
func (f *fakeWorker) Discover(ctx context.Context, plan model.TestPlan) (model.DiscoveryManifest, error) {
	f.discovers++
	if f.discoverHook != nil {
		return f.discoverHook(ctx, plan)
	}
	return model.DiscoveryManifest{
		ProtocolVersion:    model.ProtocolVersion,
		Generation:         plan.Generation,
		ContentFingerprint: plan.ContentFingerprint,
	}, nil
}
func (f *fakeWorker) Ping(context.Context) error  { return nil }
func (f *fakeWorker) Reset(context.Context) error { f.resets++; return f.resetErr }
func (f *fakeWorker) Reload(_ context.Context, generation uint64) error {
	f.generation = generation
	return nil
}
func (f *fakeWorker) Shutdown(context.Context) error { f.closed.Store(true); return nil }
func (f *fakeWorker) PID() int                       { return f.pid }
func (f *fakeWorker) Generation() uint64             { return f.generation }
func (f *fakeWorker) TakeStartupTimings() map[string]int64 {
	result := f.startupTime
	f.startupTime = nil
	return result
}

func TestSessionServiceReusesWorker(t *testing.T) {
	t.Parallel()

	created := 0
	process := &fakeWorker{pid: 42, generation: 1}
	service := &SessionService{
		sessions: make(map[string]*session),
		factory: func(context.Context, worker.ProcessOptions) (managedWorker, error) {
			created++
			return process, nil
		},
	}
	request := RunRequest{
		SessionID: "session-project", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "run-1", Generation: 1},
	}
	for index := range 2 {
		request.Plan.RunID = "run-" + string(rune('1'+index))
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		events := 0
		response, handleErr := service.Handle(context.Background(), daemon.Request{
			ID: "request", Method: "session.run", Payload: payload,
		}, func(any) error { events++; return nil })
		require.NoError(t, handleErr)
		assert.Equal(t, 1, events)
		result, ok := response.(map[string]any)
		require.True(t, ok)
		timings, ok := result["timings_ns"].(map[string]int64)
		require.True(t, ok)
		assert.Contains(t, timings, "queue_wait")
		assert.Contains(t, timings, "worker_execution_serialization")
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 2, process.runs)
	assert.Equal(t, 2, process.resets)
	sessions := service.List()
	require.Len(t, sessions, 1)
	assert.Equal(t, uint64(2), sessions[0].Runs)
}

func TestSessionServiceBenchReusesWorkerAndResetsIt(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 142, generation: 1}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	request := BenchRequest{
		SessionID: "benchmark-session", Project: "/project", Godot: "godot",
		Plan: model.BenchmarkPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "benchmark-run", Generation: 1,
			Samples: 2, Iterations: 1,
		},
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	events := 0
	response, err := service.Handle(context.Background(), daemon.Request{
		ID: "benchmark-request", Method: "session.bench", Payload: payload,
	}, func(any) error { events++; return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, events)
	assert.Equal(t, 1, process.benches)
	assert.Equal(t, 1, process.resets)
	result, ok := response.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 142, result["worker_pid"])
}

func TestSessionServiceBenchmarksAcrossWorktreesRespectGlobalWorkerBound(t *testing.T) {
	t.Parallel()

	const clients = 8
	const maxWorkers = 2
	var nextPID atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, maxWorkers)
	release := make(chan struct{})
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: int(nextPID.Add(1)) + 2000, generation: options.Generation}
		process.benchHook = func(plan model.BenchmarkPlan, sink executor.BenchmarkEventSink) error {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return sink.EmitBenchmark(model.BenchmarkEvent{
				ProtocolVersion: model.ProtocolVersion, Type: model.EventBenchmarkRunFinished,
				RunID: plan.RunID, Status: "pass", Generation: plan.Generation,
			})
		}
		return process, nil
	}, maxWorkers)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	errorsByClient := make(chan error, clients)
	for index := range clients {
		go func() {
			payload, err := json.Marshal(BenchRequest{
				SessionID: fmt.Sprintf("benchmark-worktree-%02d", index),
				Project:   fmt.Sprintf("/dirty-worktree-%02d", index),
				Godot:     "godot",
				Plan: model.BenchmarkPlan{
					ProtocolVersion: model.ProtocolVersion,
					RunID:           fmt.Sprintf("benchmark-run-%02d", index),
					Generation:      1,
					Samples:         1,
					Iterations:      1,
				},
			})
			if err != nil {
				errorsByClient <- err
				return
			}
			_, err = service.Handle(context.Background(), daemon.Request{
				ID: fmt.Sprintf("benchmark-request-%02d", index), Method: "session.bench", Payload: payload,
			}, func(any) error { return nil })
			errorsByClient <- err
		}()
	}
	for range maxWorkers {
		waitServiceSignal(t, started)
	}
	assert.Equal(t, maxWorkers, service.WorkerCount())
	assert.Equal(t, int32(maxWorkers), maximum.Load())
	close(release)
	for range clients {
		require.NoError(t, <-errorsByClient)
	}
	assert.Equal(t, int32(clients), nextPID.Load())
}

func TestSessionServiceBenchmarkCrashReplacementAndRecycling(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 3000 + len(processes), generation: options.Generation}
		if len(processes) == 0 {
			process.benchHook = func(model.BenchmarkPlan, executor.BenchmarkEventSink) error {
				return errors.New("benchmark worker crashed")
			}
		}
		processes = append(processes, process)
		return process, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	request := BenchRequest{
		SessionID: "benchmark-recovery", Project: "/dirty-worktree", Godot: "godot",
		WorkerMaxRuns: 1,
		Plan: model.BenchmarkPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "benchmark-crash", Generation: 1,
			Samples: 1, Iterations: 1,
		},
	}
	invoke := func() error {
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: request.Plan.RunID, Method: "session.bench", Payload: payload,
		}, func(any) error { return nil })
		return err
	}

	err := invoke()
	require.ErrorContains(t, err, "benchmark worker crashed")
	require.Len(t, processes, 1)
	assert.True(t, processes[0].closed.Load())

	request.Plan.RunID = "benchmark-replacement"
	require.NoError(t, invoke())
	require.Len(t, processes, 2)
	require.Eventually(t, processes[1].closed.Load, time.Second, time.Millisecond, "worker_max_runs must recycle the successful replacement")
}

func TestSessionServiceEmitsStructuredLifecycleLogs(t *testing.T) {
	t.Parallel()

	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return &fakeWorker{pid: 73, generation: options.Generation}, nil
	})
	service.sourceFactory = nil
	var logMu sync.Mutex
	var records []map[string]any
	service.log = func(record map[string]any) {
		logMu.Lock()
		defer logMu.Unlock()
		records = append(records, record)
	}
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	payload, err := json.Marshal(RunRequest{
		SessionID: "logged", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "logged-run",
			Generation:      1,
		},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "logged-request", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)

	logMu.Lock()
	defer logMu.Unlock()
	byEvent := make(map[string]map[string]any, len(records))
	for _, record := range records {
		byEvent[record["event"].(string)] = record
	}
	for _, event := range []string{"worker_started", "run_started", "run_finished"} {
		record, exists := byEvent[event]
		require.True(t, exists, "missing %s lifecycle log", event)
		assert.Equal(t, "logged", record["session_id"])
		assert.Equal(t, "pid:73", record["worker_id"])
		assert.Equal(t, uint64(1), record["generation"])
	}
	assert.Equal(t, "logged-run", byEvent["run_started"]["run_id"])
	assert.Equal(t, "logged-run", byEvent["run_finished"]["run_id"])
}

func TestMergeWorkerTimingsSeparatesExecutionAndSerializationPhases(t *testing.T) {
	t.Parallel()

	timings := map[string]int64{}
	mergeWorkerTimings(timings, map[string]any{
		"timings_usec": map[string]any{
			"script_load_dynamic_definition": float64(11),
			"hooks_cases":                    int64(22),
			"result_serialization":           33,
		},
	})

	assert.Equal(t, int64(11*time.Microsecond), timings["script_load_dynamic_definition"])
	assert.Equal(t, int64(22*time.Microsecond), timings["hooks_cases"])
	assert.Equal(t, int64(33*time.Microsecond), timings["result_serialization"])
}

func TestLifecycleLogLevelsPreserveFailuresAtStrictThresholds(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "error", lifecycleLogLevel("run_failed"))
	assert.Equal(t, "error", lifecycleLogLevel("source_reconcile_error"))
	assert.Equal(t, "debug", lifecycleLogLevel("worker_reused"))
	assert.Equal(t, "debug", lifecycleLogLevel("worker_late_callbacks"))
	assert.Equal(t, "debug", lifecycleLogLevel("discovery_finished"))
	assert.Equal(t, "info", lifecycleLogLevel("worker_started"))
}

func TestSessionServiceLogsLateCallbackInvocationsAtDebugLevel(t *testing.T) {
	t.Parallel()

	var nextPID atomic.Int32
	nextPID.Store(400)
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		pid := int(nextPID.Add(1))
		return &fakeWorker{
			pid:        pid,
			generation: options.Generation,
			runHook: func(plan model.TestPlan, sink executor.EventSink) error {
				return sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion,
					Type:            model.EventRunFinished,
					RunID:           plan.RunID,
					Status:          "pass",
					Generation:      plan.Generation,
					Metadata:        map[string]any{"late_callback_invocations": float64(5)},
				})
			},
		}, nil
	}, 2)
	service.sourceFactory = nil
	var logMu sync.Mutex
	var records []map[string]any
	service.log = func(record map[string]any) {
		logMu.Lock()
		defer logMu.Unlock()
		records = append(records, record)
	}
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	singlePayload, err := json.Marshal(RunRequest{
		SessionID: "late-callbacks", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "single-run",
			Generation:      1,
			Files:           []model.FilePlan{{Path: "a.test.gd"}},
		},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "single-run", Method: "session.run", Payload: singlePayload,
	}, func(any) error { return nil })
	require.NoError(t, err)

	multiPayload, err := json.Marshal(RunRequest{
		SessionID: "late-callbacks", Project: "/project", Godot: "godot", Jobs: 2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "multi-run",
			Generation:      1,
			Files:           []model.FilePlan{{Path: "a.test.gd"}, {Path: "b.test.gd"}},
		},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "multi-run", Method: "session.run", Payload: multiPayload,
	}, func(any) error { return nil })
	require.NoError(t, err)

	logMu.Lock()
	defer logMu.Unlock()
	var lateRecords []map[string]any
	for _, record := range records {
		if record["event"] == "worker_late_callbacks" {
			lateRecords = append(lateRecords, record)
		}
	}
	require.Len(t, lateRecords, 3)
	for _, record := range lateRecords {
		assert.Equal(t, "debug", record["level"])
		assert.Equal(t, int64(5), record["late_callback_invocations"])
		assert.Equal(t, "late-callbacks", record["session_id"])
	}
}

func TestSessionServiceRecyclesWorkerAtConfiguredAge(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 30 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	service.sourceFactory = nil
	service.maxWorkerAge = time.Minute
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	run := func(id string) {
		payload, err := json.Marshal(RunRequest{
			SessionID: "aged", Project: "/project", Godot: "godot",
			Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: id, Generation: 1},
		})
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: id, Method: "session.run", Payload: payload,
		}, func(any) error { return nil })
		require.NoError(t, err)
	}
	run("first")
	service.sessions["aged"].workerBorn = time.Now().Add(-2 * time.Minute)
	run("second")

	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceRecyclesWorkerWhenGlobalMemoryBudgetIsExceeded(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 130, generation: 1}
	service := newSessionService(func(_ context.Context, _ worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	service.memoryBudgetMB = 64
	service.memoryUsage = func(pid int) (uint64, error) {
		assert.Equal(t, 130, pid)
		return 65 * 1024 * 1024, nil
	}
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "memory", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "memory-run", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "memory-run", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })

	require.NoError(t, err)
	require.Eventually(t, process.closed.Load, time.Second, time.Millisecond)
	assert.Zero(t, service.totalWorkerBytes)
}

func TestSessionServiceRecyclesWhenMemoryCannotBeMeasured(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 131, generation: 1}
	service := newSessionService(func(_ context.Context, _ worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	service.memoryBudgetMB = 64
	service.memoryUsage = func(int) (uint64, error) { return 0, errors.New("unavailable") }
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "memory-error", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "memory-error-run", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "memory-error-run", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })

	require.NoError(t, err)
	require.Eventually(t, process.closed.Load, time.Second, time.Millisecond)
}

func TestSessionServiceManyClientsStayWithinWorkerAndMemoryBounds(t *testing.T) {
	t.Parallel()

	const (
		clients       = 24
		maxWorkers    = 8
		workerBytes   = 24 * 1024 * 1024
		memoryBudget  = 96
		retainedLimit = memoryBudget * 1024 * 1024
	)
	var nextPID atomic.Int32
	started := make(chan struct{}, maxWorkers)
	release := make(chan struct{})
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: int(nextPID.Add(1)) + 1000, generation: options.Generation}
		process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return sink.Emit(model.Event{
				ProtocolVersion: model.ProtocolVersion,
				Type:            model.EventRunFinished,
				RunID:           plan.RunID,
				Status:          "pass",
			})
		}
		return process, nil
	}, maxWorkers)
	service.sourceFactory = nil
	service.memoryBudgetMB = memoryBudget
	service.memoryUsage = func(pid int) (uint64, error) {
		assert.Greater(t, pid, 1000)
		return workerBytes, nil
	}
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	errorsByClient := make(chan error, clients)
	for index := range clients {
		go func() {
			payload, err := json.Marshal(RunRequest{
				SessionID: fmt.Sprintf("memory-client-%02d", index),
				Project:   fmt.Sprintf("/worktree-%02d", index),
				Godot:     "godot",
				Plan: model.TestPlan{
					ProtocolVersion: model.ProtocolVersion,
					RunID:           fmt.Sprintf("memory-run-%02d", index),
					Generation:      1,
				},
			})
			if err != nil {
				errorsByClient <- err
				return
			}
			_, err = service.Handle(context.Background(), daemon.Request{
				ID: fmt.Sprintf("memory-request-%02d", index), Method: "session.run", Payload: payload,
			}, func(any) error { return nil })
			errorsByClient <- err
		}()
	}
	for range maxWorkers {
		waitServiceSignal(t, started)
	}
	assert.Equal(t, maxWorkers, service.WorkerCount())
	close(release)
	for range clients {
		require.NoError(t, <-errorsByClient)
	}

	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.liveWorkers <= memoryBudget/(workerBytes/(1024*1024)) &&
			service.totalWorkerBytes <= retainedLimit
	}, 2*time.Second, time.Millisecond)
	service.mu.Lock()
	assert.LessOrEqual(t, service.totalWorkerBytes, uint64(retainedLimit))
	assert.LessOrEqual(t, service.liveWorkers, memoryBudget/(workerBytes/(1024*1024)))
	service.mu.Unlock()
}

func TestSessionServiceRejectsSessionIdentityRebinding(t *testing.T) {
	t.Parallel()

	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return &fakeWorker{pid: 41, generation: options.Generation}, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	for index, request := range []WarmRequest{
		{SessionID: "collision", Project: "/worktree-a", Godot: "godot", Generation: 1},
		{SessionID: "collision", Project: "/worktree-b", Godot: "godot", Generation: 1},
	} {
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: fmt.Sprintf("request-%d", index), Method: "session.warm", Payload: payload,
		}, nil)
		if index == 0 {
			require.NoError(t, err)
		} else {
			require.EqualError(t, err, `session "collision" is already bound to project "/worktree-a" and Godot "godot"`)
		}
	}
}

func TestSessionServiceReportsGenerationWithoutCreatingWorker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	created := 0
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		created++
		return &fakeWorker{pid: 51, generation: options.Generation}, nil
	})
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	generation := func(id string) uint64 {
		payload, err := json.Marshal(GenerationRequest{
			SessionID: "generation-session", Project: root, Godot: "godot",
		})
		require.NoError(t, err)
		response, err := service.Handle(context.Background(), daemon.Request{
			ID: id, Method: "session.generation", Payload: payload,
		}, nil)
		require.NoError(t, err)
		values, ok := response.(map[string]any)
		require.True(t, ok)
		value, ok := values["generation"].(uint64)
		require.True(t, ok)
		return value
	}

	assert.Equal(t, uint64(1), generation("initial"))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "example.test.gd"),
		[]byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"),
		0o600,
	))
	assert.Equal(t, uint64(2), generation("changed"))
	assert.Zero(t, created)
	assert.Zero(t, service.WorkerCount())
}

func TestSessionServiceGenerationRequestIsStrict(t *testing.T) {
	t.Parallel()
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return &fakeWorker{pid: 52, generation: options.Generation}, nil
	})
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	_, err := service.Handle(context.Background(), daemon.Request{
		ID: "strict", Method: "session.generation",
		Payload: json.RawMessage(`{"session_id":"session","project":"/project","godot":"godot","unexpected":true}`),
	}, nil)
	require.ErrorContains(t, err, `unknown field "unexpected"`)
}

func TestSessionServiceGenerationCanReconcileDuringActiveRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	started := make(chan struct{})
	release := make(chan struct{})
	process := &fakeWorker{pid: 53, generation: 1}
	process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
		close(started)
		<-release
		return sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventRunFinished,
			RunID:           plan.RunID,
		})
	}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		require.NoError(t, service.Close(context.Background()))
	})

	runPayload, err := json.Marshal(RunRequest{
		SessionID: "concurrent-generation", Project: root, Godot: "godot",
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "active-run",
			Generation:      1,
		},
	})
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := service.Handle(context.Background(), daemon.Request{
			ID: "run", Method: "session.run", Payload: runPayload,
		}, func(any) error { return nil })
		runDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "active run did not start")
	}

	generationPayload, err := json.Marshal(GenerationRequest{
		SessionID: "concurrent-generation", Project: root, Godot: "godot",
	})
	require.NoError(t, err)
	generationDone := make(chan error, 1)
	go func() {
		_, generationErr := service.Handle(context.Background(), daemon.Request{
			ID: "generation", Method: "session.generation", Payload: generationPayload,
		}, nil)
		generationDone <- generationErr
	}()
	close(release)
	require.NoError(t, <-runDone)
	require.NoError(t, <-generationDone)
}

func TestSessionServiceRejectsUnsafeOrIncompatiblePlans(t *testing.T) {
	t.Parallel()

	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return &fakeWorker{pid: 43, generation: options.Generation}, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	for _, test := range []struct {
		name string
		plan model.TestPlan
		want string
	}{
		{
			name: "protocol",
			plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion + 1, RunID: "run", Generation: 1},
			want: fmt.Sprintf("protocol version %d does not match %d", model.ProtocolVersion+1, model.ProtocolVersion),
		},
		{
			name: "traversal",
			plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1,
				Files: []model.FilePlan{{Path: "../outside.test.gd"}},
			},
			want: "path must be normalized and remain inside the project",
		},
		{
			name: "resource path",
			plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1,
				Files: []model.FilePlan{{Path: "res://tests/example.test.gd"}},
			},
			want: "path must be normalized",
		},
		{
			name: "leak check policy",
			plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1,
				LeakCheck: model.LeakCheck{Policy: "strict"},
			},
			want: `leak_check.policy "strict" must be fail, warn, or off`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(RunRequest{
				SessionID: "invalid-" + test.name, Project: "/project", Godot: "godot", Plan: test.plan,
			})
			require.NoError(t, err)
			_, err = service.Handle(context.Background(), daemon.Request{
				ID: "request", Method: "session.run", Payload: payload,
			}, func(any) error { return nil })
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestSessionServiceReplacesGeneration(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := &SessionService{
		sessions: make(map[string]*session),
		factory: func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
			process := &fakeWorker{pid: 40 + len(processes), generation: options.Generation}
			processes = append(processes, process)
			return process, nil
		},
	}
	for _, generation := range []uint64{1, 2} {
		payload, err := json.Marshal(WarmRequest{
			SessionID: "session-project", Project: "/project", Godot: "godot", Generation: generation,
		})
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: "warm", Method: "session.warm", Payload: payload,
		}, nil)
		require.NoError(t, err)
	}
	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceBoundsRetainedWorkers(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 100 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	service.sourceFactory = nil
	service.maxWorkers = 3
	for index := range 10 {
		payload, err := json.Marshal(WarmRequest{
			SessionID: fmt.Sprintf("session-%02d", index),
			Project:   fmt.Sprintf("/worktree-%02d", index), Godot: "godot", Generation: 1,
		})
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: "warm", Method: "session.warm", Payload: payload,
		}, nil)
		require.NoError(t, err)
		assert.LessOrEqual(t, service.WorkerCount(), 3)
	}
	closed := 0
	for _, process := range processes {
		if process.closed.Load() {
			closed++
		}
	}
	assert.Equal(t, 3, service.WorkerCount())
	assert.Equal(t, 7, closed)
}

func TestSessionServiceSchedulesFilesConcurrentlyWithinProjectJobs(t *testing.T) {
	t.Parallel()

	var factoryMu sync.Mutex
	var processes []*fakeWorker
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{generation: options.Generation}
		process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
			current := active.Add(1)
			for {
				previous := peak.Load()
				if current <= previous || peak.CompareAndSwap(previous, current) {
					break
				}
			}
			select {
			case <-release:
			default:
				started <- struct{}{}
				<-release
			}
			active.Add(-1)
			id := plan.Files[0].Path + "::test"
			if err := sink.Emit(model.Event{Type: model.EventCaseStarted, RunID: plan.RunID, TestID: id}); err != nil {
				return err
			}
			if err := sink.Emit(model.Event{Type: model.EventCaseFinished, RunID: plan.RunID, TestID: id, Status: "pass"}); err != nil {
				return err
			}
			return sink.Emit(model.Event{
				Type: model.EventRunFinished, RunID: plan.RunID, Status: "pass",
				Metadata: map[string]any{"timings_usec": map[string]any{
					"script_load_dynamic_definition": float64(11),
					"hooks_cases":                    float64(22),
					"result_serialization":           float64(33),
				}},
			})
		}
		factoryMu.Lock()
		process.pid = 900 + len(processes)
		processes = append(processes, process)
		factoryMu.Unlock()
		return process, nil
	}, 3)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	request := RunRequest{
		SessionID: "files", Project: "/project", Godot: "godot", Jobs: 2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "multi-file", Generation: 1,
			Files: []model.FilePlan{{Path: "a.test.gd"}, {Path: "b.test.gd"}, {Path: "c.test.gd"}},
		},
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	var eventsMu sync.Mutex
	var events []model.Event
	done := make(chan error, 1)
	go func() {
		_, handleErr := service.Handle(context.Background(), daemon.Request{
			ID: "request", Method: "session.run", Payload: payload,
		}, func(value any) error {
			event, ok := value.(model.Event)
			if ok {
				eventsMu.Lock()
				events = append(events, event)
				eventsMu.Unlock()
			}
			return nil
		})
		done <- handleErr
	}()
	waitServiceSignal(t, started)
	waitServiceSignal(t, started)
	assert.Equal(t, int32(2), peak.Load())
	close(release)
	require.NoError(t, <-done)

	eventsMu.Lock()
	defer eventsMu.Unlock()
	counts := make(map[model.EventType]int)
	for _, event := range events {
		counts[event.Type]++
	}
	assert.Equal(t, 1, counts[model.EventRunStarted])
	assert.Equal(t, 3, counts[model.EventCaseStarted])
	assert.Equal(t, 3, counts[model.EventCaseFinished])
	assert.Equal(t, 1, counts[model.EventRunFinished])
	require.Len(t, processes, 2)
	assert.False(t, processes[0].closed.Load(), "the primary worker remains warm")
	assert.False(t, processes[1].closed.Load(), "the additional lane remains warm for the next run")
	assert.Equal(t, 3, processes[0].runs+processes[1].runs)
	assert.Equal(t, 3, processes[0].resets+processes[1].resets)

	request.Plan.RunID = "multi-file-second"
	payload, err = json.Marshal(request)
	require.NoError(t, err)
	response, err := service.Handle(context.Background(), daemon.Request{
		ID: "request-second", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)
	values, ok := response.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, values["reused"])
	timings, ok := values["timings_ns"].(map[string]int64)
	require.True(t, ok)
	for _, phase := range []string{
		"queue_wait", "worker_acquisition_startup", "worker_execution_serialization",
		"script_load_dynamic_definition", "hooks_cases", "result_serialization",
		"worker_reset", "daemon_total",
	} {
		assert.Contains(t, timings, phase)
	}
	require.Len(t, processes, 2, "the second command reuses every warm lane")
	assert.False(t, processes[0].closed.Load())
	assert.False(t, processes[1].closed.Load())
	assert.Equal(t, 6, processes[0].runs+processes[1].runs)
	require.Len(t, service.List(), 1)
	assert.Equal(t, uint64(6), service.List()[0].Runs)
}

func TestSessionServiceBootstrapsPrimaryBeforeStartingAdditionalWorkerLanes(t *testing.T) {
	t.Parallel()

	var factoryMu sync.Mutex
	created := 0
	primaryCreating := make(chan struct{})
	releasePrimary := make(chan struct{})
	extraCreated := make(chan struct{})
	var extraOnce sync.Once
	runsStarted := make(chan struct{}, 3)
	releaseRuns := make(chan struct{})
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		factoryMu.Lock()
		created++
		ordinal := created
		factoryMu.Unlock()
		if ordinal == 1 {
			close(primaryCreating)
			<-releasePrimary
		} else {
			extraOnce.Do(func() { close(extraCreated) })
		}
		process := &fakeWorker{pid: 950 + ordinal, generation: options.Generation}
		process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
			runsStarted <- struct{}{}
			<-releaseRuns
			return sink.Emit(model.Event{
				ProtocolVersion: model.ProtocolVersion,
				Type:            model.EventRunFinished,
				RunID:           plan.RunID,
				Status:          "pass",
			})
		}
		return process, nil
	}, 3)
	service.sourceFactory = nil
	t.Cleanup(func() {
		select {
		case <-releasePrimary:
		default:
			close(releasePrimary)
		}
		select {
		case <-releaseRuns:
		default:
			close(releaseRuns)
		}
		require.NoError(t, service.Close(context.Background()))
	})

	payload, err := json.Marshal(RunRequest{
		SessionID: "ordered-bootstrap", Project: "/project", Godot: "godot", Jobs: 3,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "ordered-bootstrap-run",
			Generation:      1,
			Files: []model.FilePlan{
				{Path: "a.test.gd"},
				{Path: "b.test.gd"},
				{Path: "c.test.gd"},
			},
		},
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, handleErr := service.Handle(context.Background(), daemon.Request{
			ID: "ordered-bootstrap-request", Method: "session.run", Payload: payload,
		}, func(any) error { return nil })
		done <- handleErr
	}()

	waitServiceSignal(t, primaryCreating)
	select {
	case <-extraCreated:
		require.FailNow(t, "additional worker started before primary bootstrap completed")
	default:
	}
	factoryMu.Lock()
	assert.Equal(t, 1, created)
	factoryMu.Unlock()
	close(releasePrimary)
	for range 3 {
		waitServiceSignal(t, runsStarted)
	}
	factoryMu.Lock()
	assert.Equal(t, 3, created)
	factoryMu.Unlock()
	close(releaseRuns)
	require.NoError(t, <-done)
}

func TestSessionServiceMultiFileRunLimitCountsPerWorkerLane(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 980 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	}, 1)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "multi-file-run-limit", Project: "/project", Godot: "godot",
		Jobs: 1, WorkerMaxRuns: 2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "multi-file-run-limit",
			Generation:      1,
			Files: []model.FilePlan{
				{Path: "a.test.gd"},
				{Path: "b.test.gd"},
				{Path: "c.test.gd"},
				{Path: "d.test.gd"},
				{Path: "e.test.gd"},
			},
		},
	})
	require.NoError(t, err)
	response, err := service.Handle(context.Background(), daemon.Request{
		ID: "multi-file-run-limit-request", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)

	require.Len(t, processes, 3)
	assert.Equal(t, []int{2, 2, 1}, []int{processes[0].runs, processes[1].runs, processes[2].runs})
	assert.True(t, processes[0].closed.Load())
	assert.True(t, processes[1].closed.Load())
	assert.False(t, processes[2].closed.Load())
	assert.Equal(t, 1, service.WorkerCount())
	require.Len(t, service.List(), 1)
	assert.Equal(t, uint64(5), service.List()[0].Runs)
	values, ok := response.(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []int{980, 980, 981, 981, 982}, values["worker_pids"])
}

func waitServiceSignal(t *testing.T, values <-chan struct{}) {
	t.Helper()
	select {
	case <-values:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for service signal")
	}
}

func TestSessionServiceMarksChangedRunStaleAndReloadsBodyOnlyGeneration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))

	var processes []*fakeWorker
	processChanged := false
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 300 + len(processes), generation: options.Generation}
		if !processChanged {
			process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
				if err := sink.Emit(model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted, RunID: plan.RunID}); err != nil {
					return err
				}
				processChanged = true
				if err := os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tvar changed: int = 1\n"), 0o600); err != nil {
					return err
				}
				return sink.Emit(model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished, RunID: plan.RunID})
			}
		}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { _ = service.Close(context.Background()) }()

	request := RunRequest{
		SessionID: "generation-session", Project: root, Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "changing-run", Generation: 1},
	}
	var finished model.Event
	emit := func(value any) error {
		event, ok := value.(model.Event)
		if ok && event.Type == model.EventRunFinished {
			finished = event
		}
		return nil
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "run", Method: "session.run", Payload: payload,
	}, emit)
	require.NoError(t, err)
	assert.True(t, finished.Stale)
	assert.Equal(t, uint64(1), finished.Metadata["requested_generation"])
	assert.Equal(t, uint64(2), finished.Metadata["current_generation"])

	request.Plan.RunID = "replacement-run"
	payload, err = json.Marshal(request)
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "run-2", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.EqualError(t, err, "source generation changed from 1 to 2; rediscover and retry")

	request.Plan.Generation = 2
	request.Plan.RunID = "rediscovered-run"
	payload, err = json.Marshal(request)
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "run-3", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)
	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.Equal(t, uint64(2), processes[1].Generation())
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceReplacesWorkerAfterStructuralChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 400 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { _ = service.Close(context.Background()) }()
	warm := func(id string) {
		payload, err := json.Marshal(WarmRequest{
			SessionID: "structural-session", Project: root, Godot: "godot", Generation: 1,
		})
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: id, Method: "session.warm", Payload: payload,
		}, nil)
		require.NoError(t, err)
	}
	warm("initial")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nsignal changed\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	warm("changed")
	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.Equal(t, uint64(2), processes[1].Generation())
}

func TestSessionServicePeriodicReconcileInvalidatesObsoleteWork(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 450 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { require.NoError(t, service.Close(context.Background())) }()
	payload, err := json.Marshal(WarmRequest{
		SessionID: "periodic-session", Project: root, Godot: "godot", Generation: 1,
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tvar changed: int = 1\n"), 0o600))
	updates, failures := service.ReconcileSources(context.Background())
	assert.Empty(t, failures)
	require.Contains(t, updates, "periodic-session")
	assert.Equal(t, uint64(2), updates["periodic-session"].Update.Generation)

	future := service.scheduler.Submit(context.Background(), scheduler.Request{
		Session: service.sessions["periodic-session"].schedulerKey, Generation: 1, Priority: scheduler.Background,
		Run: func(context.Context, scheduler.Lease) error { return nil },
	})
	result, err := future.Wait(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, result.Err, scheduler.ErrObsoleteGeneration)

	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm-current", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)
	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.Equal(t, uint64(2), processes[1].Generation())
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceAcceptsWorkForSessionRecreatedAfterIdleEviction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 470 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { require.NoError(t, service.Close(context.Background())) }()
	payload, err := json.Marshal(WarmRequest{
		SessionID: "evicted-session", Project: root, Godot: "godot", Generation: 1,
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)
	retired := service.sessions["evicted-session"].schedulerKey

	removed, err := service.Reconcile(time.Now().UTC().Add(time.Hour), time.Minute, time.Minute)
	require.NoError(t, err)
	require.Equal(t, projectidentity.RemoveIdle, removed["evicted-session"])

	// The same project session id comes back with a fresh source tracker at
	// generation 1; the eviction fence must not reject it.
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm-again", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)
	require.Len(t, processes, 2)
	assert.False(t, processes[1].closed.Load())
	assert.NotEqual(t, retired, service.sessions["evicted-session"].schedulerKey)

	// Work still addressed to the evicted incarnation stays fenced.
	future := service.scheduler.Submit(context.Background(), scheduler.Request{
		Session: retired, Generation: 1, Priority: scheduler.Interactive,
		Run: func(context.Context, scheduler.Lease) error { return nil },
	})
	result, err := future.Wait(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, result.Err, scheduler.ErrObsoleteGeneration)
}

func TestSessionServiceReportsFencedWorkAsRetryableStaleGeneration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return &fakeWorker{pid: 490, generation: options.Generation}, nil
	})
	defer func() { require.NoError(t, service.Close(context.Background())) }()
	entry, err := service.get("fenced-session", root, "godot", 0)
	require.NoError(t, err)
	_, err = service.reconcileSource(context.Background(), entry, sourcewatch.Initial)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tvar changed: int = 1\n"), 0o600))
	_, err = service.reconcileSource(context.Background(), entry, sourcewatch.Periodic)
	require.NoError(t, err)

	ran := false
	_, err = service.schedule(
		context.Background(), entry, 1, scheduler.Interactive, scheduler.Reuse,
		func(context.Context) (any, error) { ran = true; return nil, nil },
	)
	require.EqualError(t, err, "source generation changed from 1 to 2"+StaleGenerationSuffix)
	require.ErrorIs(t, err, scheduler.ErrObsoleteGeneration)
	assert.False(t, ran)

	service.scheduler.Retire(entry.schedulerKey)
	_, err = service.schedule(
		context.Background(), entry, 2, scheduler.Interactive, scheduler.Reuse,
		func(context.Context) (any, error) { ran = true; return nil, nil },
	)
	require.ErrorIs(t, err, scheduler.ErrObsoleteGeneration)
	assert.True(t, strings.HasSuffix(err.Error(), StaleGenerationSuffix), err.Error())
	assert.False(t, ran)
}

func TestSessionServiceReconcilesDeletedWorktree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	process := &fakeWorker{pid: 88, generation: 1}
	service := &SessionService{
		sessions: make(map[string]*session),
		factory: func(context.Context, worker.ProcessOptions) (managedWorker, error) {
			return process, nil
		},
	}
	payload, err := json.Marshal(WarmRequest{
		SessionID: "deleted-worktree", Project: root, Godot: "godot", Generation: 1,
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)
	require.NoError(t, os.Remove(root))
	now := time.Now().UTC()
	removed, err := service.Reconcile(now, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Empty(t, removed)
	removed, err = service.Reconcile(now.Add(2*time.Minute), time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, projectidentity.RemoveMissing, removed["deleted-worktree"])
	assert.True(t, process.closed.Load())
	assert.Zero(t, service.WorkerCount())
}

func TestSessionServiceRecyclesAtRunLimit(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 91, generation: 1}
	service := &SessionService{
		sessions: make(map[string]*session),
		factory: func(context.Context, worker.ProcessOptions) (managedWorker, error) {
			return process, nil
		},
	}
	payload, err := json.Marshal(RunRequest{
		SessionID: "recycle", Project: "/project", Godot: "godot", WorkerMaxRuns: 1,
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "run", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)
	assert.Eventually(t, process.closed.Load, time.Second, time.Millisecond)
	assert.Zero(t, service.WorkerCount())
}

func TestSessionServiceRunLimitCountsPerWorker(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 600 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	for index := range 3 {
		payload, err := json.Marshal(RunRequest{
			SessionID: "per-worker-limit", Project: "/project", Godot: "godot", WorkerMaxRuns: 2,
			Plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion,
				RunID:           fmt.Sprintf("run-%d", index),
				Generation:      1,
			},
		})
		require.NoError(t, err)
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: fmt.Sprintf("request-%d", index), Method: "session.run", Payload: payload,
		}, func(any) error { return nil })
		require.NoError(t, err)
	}

	require.Len(t, processes, 2)
	assert.Equal(t, 2, processes[0].runs)
	assert.Eventually(t, processes[0].closed.Load, time.Second, time.Millisecond)
	assert.Equal(t, 1, processes[1].runs)
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceIsolationLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		isolation       string
		wantProcesses   int
		wantFirstClosed bool
		wantSecondOpen  bool
	}{
		{name: "reuse", isolation: "reuse", wantProcesses: 1},
		{name: "recycle", isolation: "recycle", wantProcesses: 2, wantFirstClosed: true},
		{name: "fresh", isolation: "fresh", wantProcesses: 2, wantFirstClosed: true},
		{name: "exclusive", isolation: "exclusive", wantProcesses: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var processes []*fakeWorker
			service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
				process := &fakeWorker{pid: 700 + len(processes), generation: options.Generation}
				processes = append(processes, process)
				return process, nil
			})
			service.sourceFactory = nil
			t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

			for index := range 2 {
				payload, err := json.Marshal(RunRequest{
					SessionID: "isolation-" + test.name, Project: "/project", Godot: "godot",
					Isolation: test.isolation,
					Plan: model.TestPlan{
						ProtocolVersion: model.ProtocolVersion,
						RunID:           fmt.Sprintf("run-%d", index),
						Generation:      1,
					},
				})
				require.NoError(t, err)
				_, err = service.Handle(context.Background(), daemon.Request{
					ID: fmt.Sprintf("request-%d", index), Method: "session.run", Payload: payload,
				}, func(any) error { return nil })
				require.NoError(t, err)
			}

			require.Len(t, processes, test.wantProcesses)
			if test.wantFirstClosed {
				assert.Eventually(t, processes[0].closed.Load, time.Second, time.Millisecond)
			}
			if test.wantSecondOpen {
				assert.False(t, processes[1].closed.Load())
			}
		})
	}
}

func TestSessionServiceTaintedWorkerIsRecycled(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 800, generation: 1}
	process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
		if err := sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventWorkerTainted,
			RunID:           plan.RunID,
		}); err != nil {
			return err
		}
		return sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventRunFinished,
			RunID:           plan.RunID,
		})
	}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })
	payload, err := json.Marshal(RunRequest{
		SessionID: "tainted", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "request", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)
	assert.Eventually(t, process.closed.Load, time.Second, time.Millisecond)
}

func TestSessionServiceDiscoversWithReusedWorker(t *testing.T) {
	t.Parallel()

	created := 0
	process := &fakeWorker{
		pid: 501, generation: 1,
		startupTime: map[string]int64{"project_bootstrap_import": 1234},
	}
	process.discoverHook = func(_ context.Context, plan model.TestPlan) (model.DiscoveryManifest, error) {
		return model.DiscoveryManifest{
			ProtocolVersion:    model.ProtocolVersion,
			Generation:         plan.Generation,
			ContentFingerprint: plan.ContentFingerprint,
			Files: []model.DiscoveryFile{{
				Path: "tests/example.test.gd",
				Cases: []model.DiscoveryCase{{
					ID: "tests/example.test.gd::dynamic@4:0[value]", Name: "value",
					File: "tests/example.test.gd", Line: 4,
				}},
			}},
		}, nil
	}
	service := &SessionService{
		sessions: make(map[string]*session),
		factory: func(context.Context, worker.ProcessOptions) (managedWorker, error) {
			created++
			return process, nil
		},
	}
	request := DiscoverRequest{
		SessionID: "discover-project", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "discover-1", Generation: 1,
			ContentFingerprint: "content-1",
		},
	}
	for index := range 2 {
		request.Plan.RunID = fmt.Sprintf("discover-%d", index+1)
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		value, err := service.Handle(context.Background(), daemon.Request{
			ID: request.Plan.RunID, Method: "session.discover", Payload: payload,
		}, nil)
		require.NoError(t, err)
		response, ok := value.(DiscoverResponse)
		require.True(t, ok)
		manifest := response.Manifest
		require.Len(t, manifest.Files, 1)
		assert.Equal(t, "tests/example.test.gd::dynamic@4:0[value]", manifest.Files[0].Cases[0].ID)
		if index == 0 {
			assert.EqualValues(t, 1234, response.TimingsNS["project_bootstrap_import"])
		} else {
			assert.NotContains(t, response.TimingsNS, "project_bootstrap_import")
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 2, process.discovers)
	assert.Equal(t, 2, process.resets)
	assert.False(t, process.closed.Load())
	assert.Zero(t, service.List()[0].Runs, "discovery must not count as an executed run")
}

func TestSessionServiceDiscoveryRejectsStaleGenerationAndReplacesWorker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 502 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { require.NoError(t, service.Close(context.Background())) }()

	discover := func(id string, generation uint64) (model.DiscoveryManifest, error) {
		payload, err := json.Marshal(DiscoverRequest{
			SessionID: "discover-generation", Project: root, Godot: "godot",
			Plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion, RunID: id, Generation: generation,
				ContentFingerprint: id,
			},
		})
		require.NoError(t, err)
		value, err := service.Handle(context.Background(), daemon.Request{
			ID: id, Method: "session.discover", Payload: payload,
		}, nil)
		if err != nil {
			return model.DiscoveryManifest{}, err
		}
		response, ok := value.(DiscoverResponse)
		require.True(t, ok)
		return response.Manifest, nil
	}

	manifest, err := discover("initial", 1)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), manifest.Generation)
	require.NoError(t, os.WriteFile(
		script,
		[]byte("extends GDTest\nfunc test_value() -> void:\n\tvar changed: int = 1\n"),
		0o600,
	))
	_, err = discover("stale", 1)
	require.EqualError(t, err, "source generation changed from 1 to 2; rediscover and retry")
	manifest, err = discover("changed", 2)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), manifest.Generation)
	require.Len(t, processes, 2)
	assert.True(t, processes[0].closed.Load())
	assert.Equal(t, uint64(2), processes[1].Generation())
	assert.Equal(t, 1, processes[1].discovers)
	assert.False(t, processes[1].closed.Load())
}

func TestSessionServiceDiscoveryFailureDiscardsWorker(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	process := &fakeWorker{pid: 503, generation: 1}
	process.discoverHook = func(ctx context.Context, _ model.TestPlan) (model.DiscoveryManifest, error) {
		close(entered)
		<-ctx.Done()
		return model.DiscoveryManifest{}, ctx.Err()
	}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	ctx, cancel := context.WithCancel(context.Background())
	payload, err := json.Marshal(DiscoverRequest{
		SessionID: "cancel-discovery", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "discover", Generation: 1},
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, handleErr := service.Handle(ctx, daemon.Request{
			ID: "discover", Method: "session.discover", Payload: payload,
		}, nil)
		done <- handleErr
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Eventually(t, func() bool { return service.WorkerCount() == 0 }, time.Second, time.Millisecond)
	assert.True(t, process.closed.Load())
}

func TestSessionServiceDiscoveryResetFailureDiscardsWorker(t *testing.T) {
	t.Parallel()

	process := &fakeWorker{pid: 504, generation: 1, resetErr: errors.New("reset failed")}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return process, nil
	})
	service.sourceFactory = nil
	payload, err := json.Marshal(DiscoverRequest{
		SessionID: "reset-discovery", Project: "/project", Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: model.ProtocolVersion, RunID: "discover", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "discover", Method: "session.discover", Payload: payload,
	}, nil)
	require.EqualError(t, err, "reset worker after discovery: reset failed")
	assert.True(t, process.closed.Load())
	assert.Zero(t, service.WorkerCount())
}

func TestSessionServiceDiscoveryRequestValidation(t *testing.T) {
	t.Parallel()

	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
		return nil, errors.New("must not create worker")
	})
	service.sourceFactory = nil
	tests := []struct {
		name    string
		payload string
		match   string
	}{
		{name: "missing required", payload: `{}`, match: "discover needs session_id, project, godot, and plan.run_id"},
		{name: "unknown field", payload: `{"session_id":"s","project":"/p","godot":"g","plan":{"run_id":"r"},"extra":true}`, match: `unknown field "extra"`},
		{name: "trailing value", payload: `{"session_id":"s","project":"/p","godot":"g","plan":{"run_id":"r"}} {}`, match: "multiple JSON values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := service.Handle(context.Background(), daemon.Request{
				ID: "discover", Method: "session.discover", Payload: json.RawMessage(test.payload),
			}, nil)
			require.ErrorContains(t, err, test.match)
		})
	}
}

func TestSessionServiceMultiFileRunPromotesWorkerFrameworkErrorToRunFinishedStatus(t *testing.T) {
	t.Parallel()

	var nextPID atomic.Int32
	nextPID.Store(600)
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		pid := int(nextPID.Add(1))
		return &fakeWorker{
			pid:        pid,
			generation: options.Generation,
			runHook: func(plan model.TestPlan, sink executor.EventSink) error {
				filePath := plan.Files[0].Path
				caseID := filePath + "::test_passes"
				if err := sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion,
					Type:            model.EventCaseStarted,
					RunID:           plan.RunID,
					TestID:          caseID,
				}); err != nil {
					return err
				}
				if err := sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion,
					Type:            model.EventCaseFinished,
					RunID:           plan.RunID,
					TestID:          caseID,
					Status:          "pass",
				}); err != nil {
					return err
				}
				status := "pass"
				if filePath == "b.test.gd" {
					status = "error"
					if err := sink.Emit(model.Event{
						ProtocolVersion: model.ProtocolVersion,
						Type:            model.EventWorkerError,
						RunID:           plan.RunID,
						Name:            "worker.framework",
						Assertion:       "framework",
						Message:         "expect is only valid while a case is executing",
						Location:        &model.Location{File: "b.test.gd", Line: 15},
						Metadata:        map[string]any{"code": "worker.framework"},
					}); err != nil {
						return err
					}
				}
				return sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion,
					Type:            model.EventRunFinished,
					RunID:           plan.RunID,
					Status:          status,
					Generation:      plan.Generation,
				})
			},
		}, nil
	}, 2)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "multi-file-hook-error",
		Project:   "/project",
		Godot:     "godot",
		Jobs:      2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "multi-file-run",
			Generation:      1,
			Files:           []model.FilePlan{{Path: "a.test.gd"}, {Path: "b.test.gd"}},
		},
	})
	require.NoError(t, err)

	var workerErrors []model.Event
	var runFinished model.Event
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "multi-file-run", Method: "session.run", Payload: payload,
	}, func(value any) error {
		event, ok := value.(model.Event)
		if !ok {
			return fmt.Errorf("unexpected daemon event %T", value)
		}
		switch event.Type {
		case model.EventWorkerError:
			workerErrors = append(workerErrors, event)
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, workerErrors, 1)
	assert.Equal(t, "worker.framework", workerErrors[0].Name)
	assert.Equal(t, "error", runFinished.Status)
}
