package reporter

import (
	"bytes"
	"encoding/json"
	"strings"
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

func TestTerminalPrintsCustomMatcherFailures(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewTerminal(&output)

	// Failure with expected value
	require.NoError(t, reporter.Emit(model.Event{
		Type:      model.EventAssertionFail,
		TestID:    "tests/matcher.test.gd::test_case",
		Assertion: "matches_order",
		Message:   "item differs",
		Location:  &model.Location{File: "tests/matcher.test.gd", Line: 15},
		Expected:  &model.WireValue{Type: "string", Value: "apple"},
		Actual:    &model.WireValue{Type: "string", Value: "orange"},
	}))

	// Failure without expected value
	require.NoError(t, reporter.Emit(model.Event{
		Type:      model.EventAssertionFail,
		TestID:    "tests/matcher.test.gd::test_case",
		Assertion: "is_valid",
		Message:   "value is not valid",
		Location:  &model.Location{File: "tests/matcher.test.gd", Line: 20},
		Actual:    &model.WireValue{Type: "string", Value: "bad"},
	}))

	// Failure with expected = null
	require.NoError(t, reporter.Emit(model.Event{
		Type:      model.EventAssertionFail,
		TestID:    "tests/matcher.test.gd::test_case",
		Assertion: "matches_cleared",
		Message:   "expected null",
		Location:  &model.Location{File: "tests/matcher.test.gd", Line: 25},
		Expected:  &model.WireValue{Type: "null", Value: nil},
		Actual:    &model.WireValue{Type: "string", Value: "lingering"},
	}))

	// Caller-prefixed message
	require.NoError(t, reporter.Emit(model.Event{
		Type:      model.EventAssertionFail,
		TestID:    "tests/matcher.test.gd::test_case",
		Assertion: "matches_threshold",
		Message:   "stock check: count below minimum",
		Location:  &model.Location{File: "tests/matcher.test.gd", Line: 30},
		Expected:  &model.WireValue{Type: "int", Value: "10"},
		Actual:    &model.WireValue{Type: "int", Value: "3"},
	}))

	text := output.String()
	assert.Contains(t, text, "FAIL tests/matcher.test.gd::test_case (tests/matcher.test.gd:15): item differs (expected=\"apple\", actual=\"orange\")")
	assert.Contains(t, text, "FAIL tests/matcher.test.gd::test_case (tests/matcher.test.gd:20): value is not valid (actual=\"bad\")")
	assert.NotContains(t, text, "value is not valid (expected=")
	assert.Contains(t, text, "FAIL tests/matcher.test.gd::test_case (tests/matcher.test.gd:25): expected null (expected=null, actual=\"lingering\")")
	assert.Contains(t, text, "FAIL tests/matcher.test.gd::test_case (tests/matcher.test.gd:30): stock check: count below minimum (expected=\"10\", actual=\"3\")")
}

func TestJSONLPrintsCustomMatcherFailures(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := NewJSONL(&output)

	events := []model.Event{
		{
			Type:      model.EventAssertionFail,
			TestID:    "tests/custom.test.gd::test_matcher",
			Assertion: "matches_order",
			Message:   "custom label: item mismatch",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 42},
			Actual:    &model.WireValue{Type: "string", Value: "banana"},
			Expected:  &model.WireValue{Type: "string", Value: "apple"},
		},
		{
			Type:      model.EventAssertionFail,
			TestID:    "tests/custom.test.gd::test_matcher",
			Assertion: "is_empty_state",
			Message:   "state should be cleared",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 48},
			Actual:    &model.WireValue{Type: "string", Value: "full"},
			Expected:  &model.WireValue{Type: "null", Value: nil},
		},
		{
			Type:      model.EventAssertionFail,
			TestID:    "tests/custom.test.gd::test_matcher",
			Assertion: "predicate_check",
			Message:   "predicate failed",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 54},
			Actual:    &model.WireValue{Type: "int", Value: "99"},
			Expected:  nil,
		},
	}

	for _, event := range events {
		require.NoError(t, reporter.Emit(event))
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 3)

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, "assertion_failed", first["type"])
	assert.Equal(t, "matches_order", first["assertion"])
	assert.Equal(t, "custom label: item mismatch", first["message"])
	assert.NotNil(t, first["expected"])
	assert.NotNil(t, first["actual"])

	var second map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
	assert.Equal(t, "is_empty_state", second["assertion"])
	expectedValue := second["expected"].(map[string]any)
	assert.Equal(t, "null", expectedValue["type"])
	assert.Nil(t, expectedValue["value"])

	var third map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &third))
	assert.Equal(t, "predicate_check", third["assertion"])
	assert.Nil(t, third["expected"])
}

func TestTerminalRendersCaseWarningsAndSuiteLeakFailures(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	terminal := NewTerminal(&output)
	require.NoError(t, terminal.Emit(model.Event{
		Type:      model.EventCaseWarning,
		TestID:    "tests/hud.test.gd::test_toast",
		Assertion: "leak",
		Message:   `leaked 1 node: stray Control "/root/Toast"`,
		Location:  &model.Location{File: "tests/hud.test.gd", Line: 12},
	}))
	require.NoError(t, terminal.Emit(model.Event{
		Type:     model.EventCaseFinished,
		TestID:   "tests/hud.test.gd::test_toast",
		Status:   "pass",
		Messages: []string{"quiet on pass"},
	}))
	require.NoError(t, terminal.Emit(model.Event{
		Type:      model.EventAssertionFail,
		Assertion: "leak",
		Message:   "`before_suite`/`after_suite` of suite `Inventory` leaked 1 node: orphan Node \"Slot\"",
		Location:  &model.Location{File: "tests/inventory.test.gd", Line: 4},
	}))

	assert.Equal(
		t,
		"WARN tests/hud.test.gd::test_toast (tests/hud.test.gd:12): leaked 1 node: stray Control \"/root/Toast\"\n"+
			"FAIL (tests/inventory.test.gd:4): `before_suite`/`after_suite` of suite `Inventory` leaked 1 node: orphan Node \"Slot\"\n",
		output.String(),
	)
}

func TestJSONLStreamsCaseWarningAndLeakFailureMetadata(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	machine := NewJSONL(&output)
	require.NoError(t, machine.Emit(model.Event{
		ProtocolVersion: model.ProtocolVersion,
		Type:            model.EventCaseWarning,
		RunID:           "run-1",
		TestID:          "tests/hud.test.gd::test_toast",
		Assertion:       "leak",
		Message:         `leaked 21 nodes: orphan Node "N0", and 1 more`,
		Location:        &model.Location{File: "tests/hud.test.gd", Line: 12},
		Metadata: map[string]any{
			"leaks": []map[string]any{{
				"kind":        "orphan",
				"class":       "Node",
				"name":        "N0",
				"script":      "",
				"path":        "",
				"descendants": 0,
			}},
			"omitted": 1,
		},
	}))

	assert.JSONEq(t, `{
		"protocol_version": 5,
		"type": "case_warning",
		"run_id": "run-1",
		"test_id": "tests/hud.test.gd::test_toast",
		"assertion": "leak",
		"message": "leaked 21 nodes: orphan Node \"N0\", and 1 more",
		"location": {"file": "tests/hud.test.gd", "line": 12},
		"metadata": {
			"leaks": [{
				"kind": "orphan",
				"class": "Node",
				"name": "N0",
				"script": "",
				"path": "",
				"descendants": 0
			}],
			"omitted": 1
		}
	}`, output.String())
}
