package results

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorPreservesManifestExecutionOrderAndCaseData(t *testing.T) {
	t.Parallel()

	collector := NewCollector()
	events := []model.Event{
		{Type: model.EventCaseStarted, TestID: "z::test"},
		{
			Type: model.EventCaseFinished, TestID: "z::test", Status: "pass",
			Assertions: 2, Messages: []string{"kept"},
			Outcomes: []model.Outcome{{Kind: "ok", Data: model.WireValue{Type: "dictionary"}}},
		},
		{Type: model.EventCaseStarted, TestID: "a::test"},
		{Type: model.EventCaseFinished, TestID: "a::test", Status: "fail"},
	}
	for _, event := range events {
		require.NoError(t, collector.Emit(event))
	}
	summary := collector.Summary()
	require.Len(t, summary.Cases, 2)
	assert.Equal(t, "z::test", summary.Cases[0].ID)
	assert.Equal(t, 2, summary.Cases[0].Assertions)
	assert.Equal(t, []string{"kept"}, summary.Cases[0].Messages)
	require.Len(t, summary.Cases[0].Outcomes, 1)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
}

func TestCollectorFoldsDiscoveryDiagnosticsAsInfrastructure(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	events := []model.Event{
		{Type: model.EventRunStarted, RunID: "run"},
		{Type: model.EventWorkerError, RunID: "run", Name: "worker.discovery", Message: "invalid provider"},
		{Type: model.EventRunFinished, RunID: "run", Status: "error"},
	}
	for _, event := range events {
		require.NoError(t, collector.Emit(event))
	}
	summary := collector.Summary()
	assert.Equal(t, 1, summary.Errored)
	assert.True(t, summary.HasInfrastructureFailure())
	assert.True(t, summary.HasFailures())
	assert.Len(t, summary.Infrastructure, 1)
	assert.Empty(t, summary.Errors)
}

func TestCollectorFoldsExecutionFrameworkErrorsSeparatelyFromInfrastructure(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	events := []model.Event{
		{Type: model.EventRunStarted, RunID: "run"},
		{Type: model.EventCaseStarted, RunID: "run", TestID: "suite.test.gd::test_passing"},
		{Type: model.EventCaseFinished, RunID: "run", TestID: "suite.test.gd::test_passing", Status: "pass"},
		{
			Type:      model.EventWorkerError,
			RunID:     "run",
			Name:      "worker.framework",
			Assertion: "framework",
			Message:   "expect is only valid while a case is executing",
			Location:  &model.Location{File: "suite.test.gd", Line: 12},
			Metadata:  map[string]any{"code": "worker.framework"},
		},
		{
			Type:      model.EventWorkerError,
			RunID:     "run",
			Name:      "worker.framework",
			Assertion: "framework",
			Message:   "fail is only valid while a case is executing",
			Location:  &model.Location{File: "suite.test.gd", Line: 24},
			Metadata:  map[string]any{"code": "worker.framework"},
		},
		{Type: model.EventRunFinished, RunID: "run", Status: "error"},
	}
	for _, event := range events {
		require.NoError(t, collector.Emit(event))
	}
	summary := collector.Summary()
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 2, summary.Errored)
	assert.True(t, summary.HasFailures())
	assert.False(t, summary.HasInfrastructureFailure())
	assert.Empty(t, summary.Infrastructure)
	require.Len(t, summary.Errors, 2)
	assert.Equal(t, "suite.test.gd", summary.Errors[0].Location.File)
	assert.Equal(t, 12, summary.Errors[0].Location.Line)
	assert.Equal(t, 24, summary.Errors[1].Location.Line)
}

func TestCollectorCombinesFrameworkAndInfrastructureErrorsInTotalCount(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	require.NoError(t, collector.Emit(model.Event{
		Type: model.EventWorkerError, Name: "worker.framework", Message: "hook misuse",
	}))
	require.NoError(t, collector.Emit(model.Event{
		Type: model.EventWorkerError, Name: "worker.discovery", Message: "invalid provider",
	}))
	summary := collector.Summary()
	assert.Equal(t, 2, summary.Errored)
	assert.True(t, summary.HasFailures())
	assert.True(t, summary.HasInfrastructureFailure())
	assert.Len(t, summary.Errors, 1)
	assert.Len(t, summary.Infrastructure, 1)
}

func TestCollectorUsesExplicitManifestOrderForConcurrentEvents(t *testing.T) {
	t.Parallel()
	collector := NewCollectorWithOrder([]string{"z::test", "a::test"})
	for _, id := range []string{"a::test", "z::test"} {
		require.NoError(t, collector.Emit(model.Event{Type: model.EventCaseStarted, TestID: id}))
		require.NoError(t, collector.Emit(model.Event{Type: model.EventCaseFinished, TestID: id, Status: "pass"}))
	}
	summary := collector.Summary()
	require.Len(t, summary.Cases, 2)
	assert.Equal(t, "z::test", summary.Cases[0].ID)
	assert.Equal(t, "a::test", summary.Cases[1].ID)
}

