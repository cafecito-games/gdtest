package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
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
		Project: "/project", Godot: "godot",
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
		payload, err := json.Marshal(WarmRequest{Project: "/project", Godot: "godot", Generation: generation})
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
