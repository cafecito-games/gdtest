package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/scheduler"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorker struct {
	pid        int
	generation uint64
	runs       int
	resets     int
	closed     bool
	runHook    func(model.TestPlan, executor.EventSink) error
}

func (f *fakeWorker) Run(_ context.Context, plan model.TestPlan, sink executor.EventSink) error {
	f.runs++
	if f.runHook != nil {
		return f.runHook(plan, sink)
	}
	return sink.Emit(model.Event{ProtocolVersion: 1, Type: model.EventRunFinished, RunID: plan.RunID})
}
func (f *fakeWorker) Ping(context.Context) error  { return nil }
func (f *fakeWorker) Reset(context.Context) error { f.resets++; return nil }
func (f *fakeWorker) Reload(_ context.Context, generation uint64) error {
	f.generation = generation
	return nil
}
func (f *fakeWorker) Shutdown(context.Context) error { f.closed = true; return nil }
func (f *fakeWorker) PID() int                       { return f.pid }
func (f *fakeWorker) Generation() uint64             { return f.generation }

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
		Plan: model.TestPlan{ProtocolVersion: 1, RunID: "run-1", Generation: 1},
	}
	for index := range 2 {
		request.Plan.RunID = "run-" + string(rune('1'+index))
		payload, err := json.Marshal(request)
		require.NoError(t, err)
		events := 0
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: "request", Method: "session.run", Payload: payload,
		}, func(any) error { events++; return nil })
		require.NoError(t, err)
		assert.Equal(t, 1, events)
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 2, process.runs)
	assert.Equal(t, 2, process.resets)
	sessions := service.List()
	require.Len(t, sessions, 1)
	assert.Equal(t, uint64(2), sessions[0].Runs)
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
		if generation == 2 {
			entry := service.sessions["session-project"]
			entry.lastStructural = 2
		}
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
	assert.True(t, processes[0].closed)
	assert.False(t, processes[1].closed)
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
		if process.closed {
			closed++
		}
	}
	assert.Equal(t, 3, service.WorkerCount())
	assert.Equal(t, 7, closed)
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
				if err := sink.Emit(model.Event{ProtocolVersion: 1, Type: model.EventRunStarted, RunID: plan.RunID}); err != nil {
					return err
				}
				processChanged = true
				if err := os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tvar changed: int = 1\n"), 0o600); err != nil {
					return err
				}
				return sink.Emit(model.Event{ProtocolVersion: 1, Type: model.EventRunFinished, RunID: plan.RunID})
			}
		}
		processes = append(processes, process)
		return process, nil
	})
	defer func() { _ = service.Close(context.Background()) }()

	request := RunRequest{
		SessionID: "generation-session", Project: root, Godot: "godot",
		Plan: model.TestPlan{ProtocolVersion: 1, RunID: "changing-run", Generation: 1},
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
	require.NoError(t, err)
	require.Len(t, processes, 1)
	assert.Equal(t, uint64(2), processes[0].Generation())
	assert.False(t, processes[0].closed)
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
	assert.True(t, processes[0].closed)
	assert.Equal(t, uint64(2), processes[1].Generation())
}

func TestSessionServicePeriodicReconcileInvalidatesObsoleteWork(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	script := filepath.Join(root, "example.test.gd")
	require.NoError(t, os.WriteFile(script, []byte("extends GDTest\nfunc test_value() -> void:\n\tpass\n"), 0o600))
	process := &fakeWorker{pid: 450, generation: 1}
	service := newSessionService(func(context.Context, worker.ProcessOptions) (managedWorker, error) {
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
		Session: "periodic-session", Generation: 1, Priority: scheduler.Background,
		Run: func(context.Context, scheduler.Lease) error { return nil },
	})
	result, err := future.Wait(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, result.Err, scheduler.ErrObsoleteGeneration)

	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "warm-current", Method: "session.warm", Payload: payload,
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), process.Generation())
	assert.False(t, process.closed)
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
	assert.True(t, process.closed)
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
		Plan: model.TestPlan{ProtocolVersion: 1, RunID: "run", Generation: 1},
	})
	require.NoError(t, err)
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "run", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.NoError(t, err)
	assert.True(t, process.closed)
	assert.Zero(t, service.WorkerCount())
}