func TestCollectorCountsRunErrorWithoutDiagnostic(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	require.NoError(t, collector.Emit(model.Event{Type: model.EventRunFinished, Status: "error"}))
	summary := collector.Summary()
	assert.Equal(t, 1, summary.Errored)
	assert.False(t, summary.HasInfrastructureFailure())
}

func TestOutcomePolicyFailsOnlyEnabledObservedStatuses(t *testing.T) {
	t.Parallel()

	summary := Summary{Skipped: 2, Todo: 1, Void: 3}
	assert.False(t, summary.HasFailures())

	summary.ApplyOutcomePolicy(OutcomePolicy{FailOnSkip: true, FailOnVoid: true})
	assert.True(t, summary.HasFailures())
	assert.Equal(t, []PolicyFailure{
		{Status: "skip", Count: 2},
		{Status: "void", Count: 3},
	}, summary.PolicyFailures)
	assert.Equal(t, 2, summary.Skipped, "strict policy must preserve case outcome counts")
	assert.Equal(t, 1, summary.Todo)
	assert.Equal(t, 3, summary.Void)

	summary.ApplyOutcomePolicy(OutcomePolicy{FailOnTodo: true})
	assert.Equal(t, []PolicyFailure{{Status: "todo", Count: 1}}, summary.PolicyFailures)
}

func TestOutcomePolicyIgnoresEnabledStatusesWithZeroCases(t *testing.T) {
	t.Parallel()

	summary := Summary{}
	summary.ApplyOutcomePolicy(OutcomePolicy{FailOnSkip: true, FailOnTodo: true, FailOnVoid: true})

	assert.Empty(t, summary.PolicyFailures)
	assert.False(t, summary.HasFailures())
}

func TestCollectorFoldsCustomMatcherAssertionFailures(t *testing.T) {
	t.Parallel()

	collector := NewCollector()
	caseID := "tests/custom.test.gd::test_matchers"
	require.NoError(t, collector.Emit(model.Event{
		Type:   model.EventCaseStarted,
		TestID: caseID,
		Name:   "test_matchers",
	}))

	failures := []model.Event{
		{
			Type:      model.EventAssertionFail,
			TestID:    caseID,
			Assertion: "matches_order",
			Message:   "order item differs",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 20},
			Actual:    &model.WireValue{Type: "string", Value: "orange"},
			Expected:  &model.WireValue{Type: "string", Value: "apple"},
		},
		{
			Type:      model.EventAssertionFail,
			TestID:    caseID,
			Assertion: "is_valid_format",
			Message:   "text format is invalid",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 25},
			Actual:    &model.WireValue{Type: "string", Value: "abc"},
			Expected:  nil,
		},
		{
			Type:      model.EventAssertionFail,
			TestID:    caseID,
			Assertion: "matches_cleared",
			Message:   "expected state to be cleared",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 30},
			Actual:    &model.WireValue{Type: "string", Value: "active"},
			Expected:  &model.WireValue{Type: "null", Value: nil},
		},
		{
			Type:      model.EventAssertionFail,
			TestID:    caseID,
			Assertion: "matches_threshold",
			Message:   "inventory limit: count exceeds maximum",
			Location:  &model.Location{File: "tests/custom.test.gd", Line: 35},
			Actual:    &model.WireValue{Type: "int", Value: "10"},
			Expected:  &model.WireValue{Type: "int", Value: "5"},
		},
	}

	for _, failure := range failures {
		require.NoError(t, collector.Emit(failure))
	}

	require.NoError(t, collector.Emit(model.Event{
		Type:       model.EventCaseFinished,
		TestID:     caseID,
		Name:       "test_matchers",
		Status:     "fail",
		Assertions: 4,
	}))

	summary := collector.Summary()
	assert.Equal(t, 1, summary.Failed)
	require.Len(t, summary.Cases, 1)

	caseResult := summary.Cases[0]
	assert.Equal(t, caseID, caseResult.ID)
	assert.Equal(t, "fail", caseResult.Status)
	assert.Equal(t, 4, caseResult.Assertions)
	require.Len(t, caseResult.Failures, 4)

	assert.Equal(t, "matches_order", caseResult.Failures[0].Assertion)
	assert.Equal(t, "order item differs", caseResult.Failures[0].Message)
	assert.NotNil(t, caseResult.Failures[0].Expected)

	assert.Equal(t, "is_valid_format", caseResult.Failures[1].Assertion)
	assert.Nil(t, caseResult.Failures[1].Expected)

	assert.Equal(t, "matches_cleared", caseResult.Failures[2].Assertion)
	require.NotNil(t, caseResult.Failures[2].Expected)
	assert.Equal(t, "null", caseResult.Failures[2].Expected.Type)
	assert.Nil(t, caseResult.Failures[2].Expected.Value)

	assert.Equal(t, "matches_threshold", caseResult.Failures[3].Assertion)
	assert.Equal(t, "inventory limit: count exceeds maximum", caseResult.Failures[3].Message)
}
