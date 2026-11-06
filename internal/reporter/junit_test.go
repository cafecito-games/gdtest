package reporter

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFormatAcceptsKnownFormatsAndRejectsUnknown(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateFormat(FormatTerminal))
	require.NoError(t, ValidateFormat(FormatJSONL))
	require.NoError(t, ValidateFormat(FormatJUnit))

	err := ValidateFormat("tap")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `report format "tap" must be terminal, jsonl, or junit`)
}

func TestJUnitEmptySummaryProducesValidEmptyDocument(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	junitReporter := NewJUnit(&output)
	require.NoError(t, junitReporter.Emit(model.Event{Type: model.EventRunStarted}))
	assert.Empty(t, output.String(), "JUnit must not stream partial output before Summary")

	require.NoError(t, junitReporter.Summary(results.Summary{}))
	assert.True(t, strings.HasPrefix(output.String(), xml.Header))

	var decoded junitTestSuites
	require.NoError(t, xml.Unmarshal(output.Bytes(), &decoded))
	assert.Equal(t, "gdtest", decoded.Name)
	assert.Equal(t, 0, decoded.Tests)
	assert.Equal(t, 0, decoded.Failures)
	assert.Equal(t, 0, decoded.Errors)
	assert.Equal(t, 0, decoded.Skipped)
	assert.Equal(t, "0.000000", decoded.Time)
	assert.Empty(t, decoded.Suites)
}

