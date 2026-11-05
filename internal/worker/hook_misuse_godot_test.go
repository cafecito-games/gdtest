package worker_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotHookAndCaseMisuseReportsSourceLocatedFrameworkErrorsInOrder(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot hook misuse worker test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	hookFile := parseFixture(t, root, "fixtures/godot/api/hook_misuse.test.gd")
	rootBeforeSuiteFile := parseFixture(t, root, "fixtures/godot/api/root_before_suite_misuse.test.gd")
	passingFile := parseFixture(t, root, "fixtures/godot/basic/passing.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "hook-misuse", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)
	initialPID := process.PID()

	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "hook-misuse-discover",
		ContentFingerprint: "hook-misuse",
		Generation:         1,
		Files:              []model.FilePlan{hookFile, rootBeforeSuiteFile},
	})
	require.NoError(t, err)
	assert.Empty(t, manifest.Errors)
	require.Len(t, manifest.Files, 2)
	require.Len(t, manifest.Files[0].Cases, 6)
	require.Len(t, manifest.Files[1].Cases, 2)

	require.NoError(t, process.Reset(ctx))

	var timeline []string
	var workerErrors []model.Event
	var caseFailures []model.Event
	caseStatuses := make(map[string]string)
	var runFinished model.Event
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "hook-misuse-run",
		Generation:      1,
		Files:           []model.FilePlan{hookFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerError:
			workerErrors = append(workerErrors, event)
			timeline = append(timeline, "worker_error:"+event.Message)
		case model.EventAssertionFail:
			caseFailures = append(caseFailures, event)
		case model.EventCaseFinished:
			caseStatuses[event.Name] = event.Status
			timeline = append(timeline, "case_finished:"+event.Name+":"+event.Status)
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, "error", runFinished.Status)
	assert.Equal(t, map[string]string{
		"healthy passing":              "pass",
		"registration inside case":     "error",
		"before_case error skips body": "error",
		"Root passing":                 "pass",
	}, caseStatuses)

	expectedHookErrors := []struct {
		message string
		snippet string
	}{
		{message: "expect is only valid while a case is executing", snippet: `expect_true(true, "blocked before_suite assertion")`},
		{message: "sleep duration must be finite and non-negative", snippet: "await sleep(-1.0)"},
		{message: "watch_signal is only valid while a case is executing", snippet: "watch_signal(sample_emitted)"},
		{message: "double_context is only valid while a case is executing", snippet: "double_context()"},
		{message: "message is only valid while a case is executing", snippet: `message("blocked after_suite message")`},
		{message: "skip is only valid while a case is executing", snippet: `skip("blocked after_suite skip")`},
		{message: "ok is only valid while a case is executing", snippet: `ok("healthy after_suite ok")`},
		{message: "define is only valid during suite registration", snippet: `define("late root suite"`},
		{message: "fail is only valid while a case is executing", snippet: `fail("after_all explicit fail")`},
		{message: "todo is only valid while a case is executing", snippet: `todo("after_all todo")`},
	}
	require.Len(t, workerErrors, len(expectedHookErrors))
	for index, expected := range expectedHookErrors {
		actual := workerErrors[index]
		assert.Equal(t, "worker.framework", actual.Name)
		assert.Equal(t, "framework", actual.Assertion)
		assert.Equal(t, expected.message, actual.Message)
		require.NotNil(t, actual.Location)
		assert.Equal(t, hookFile.Path, actual.Location.File)
		assert.False(t, strings.HasPrefix(actual.Location.File, "res://"))
		assert.Equal(
			t,
			lineContaining(t, root, hookFile.Path, expected.snippet),
			actual.Location.Line,
			"line for %s", expected.message,
		)
	}

	assert.Equal(t, []string{
		"worker_error:expect is only valid while a case is executing",
		"worker_error:sleep duration must be finite and non-negative",
		"worker_error:watch_signal is only valid while a case is executing",
		"worker_error:double_context is only valid while a case is executing",
		"worker_error:message is only valid while a case is executing",
		"worker_error:skip is only valid while a case is executing",
		"case_finished:healthy passing:pass",
		"case_finished:registration inside case:error",
		"case_finished:before_case error skips body:error",
		"worker_error:ok is only valid while a case is executing",
		"case_finished:Root passing:pass",
		"worker_error:define is only valid during suite registration",
		"worker_error:fail is only valid while a case is executing",
		"worker_error:todo is only valid while a case is executing",
	}, timeline)

	require.Len(t, caseFailures, 3)
	assert.Equal(t, "framework", caseFailures[0].Assertion)
	assert.Equal(t, "define is only valid during suite registration", caseFailures[0].Message)
	require.NotNil(t, caseFailures[0].Location)
	assert.Equal(t, hookFile.Path, caseFailures[0].Location.File)
	assert.Equal(t, lineContaining(t, root, hookFile.Path, `define("illegal suite"`), caseFailures[0].Location.Line)

	assert.Equal(t, "framework", caseFailures[1].Assertion)
	assert.Equal(t, "test is only valid during suite registration", caseFailures[1].Message)
	require.NotNil(t, caseFailures[1].Location)
	assert.Equal(t, hookFile.Path, caseFailures[1].Location.File)
	assert.Equal(t, lineContaining(t, root, hookFile.Path, `test("illegal case"`), caseFailures[1].Location.Line)

	assert.Equal(t, "framework", caseFailures[2].Assertion)
	assert.Equal(t, "sleep duration must be finite and non-negative", caseFailures[2].Message)
	require.NotNil(t, caseFailures[2].Location)
	assert.Equal(t, hookFile.Path, caseFailures[2].Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, hookFile.Path, "await sleep(-2.0)"),
		caseFailures[2].Location.Line,
	)

	require.NoError(t, process.Reset(ctx))

	var rootSuiteErrors []model.Event
	rootCasesExecuted := 0
	var rootRunFinished model.Event
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "root-before-suite-misuse-run",
		Generation:      1,
		Files:           []model.FilePlan{rootBeforeSuiteFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerError:
			rootSuiteErrors = append(rootSuiteErrors, event)
		case model.EventCaseStarted, model.EventCaseFinished:
			rootCasesExecuted++
		case model.EventRunFinished:
			rootRunFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Zero(t, rootCasesExecuted, "root before_suite error must prevent all file cases from running")
	assert.Equal(t, "error", rootRunFinished.Status)
	require.Len(t, rootSuiteErrors, 2)
	assert.Equal(t, "ok is only valid while a case is executing", rootSuiteErrors[0].Message)
	assert.Equal(
		t,
		lineContaining(t, root, rootBeforeSuiteFile.Path, `ok("root before_suite ok")`),
		rootSuiteErrors[0].Location.Line,
	)
	assert.Equal(t, "todo is only valid while a case is executing", rootSuiteErrors[1].Message)
	assert.Equal(
		t,
		lineContaining(t, root, rootBeforeSuiteFile.Path, `todo("root before_suite cleanup verified")`),
		rootSuiteErrors[1].Location.Line,
	)

	require.NoError(t, process.Reset(ctx))
	require.NoError(t, process.Ping(ctx))

	passed := 0
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "hook-misuse-reuse",
		Generation:      1,
		Files:           []model.FilePlan{passingFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished && event.Status == "pass" {
			passed++
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, 2, passed)
	assert.Equal(t, initialPID, process.PID())
}
