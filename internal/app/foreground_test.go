package app

import (
	"context"
	"errors"
	"testing"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type foregroundFake struct {
	ranFiles []string
	resets   int
	closed   bool
	taintOn  string
}

func (f *foregroundFake) Run(_ context.Context, plan model.TestPlan, sink executor.EventSink) error {
	if err := sink.Emit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted, RunID: plan.RunID,
	}); err != nil {
		return err
	}
	tainted := false
	for _, file := range plan.Files {
		f.ranFiles = append(f.ranFiles, file.Path)
		status := "pass"
		if file.Path == f.taintOn {
			status = "error"
		}
		if err := sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion, Type: model.EventCaseFinished,
			RunID: plan.RunID, TestID: file.Path + "::test_case", Status: status,
		}); err != nil {
			return err
		}
		if file.Path != f.taintOn {
			continue
		}
		tainted = true
		if err := sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion, Type: model.EventWorkerTainted,
			RunID: plan.RunID, Message: "case timeout",
			Metadata: map[string]any{"reason": "case_timeout"},
		}); err != nil {
			return err
		}
		break
	}
	status := "pass"
	if tainted {
		status = "error"
	}
	return sink.Emit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
		RunID: plan.RunID, Status: status, Generation: plan.Generation,
	})
}

func (f *foregroundFake) Reset(context.Context) error {
	if f.closed {
		return errors.New("worker is closed")
	}
	f.resets++
	return nil
}

func (f *foregroundFake) Shutdown(context.Context) error       { f.closed = true; return nil }
func (f *foregroundFake) TakeStartupTimings() map[string]int64 { return nil }

// A case timeout taints its worker, and the foreground runner used to hand the
// whole plan to a single worker. Losing that worker therefore dropped every
// file after the timeout in plan order, with no event and no count to say so.
func TestForegroundRunReplacesTaintedWorkerAndFinishesThePlan(t *testing.T) {
	t.Parallel()

	var workers []*foregroundFake
	factory := func(context.Context) (foregroundWorker, error) {
		process := &foregroundFake{taintOn: "b.test.gd"}
		workers = append(workers, process)
		return process, nil
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "foreground", Generation: 7,
		Files: []model.FilePlan{
			{Path: "a.test.gd"}, {Path: "b.test.gd"},
			{Path: "c.test.gd"}, {Path: "d.test.gd"},
		},
	}
	var events []model.Event
	_, err := runPlanInForeground(context.Background(), factory, plan, executor.EventSinkFunc(
		func(event model.Event) error {
			events = append(events, event)
			return nil
		},
	))
	require.NoError(t, err)

	executed := make([]string, 0, 4)
	for _, process := range workers {
		executed = append(executed, process.ranFiles...)
	}
	assert.Equal(
		t,
		[]string{"a.test.gd", "b.test.gd", "c.test.gd", "d.test.gd"},
		executed,
		"the files queued behind a tainting file execute on a replacement worker",
	)
	require.Len(t, workers, 2, "exactly one replacement worker is started")
	assert.Equal(t, []string{"a.test.gd", "b.test.gd"}, workers[0].ranFiles)
	assert.True(t, workers[0].closed, "a tainted worker is retired, never reset and reused")

	var started, finished int
	var finalStatus string
	for _, event := range events {
		switch event.Type {
		case model.EventRunStarted:
			started++
		case model.EventRunFinished:
			finished++
			finalStatus = event.Status
		}
	}
	assert.Equal(t, 1, started, "one run emits exactly one run_started")
	assert.Equal(t, 1, finished, "one run emits exactly one run_finished")
	assert.Equal(t, "error", finalStatus, "the timed-out case keeps the run's error status")
}
