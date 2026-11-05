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

func TestGodotCallbackProbesRunConformanceAndCrossSourceOrderingOnPersistentWorker(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot callback probe worker test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	passingFile := parseFixture(t, root, "fixtures/godot/api/callback_probes.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "callback-probes", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "callback-probes",
		Generation:      1,
		Files:           []model.FilePlan{passingFile},
	}
	failures := 0
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail, model.EventWorkerError:
			failures++
		case model.EventCaseFinished:
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Zero(t, failures)
	require.Len(t, statuses, 11)
	for id, status := range statuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
}

func TestGodotCallbackProbeInvalidInputsReportFrameworkErrorsAtCallSites(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot callback probe failure test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_failures.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "callback-probe-failures", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	type expectation struct {
		snippet  string
		fragment string
	}
	expected := map[string]expectation{
		"test_negative_arity_reports_framework_error": {
			snippet:  "callback_probe(-1)",
			fragment: "between 0 and 8 arguments",
		},
		"test_arity_above_eight_reports_framework_error": {
			snippet:  "callback_probe(9)",
			fragment: "between 0 and 8 arguments",
		},
		"test_negative_count_reports_framework_error": {
			snippet:  "probe.wait_until_count(-1)",
			fragment: "count must be non-negative",
		},
		"test_negative_timeout_reports_framework_error": {
			snippet:  "probe.wait_until_count(2, -0.5)",
			fragment: "timeout must be finite and non-negative",
		},
		"test_infinite_timeout_reports_framework_error": {
			snippet:  "probe.wait_until_count(1, INF)",
			fragment: "timeout must be finite and non-negative",
		},
		"test_nan_timeout_reports_framework_error": {
			snippet:  "probe.wait_until_count(1, NAN)",
			fragment: "timeout must be finite and non-negative",
		},
		"test_negative_index_reports_framework_error": {
			snippet:  "probe.arguments_at(-1)",
			fragment: "outside the 1 recorded invocations",
		},
		"test_out_of_range_index_reports_framework_error": {
			snippet:  "expect_equal(probe.arguments_at(0), [])",
			fragment: "outside the 0 recorded invocations",
		},
		"test_closed_probe_invalid_index_still_reports_framework_error": {
			snippet:  "expect_equal(probe.arguments_at(2), [])",
			fragment: "outside the 0 recorded invocations",
		},
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "callback-probe-failures",
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
	for method, want := range expected {
		id := failureFile.Path + "::" + method
		assert.Equal(t, "error", statuses[id], "status for %s", method)
		diagnostic, exists := diagnostics[id]
		require.True(t, exists, "missing diagnostic for %s", method)
		assert.Equal(t, "framework", diagnostic.Assertion)
		assert.Contains(t, diagnostic.Message, want.fragment)
		require.NotNil(t, diagnostic.Location)
		assert.Equal(t, failureFile.Path, diagnostic.Location.File)
		assert.Equal(
			t,
			lineContaining(t, root, failureFile.Path, want.snippet),
			diagnostic.Location.Line,
			"line for %s", method,
		)
	}
}

func TestGodotCallbackProbeOutsideCaseReportsErrorAndReturnsInertProbe(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot callback probe outside-case test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	outsideFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_outside_case.test.gd")
	initializerFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_class_initializer.test.gd")
	hookFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_suite_hook.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "callback-probe-outside", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "callback-probe-outside-discover",
		ContentFingerprint: "callback-probe-outside",
		Generation:         1,
		Files:              []model.FilePlan{outsideFile, initializerFile},
	})
	require.NoError(t, err)
	require.Len(t, manifest.Errors, 2)

	assert.Equal(t, "worker.discovery", manifest.Errors[0].Code)
	assert.Equal(t, "callback_probe is only valid while a case is executing", manifest.Errors[0].Message)
	require.NotNil(t, manifest.Errors[0].Location)
	assert.Equal(t, outsideFile.Path, manifest.Errors[0].Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, outsideFile.Path, "callback_probe(1)"),
		manifest.Errors[0].Location.Line,
	)

	assert.Equal(t, "worker.discovery", manifest.Errors[1].Code)
	assert.Equal(t, "callback_probe is only valid while a case is executing", manifest.Errors[1].Message)
	require.NotNil(t, manifest.Errors[1].Location)
	assert.Equal(t, initializerFile.Path, manifest.Errors[1].Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, initializerFile.Path, "callback_probe(2)"),
		manifest.Errors[1].Location.Line,
	)

	require.NoError(t, process.Reset(ctx))

	var workerErrors []model.Event
	var runFinished model.Event
	var hookCaseStatus string
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "callback-probe-suite-hook-run",
		Generation:      1,
		Files:           []model.FilePlan{hookFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerError:
			workerErrors = append(workerErrors, event)
		case model.EventCaseFinished:
			hookCaseStatus = event.Status
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, "pass", hookCaseStatus)
	assert.Equal(t, "error", runFinished.Status)
	require.Len(t, workerErrors, 1)
	assert.Equal(t, "callback_probe is only valid while a case is executing", workerErrors[0].Message)
	require.NotNil(t, workerErrors[0].Location)
	assert.Equal(t, hookFile.Path, workerErrors[0].Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, hookFile.Path, "callback_probe(1)"),
		workerErrors[0].Location.Line,
	)
}

