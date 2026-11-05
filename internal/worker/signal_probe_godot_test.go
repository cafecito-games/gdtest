package worker_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotSignalProbeInvalidInputsReportFrameworkErrorsAtCallSites(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot signal probe failure test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/signal_probe_failures.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "signal-probe-failures", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	expected := map[string]string{
		"test_null_signal_reports_framework_error":        "requires a valid signal",
		"test_freed_owner_reports_framework_error":        "still valid",
		"test_arity_above_eight_reports_framework_error":  "at most 8 arguments",
		"test_negative_count_reports_framework_error":     "count must be non-negative",
		"test_negative_timeout_reports_framework_error":   "timeout must be finite and non-negative",
		"test_out_of_range_index_reports_framework_error": "outside the 0 recorded emissions",
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "signal-probe-failures",
		Generation:      1,
		Files:           []model.FilePlan{failureFile},
	}
	diagnostics := make(map[string]model.Event)
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			diagnostics[event.TestID] = event
		case model.EventCaseFinished:
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, statuses, len(expected))
	for method, fragment := range expected {
		id := failureFile.Path + "::" + method
		assert.Equal(t, "error", statuses[id], "status for %s", method)
		diagnostic, exists := diagnostics[id]
		require.True(t, exists, "missing diagnostic for %s", method)
		assert.Equal(t, "framework", diagnostic.Assertion)
		assert.Contains(t, diagnostic.Message, fragment)
		require.NotNil(t, diagnostic.Location)
		assert.Equal(t, failureFile.Path, diagnostic.Location.File)
		assert.Positive(t, diagnostic.Location.Line)
	}
}

func TestGodotSignalProbesLeaveNoConnectionsForLaterCasesOnPersistentWorker(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot signal probe worker test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	passingFile := parseFixture(t, root, "fixtures/godot/api/signal_probes.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "signal-probes", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "signal-probes",
		Generation:      1,
		Files:           []model.FilePlan{passingFile},
	}
	failures := 0
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			failures++
		case model.EventCaseFinished:
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Zero(t, failures)
	require.Len(t, statuses, 14)
	for id, status := range statuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
}
