package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/worker"
)

type fakeWorker struct {
	pid        int
	generation uint64
	runs       int
	resets     int
	closed     bool
}

func (f *fakeWorker) Run(_ context.Context, plan model.TestPlan, sink executor.EventSink) error {
	f.runs++
	return sink.Emit(model.Event{ProtocolVersion: 1, Type: model.EventRunFinished, RunID: plan.RunID})
}
func (f *fakeWorker) Ping(context.Context) error     { return nil }
func (f *fakeWorker) Reset(context.Context) error    { f.resets++; return nil }
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
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		events := 0
		_, err = service.Handle(context.Background(), daemon.Request{
			ID: "request", Method: "session.run", Payload: payload,
		}, func(any) error { events++; return nil })
		if err != nil {
			t.Fatalf("Handle run %d: %v", index, err)
		}
		if events != 1 {
			t.Fatalf("events = %d, want 1", events)
		}
	}
	if created != 1 || process.runs != 2 || process.resets != 2 {
		t.Fatalf("created=%d runs=%d resets=%d", created, process.runs, process.resets)
	}
	if sessions := service.List(); len(sessions) != 1 || sessions[0].Runs != 2 {
		t.Fatalf("sessions = %#v", sessions)
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
		if err != nil {
			t.Fatalf("marshal warm: %v", err)
		}
		if _, err := service.Handle(context.Background(), daemon.Request{
			ID: "warm", Method: "session.warm", Payload: payload,
		}, nil); err != nil {
			t.Fatalf("warm generation %d: %v", generation, err)
		}
	}
	if len(processes) != 2 || !processes[0].closed || processes[1].closed {
		t.Fatalf("process lifecycle = %#v", processes)
	}
}

func TestSessionServiceBoundsRetainedWorkers(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	service := newSessionService(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 100 + len(processes), generation: options.Generation}
		processes = append(processes, process)
		return process, nil
	})
	service.maxWorkers = 3
	for index := range 10 {
		payload, err := json.Marshal(WarmRequest{
			SessionID: fmt.Sprintf("session-%02d", index),
			Project:   fmt.Sprintf("/worktree-%02d", index), Godot: "godot", Generation: 1,
		})
		if err != nil {
			t.Fatalf("marshal warm: %v", err)
		}
		if _, err := service.Handle(context.Background(), daemon.Request{
			ID: "warm", Method: "session.warm", Payload: payload,
		}, nil); err != nil {
			t.Fatalf("warm %d: %v", index, err)
		}
		if got := service.WorkerCount(); got > 3 {
			t.Fatalf("worker count = %d, exceeded 3", got)
		}
	}
	closed := 0
	for _, process := range processes {
		if process.closed {
			closed++
		}
	}
	if service.WorkerCount() != 3 || closed != 7 {
		t.Fatalf("workers=%d closed=%d, want 3/7", service.WorkerCount(), closed)
	}
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
	if err != nil {
		t.Fatalf("marshal warm: %v", err)
	}
	if _, err := service.Handle(context.Background(), daemon.Request{
		ID: "warm", Method: "session.warm", Payload: payload,
	}, nil); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	now := time.Now().UTC()
	removed, err := service.Reconcile(now, time.Minute, time.Hour)
	if err != nil || len(removed) != 0 {
		t.Fatalf("first reconcile = %#v, %v", removed, err)
	}
	removed, err = service.Reconcile(now.Add(2*time.Minute), time.Minute, time.Hour)
	if err != nil || removed["deleted-worktree"] != projectidentity.RemoveMissing {
		t.Fatalf("expired reconcile = %#v, %v", removed, err)
	}
	if !process.closed || service.WorkerCount() != 0 {
		t.Fatalf("closed=%t workers=%d", process.closed, service.WorkerCount())
	}
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
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	if _, err := service.Handle(context.Background(), daemon.Request{
		ID: "run", Method: "session.run", Payload: payload,
	}, func(any) error { return nil }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !process.closed || service.WorkerCount() != 0 {
		t.Fatalf("closed=%t workers=%d", process.closed, service.WorkerCount())
	}
}
