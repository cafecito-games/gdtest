package reporter

import (
	"bytes"
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalPrintsStructuredFailureMessagesAndBufferedOutput(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewTerminal(&output)
	require.NoError(t, reporter.Emit(model.Event{
		Type: model.EventOutput, TestID: "tests/value.test.gd::test_value", Message: "engine line",
	}))
	require.NoError(t, reporter.Emit(model.Event{
		Type: model.EventAssertionFail, TestID: "tests/value.test.gd::test_value",
		Message: "values differ", Location: &model.Location{File: "tests/value.test.gd", Line: 8},
		Expected: &model.WireValue{Type: "int", Value: "2"},
		Actual:   &model.WireValue{Type: "int", Value: "1"},
	}))
	require.NoError(t, reporter.Emit(model.Event{
		Type: model.EventCaseFinished, TestID: "tests/value.test.gd::test_value", Status: "fail",
		Messages: []string{"context", ""},
	}))

	text := output.String()
	assert.Contains(t, text, "tests/value.test.gd:8")
	assert.Contains(t, text, `expected="2"`)
	assert.Contains(t, text, `actual="1"`)
	assert.Contains(t, text, "MESSAGE tests/value.test.gd::test_value: context")
	assert.NotContains(t, text, "MESSAGE tests/value.test.gd::test_value: \n")
	assert.Contains(t, text, "OUTPUT tests/value.test.gd::test_value: engine line")
}

func TestTerminalVerbosePrintsPassingMessagesAndCanceledSummary(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewTerminalWithOptions(&output, true)
	require.NoError(t, reporter.Emit(model.Event{
		Type: model.EventCaseFinished, TestID: "case", Status: "pass", Messages: []string{"detail"},
	}))
	require.NoError(t, reporter.Summary(results.Summary{Passed: 1, Canceled: 2}))

	assert.Contains(t, output.String(), "MESSAGE case: detail")
	assert.Contains(t, output.String(), "1 passed, 0 failed, 0 errors, 0 skipped, 0 todo, 0 void, 2 canceled")
}

func TestTerminalSummaryReportsStrictOutcomePolicyFailures(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewTerminal(&output)
	require.NoError(t, reporter.Summary(results.Summary{
		Skipped: 2,
		Todo:    1,
		PolicyFailures: []results.PolicyFailure{
			{Status: "skip", Count: 2},
			{Status: "todo", Count: 1},
		},
	}))

	assert.Contains(t, output.String(), "0 passed, 0 failed, 0 errors, 2 skipped, 1 todo, 0 void, 0 canceled")
	assert.Contains(t, output.String(), "strict policy failed: 2 skip, 1 todo")
}

func TestJSONLSummaryIncludesStrictOutcomePolicyFailures(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewJSONL(&output)
	require.NoError(t, reporter.Summary(results.Summary{
		Void:           2,
		PolicyFailures: []results.PolicyFailure{{Status: "void", Count: 2}},
	}))

	assert.JSONEq(t, `{
		"type": "summary",
		"summary": {
			"cases": null,
			"passed": 0,
			"failed": 0,
			"skipped": 0,
			"todo": 0,
			"void": 2,
			"errored": 0,
			"canceled": 0,
			"policy_failures": [{"status": "void", "count": 2}]
		}
	}`, output.String())
}

func TestTerminalWorkerErrorPrintsSourceLocationWhenAvailable(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewTerminal(&output)
	require.NoError(t, reporter.Emit(model.Event{
		Type:     model.EventWorkerError,
		Name:     "worker.framework",
		Message:  "expect is only valid while a case is executing",
		Location: &model.Location{File: "tests/hooks.test.gd", Line: 14},
	}))
	require.NoError(t, reporter.Emit(model.Event{
		Type:     model.EventWorkerError,
		Name:     "worker.script_load",
		Message:  "unable to load test script",
		Location: &model.Location{File: "tests/broken.test.gd", Line: -1},
	}))
	require.NoError(t, reporter.Emit(model.Event{
		Type:    model.EventWorkerError,
		Name:    "worker.infrastructure",
		Message: "socket closed",
	}))

	assert.Equal(
		t,
		"ERROR worker.framework (tests/hooks.test.gd:14): expect is only valid while a case is executing\n"+
			"ERROR worker.script_load (tests/broken.test.gd): unable to load test script\n"+
			"ERROR worker.infrastructure: socket closed\n",
		output.String(),
	)
}

func TestJSONLSummaryIncludesFrameworkErrors(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewJSONL(&output)
	require.NoError(t, reporter.Summary(results.Summary{
		Cases:   []results.CaseResult{},
		Passed:  1,
		Errored: 1,
		Errors: []model.Event{{
			Type:      model.EventWorkerError,
			Name:      "worker.framework",
			Assertion: "framework",
			Message:   "expect is only valid while a case is executing",
			Location:  &model.Location{File: "tests/hooks.test.gd", Line: 14},
		}},
	}))

	assert.JSONEq(t, `{
		"type": "summary",
		"summary": {
			"cases": [],
			"errors": [{
				"protocol_version": 0,
				"type": "worker_error",
				"name": "worker.framework",
				"assertion": "framework",
				"message": "expect is only valid while a case is executing",
				"location": {"file": "tests/hooks.test.gd", "line": 14}
			}],
			"passed": 1,
			"failed": 0,
			"skipped": 0,
			"todo": 0,
			"void": 0,
			"errored": 1,
			"canceled": 0
		}
	}`, output.String())
}