func TestJUnitMapsAllCaseOutcomesAndPreservesManifestSuiteOrder(t *testing.T) {
	t.Parallel()

	summary := results.Summary{
		Cases: []results.CaseResult{
			{
				ID:         "tests/zeta.test.gd::test_pass",
				Name:       "passes cleanly",
				Status:     "pass",
				File:       "tests/zeta.test.gd",
				Line:       5,
				Duration:   1500 * time.Microsecond,
				Assertions: 2,
				Messages:   []string{"helpful note"},
				Output:     []model.Event{{Type: model.EventOutput, Message: "printed line"}},
				Warnings: []model.Event{{
					Type:      model.EventCaseWarning,
					Assertion: "leak",
					Message:   `leaked 1 node: orphan Node "Enemy"`,
					Location:  &model.Location{File: "tests/zeta.test.gd", Line: 8},
				}},
			},
			{
				ID:     "tests/zeta.test.gd::test_void",
				Name:   "void is faithful pass",
				Status: "void",
				File:   "tests/zeta.test.gd",
				Line:   12,
			},
			{
				ID:     "tests/alpha.test.gd::test_skip",
				Name:   "skipped case",
				Status: "skip",
				File:   "tests/alpha.test.gd",
				Line:   4,
				Outcomes: []model.Outcome{
					{Kind: "skip", Message: "awaiting physics fixture"},
				},
			},
			{
				ID:     "tests/alpha.test.gd::test_todo",
				Name:   "todo case",
				Status: "todo",
				File:   "tests/alpha.test.gd",
				Line:   9,
				Outcomes: []model.Outcome{
					{Kind: "todo", Message: "implement boss phase"},
				},
			},
			{
				ID:         "tests/alpha.test.gd::test_fail",
				Name:       "failing case",
				Status:     "fail",
				File:       "tests/alpha.test.gd",
				Line:       15,
				Duration:   2500 * time.Microsecond,
				Assertions: 3,
				Messages:   []string{"extra context"},
				Output:     []model.Event{{Type: model.EventOutput, Metadata: map[string]any{"text": "debug trace"}}},
				Failures: []model.Event{
					{
						Type:      model.EventAssertionFail,
						Assertion: "equal",
						Message:   "values differ",
						Location:  &model.Location{File: "tests/alpha.test.gd", Line: 18},
						Expected:  &model.WireValue{Type: "int", Value: "2"},
						Actual:    &model.WireValue{Type: "int", Value: "1"},
					},
					{
						Type:      model.EventAssertionFail,
						Assertion: "true",
						Message:   "expected true",
						Location:  &model.Location{File: "tests/alpha.test.gd", Line: 19},
					},
				},
			},
			{
				ID:       "tests/alpha.test.gd::test_error",
				Name:     "errored case",
				Status:   "error",
				File:     "tests/alpha.test.gd",
				Line:     25,
				Message:  "case timed out after 5.000 seconds",
				Duration: 5 * time.Second,
			},
			{
				ID:     "tests/alpha.test.gd::test_canceled",
				Name:   "canceled case",
				Status: "canceled",
				File:   "tests/alpha.test.gd",
				Line:   32,
			},
		},
		Passed:   1,
		Void:     1,
		Skipped:  1,
		Todo:     1,
		Failed:   1,
		Errored:  1,
		Canceled: 1,
	}

	encoded, err := MarshalJUnit(summary)
	require.NoError(t, err)

	var decoded junitTestSuites
	require.NoError(t, xml.Unmarshal(encoded, &decoded))

	assert.Equal(t, 7, decoded.Tests)
	assert.Equal(t, 1, decoded.Failures)
	assert.Equal(t, 2, decoded.Errors)
	assert.Equal(t, 2, decoded.Skipped)
	assert.Equal(t, "5.004000", decoded.Time)

	require.Len(t, decoded.Suites, 2)
	assert.Equal(t, "tests/zeta.test.gd", decoded.Suites[0].Name)
	assert.Equal(t, "tests/zeta.test.gd", decoded.Suites[0].File)
	assert.Equal(t, 2, decoded.Suites[0].Tests)
	assert.Equal(t, 0, decoded.Suites[0].Failures)
	assert.Equal(t, "0.001500", decoded.Suites[0].Time)

	passingCase := decoded.Suites[0].TestCases[0]
	assert.Equal(t, "passes cleanly", passingCase.Name)
	assert.Equal(t, "tests/zeta.test.gd", passingCase.Classname)
	assert.Equal(t, 5, passingCase.Line)
	assert.Equal(t, 2, passingCase.Assertions)
	assert.Nil(t, passingCase.Failure)
	assert.Nil(t, passingCase.Error)
	assert.Nil(t, passingCase.Skipped)
	assert.Contains(t, passingCase.SystemOut, "MESSAGE: helpful note")
	assert.Contains(t, passingCase.SystemOut, "printed line")
	assert.Contains(t, passingCase.SystemErr, "WARN (tests/zeta.test.gd:8): leaked 1 node")

	voidCase := decoded.Suites[0].TestCases[1]
	assert.Equal(t, "void is faithful pass", voidCase.Name)
	assert.Nil(t, voidCase.Failure)
	assert.Nil(t, voidCase.Error)
	assert.Nil(t, voidCase.Skipped)

	alphaSuite := decoded.Suites[1]
	assert.Equal(t, "tests/alpha.test.gd", alphaSuite.Name)
	assert.Equal(t, 5, alphaSuite.Tests)
	assert.Equal(t, 1, alphaSuite.Failures)
	assert.Equal(t, 2, alphaSuite.Errors)
	assert.Equal(t, 2, alphaSuite.Skipped)
	assert.Equal(t, "5.002500", alphaSuite.Time)

	require.NotNil(t, alphaSuite.TestCases[0].Skipped)
	assert.Equal(t, "skip", alphaSuite.TestCases[0].Skipped.Type)
	assert.Equal(t, "awaiting physics fixture", alphaSuite.TestCases[0].Skipped.Message)

	require.NotNil(t, alphaSuite.TestCases[1].Skipped)
	assert.Equal(t, "todo", alphaSuite.TestCases[1].Skipped.Type)
	assert.Equal(t, "implement boss phase", alphaSuite.TestCases[1].Skipped.Message)

	failingCase := alphaSuite.TestCases[2]
	require.NotNil(t, failingCase.Failure)
	assert.Equal(t, "equal", failingCase.Failure.Type)
	assert.Equal(t, `values differ (expected="2", actual="1")`, failingCase.Failure.Message)
	assert.Contains(t, failingCase.Failure.Body, `tests/alpha.test.gd:18: [equal] values differ (expected="2", actual="1")`)
	assert.Contains(t, failingCase.Failure.Body, `tests/alpha.test.gd:19: [true] expected true`)
	assert.Contains(t, failingCase.Failure.Body, "message: extra context")
	assert.Equal(t, "debug trace", failingCase.SystemOut)

	erroredCase := alphaSuite.TestCases[3]
	require.NotNil(t, erroredCase.Error)
	assert.Equal(t, "error", erroredCase.Error.Type)
	assert.Equal(t, "case timed out after 5.000 seconds", erroredCase.Error.Message)
	assert.Contains(t, erroredCase.Error.Body, "tests/alpha.test.gd:25: case timed out after 5.000 seconds")

	canceledCase := alphaSuite.TestCases[4]
	require.NotNil(t, canceledCase.Error)
	assert.Equal(t, "canceled", canceledCase.Error.Type)
	assert.Equal(t, "case canceled", canceledCase.Error.Message)
}

func TestJUnitRepresentsStrictPolicyViolationWithoutMutatingCaseStatuses(t *testing.T) {
	t.Parallel()

	summary := results.Summary{
		Cases: []results.CaseResult{
			{
				ID:       "tests/policy.test.gd::test_skipped",
				Name:     "skipped",
				Status:   "skip",
				File:     "tests/policy.test.gd",
				Line:     3,
				Outcomes: []model.Outcome{{Kind: "skip", Message: "pending"}},
			},
			{
				ID:     "tests/policy.test.gd::test_void",
				Name:   "void",
				Status: "void",
				File:   "tests/policy.test.gd",
				Line:   8,
			},
		},
		Skipped: 1,
		Void:    1,
	}
	summary.ApplyOutcomePolicy(results.OutcomePolicy{FailOnSkip: true, FailOnVoid: true})

	encoded, err := MarshalJUnit(summary)
	require.NoError(t, err)

	var decoded junitTestSuites
	require.NoError(t, xml.Unmarshal(encoded, &decoded))

	assert.Equal(t, 3, decoded.Tests)
	assert.Equal(t, 1, decoded.Failures)
	assert.Equal(t, 0, decoded.Errors)
	assert.Equal(t, 1, decoded.Skipped)

	require.Len(t, decoded.Suites, 2)
	assert.NotNil(t, decoded.Suites[0].TestCases[0].Skipped)
	assert.Nil(t, decoded.Suites[0].TestCases[0].Failure)
	assert.Nil(t, decoded.Suites[0].TestCases[1].Failure)
	assert.Nil(t, decoded.Suites[0].TestCases[1].Skipped)

	policySuite := decoded.Suites[1]
	assert.Equal(t, "gdtest", policySuite.Name)
	assert.Empty(t, policySuite.File)
	require.Len(t, policySuite.TestCases, 1)
	assert.Equal(t, "strict_policy", policySuite.TestCases[0].Name)
	require.NotNil(t, policySuite.TestCases[0].Failure)
	assert.Equal(t, "strict_policy", policySuite.TestCases[0].Failure.Type)
	assert.Equal(t, "strict policy failed: 1 skip, 1 void", policySuite.TestCases[0].Failure.Message)
}