func TestGodotCallbackProbeRetainedCallableAcrossCasesAndRunsIsBoundedNoOp(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot callback probe leak test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	leakFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_leak.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "callback-probe-leak", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)
	initialPID := process.PID()

	runSelected := func(runID string, methods ...string) (map[string]string, []model.Event, model.Event) {
		selectedIDs := make([]string, 0, len(methods))
		for _, method := range methods {
			selectedIDs = append(selectedIDs, leakFile.Path+"::"+method)
		}
		var diagnostics []model.Event
		var finished model.Event
		statuses := make(map[string]string)
		runErr := process.Run(ctx, model.TestPlan{
			ProtocolVersion:  model.ProtocolVersion,
			RunID:            runID,
			Generation:       1,
			SelectionApplied: true,
			SelectedIDs:      selectedIDs,
			Files:            []model.FilePlan{leakFile},
		}, executor.EventSinkFunc(func(event model.Event) error {
			switch event.Type {
			case model.EventAssertionFail, model.EventWorkerError, model.EventOutput:
				diagnostics = append(diagnostics, event)
			case model.EventCaseFinished:
				statuses[event.TestID] = event.Status
			case model.EventRunFinished:
				finished = event
			}
			return nil
		}))
		require.NoError(t, runErr)
		return statuses, diagnostics, finished
	}

	firstStatuses, firstDiagnostics, firstFinished := runSelected(
		"callback-probe-leak-run-1",
		"test_a_registers_callback_on_persistent_root",
		"test_b_late_invocations_in_later_case_are_bounded_no_ops",
	)
	assert.Empty(t, firstDiagnostics)
	require.Len(t, firstStatuses, 2)
	for id, status := range firstStatuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
	assert.Equal(t, "pass", firstFinished.Status)
	assert.Equal(t, float64(1024), firstFinished.Metadata["late_callback_invocations"])

	require.NoError(t, process.Reset(ctx))
	assert.Equal(t, initialPID, process.PID())

	secondStatuses, secondDiagnostics, secondFinished := runSelected(
		"callback-probe-leak-run-2",
		"test_c_second_run_observes_freed_prior_suite_and_bounded_late_calls",
	)
	assert.Empty(t, secondDiagnostics)
	require.Len(t, secondStatuses, 1)
	for id, status := range secondStatuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
	assert.Equal(t, "pass", secondFinished.Status)
	assert.Equal(t, float64(1024), secondFinished.Metadata["late_callback_invocations"])

	require.NoError(t, process.Reset(ctx))

	thirdStatuses, thirdDiagnostics, thirdFinished := runSelected(
		"callback-probe-leak-run-3",
		"test_d_clean_run_omits_late_callback_metadata",
	)
	assert.Empty(t, thirdDiagnostics)
	require.Len(t, thirdStatuses, 1)
	for id, status := range thirdStatuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
	assert.Equal(t, "pass", thirdFinished.Status)
	_, hasLateMetadata := thirdFinished.Metadata["late_callback_invocations"]
	assert.False(t, hasLateMetadata)
}

func TestGodotCallbackProbeUnboundedWaitHonorsExternalCaseTimeout(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot callback probe timeout test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	timeoutFile := parseFixture(t, root, "fixtures/godot/api/callback_probe_timeout.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "callback-probe-timeout", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	var caseFinished model.Event
	var runFinished model.Event
	tainted := false
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "callback-probe-timeout",
		Generation:      1,
		CaseTimeout:     50 * time.Millisecond,
		Files:           []model.FilePlan{timeoutFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerTainted:
			tainted = true
		case model.EventCaseFinished:
			caseFinished = event
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.True(t, tainted)
	assert.Equal(t, "error", caseFinished.Status)
	assert.Contains(t, caseFinished.Message, "case timed out")
	assert.Equal(t, "error", runFinished.Status)
}