func TestJUnitRendersSuiteLeaksFrameworkErrorsAndInfrastructureDeterministically(t *testing.T) {
	t.Parallel()

	summary := results.Summary{
		Failures: []model.Event{
			{
				Type:      model.EventAssertionFail,
				Assertion: "leak",
				Message:   "suite leaked 1 node",
				Location:  &model.Location{File: "tests/zeta.test.gd", Line: 4},
			},
		},
		Errors: []model.Event{
			{
				Type:      model.EventWorkerError,
				Name:      "worker.framework",
				Assertion: "framework",
				Message:   "expect outside case",
				Location:  &model.Location{File: "tests/alpha.test.gd", Line: 10},
			},
		},
		Infrastructure: []model.Event{
			{
				Type:    model.EventWorkerError,
				Name:    "worker.infrastructure",
				Message: "socket closed",
			},
		},
		Warnings: []model.Event{
			{
				Type:      model.EventCaseWarning,
				Assertion: "leak",
				Message:   "before_all leaked 1 node",
				Location:  &model.Location{File: "tests/alpha.test.gd", Line: 2},
			},
		},
		Failed:  1,
		Errored: 2,
	}

	encoded, err := MarshalJUnit(summary)
	require.NoError(t, err)

	var decoded junitTestSuites
	require.NoError(t, xml.Unmarshal(encoded, &decoded))

	assert.Equal(t, 3, decoded.Tests)
	assert.Equal(t, 1, decoded.Failures)
	assert.Equal(t, 2, decoded.Errors)

	require.Len(t, decoded.Suites, 3)
	assert.Equal(t, "tests/alpha.test.gd", decoded.Suites[0].Name)
	assert.Contains(t, decoded.Suites[0].SystemErr, "WARN (tests/alpha.test.gd:2): before_all leaked 1 node")
	require.NotNil(t, decoded.Suites[0].TestCases[0].Error)
	assert.Equal(t, 10, decoded.Suites[0].TestCases[0].Line)

	assert.Equal(t, "tests/zeta.test.gd", decoded.Suites[1].Name)
	require.NotNil(t, decoded.Suites[1].TestCases[0].Failure)
	assert.Equal(t, "leak", decoded.Suites[1].TestCases[0].Failure.Type)

	assert.Equal(t, "gdtest", decoded.Suites[2].Name)
	assert.Empty(t, decoded.Suites[2].File)
	require.NotNil(t, decoded.Suites[2].TestCases[0].Error)
	assert.Equal(t, "worker.infrastructure", decoded.Suites[2].TestCases[0].Error.Type)
	assert.Equal(t, "socket closed", decoded.Suites[2].TestCases[0].Error.Message)
}

func TestJUnitSanitizesDisallowedXMLControlBytesAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	noisy := "ansi \x1b[31mred\x1b[0m null\x00 bell\x07 badutf8" + string([]byte{0xff, 0xfe}) + " xml<&>\"'"
	summary := results.Summary{
		Cases: []results.CaseResult{
			{
				ID:     "tests/noisy.test.gd::test_noisy",
				Name:   "noisy " + "\x00",
				Status: "fail",
				Output: []model.Event{{Type: model.EventOutput, Message: noisy}},
				Failures: []model.Event{{
					Type:      model.EventAssertionFail,
					Assertion: "equal",
					Message:   noisy,
				}},
			},
		},
		Failed: 1,
	}

	encoded, err := MarshalJUnit(summary)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "\x00")
	assert.NotContains(t, string(encoded), "\x07")
	assert.NotContains(t, string(encoded), "\x1b")

	var decoded junitTestSuites
	require.NoError(t, xml.Unmarshal(encoded, &decoded))
	require.Len(t, decoded.Suites, 1)
	require.Len(t, decoded.Suites[0].TestCases, 1)
	assert.Contains(t, decoded.Suites[0].TestCases[0].SystemOut, `xml<&>"'`)
	assert.Contains(t, decoded.Suites[0].TestCases[0].SystemOut, "\uFFFD")
}
