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

func TestGodotCustomMatcherPassingAndAssertionCounting(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot custom matcher test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	passingFile := parseFixture(t, root, "fixtures/godot/api/custom_matchers.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "custom-matcher-passing", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	caseStatuses := make(map[string]string)
	caseAssertions := make(map[string]int)
	var failures []model.Event
	var runFinished model.Event

	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-passing-run",
		Generation:      1,
		Files:           []model.FilePlan{passingFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail, model.EventWorkerError:
			failures = append(failures, event)
		case model.EventCaseFinished:
			method := event.TestID
			if index := strings.LastIndex(event.TestID, "::"); index >= 0 {
				method = event.TestID[index+2:]
			}
			caseStatuses[method] = event.Status
			caseAssertions[method] = event.Assertions
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Empty(t, failures)
	assert.Equal(t, "pass", runFinished.Status)
	assert.Equal(t, map[string]string{
		"test_passing_matcher":                       "pass",
		"test_passing_matcher_with_caller_message":   "pass",
		"test_match_called_only_once_on_pass":        "pass",
		"test_multiple_assertions_accumulate_counts": "pass",
		"test_result_properties_are_immutable":       "pass",
		"test_defensive_typed_array_cloning":         "pass",
	}, caseStatuses)
	assert.Equal(t, 1, caseAssertions["test_passing_matcher"])
	assert.Equal(t, 1, caseAssertions["test_passing_matcher_with_caller_message"])
	assert.Equal(t, 2, caseAssertions["test_match_called_only_once_on_pass"])
	assert.Equal(t, 2, caseAssertions["test_multiple_assertions_accumulate_counts"])
	assert.Equal(t, 4, caseAssertions["test_result_properties_are_immutable"])
	assert.Equal(t, 4, caseAssertions["test_defensive_typed_array_cloning"])
}

func TestGodotCustomMatcherFailuresAndFailClosedMatrix(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot custom matcher failure test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/custom_matcher_failures.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "custom-matcher-failures", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	selectedMethods := []string{
		"test_mismatch_without_expected",
		"test_mismatch_with_expected",
		"test_mismatch_with_expected_null",
		"test_mismatch_with_caller_message",
		"test_wrapped_caller_location_attribution",
		"test_matcher_internal_helpers_location",
		"test_match_called_only_once_on_failure",
		"test_defensive_collection_mutations",
		"test_defensive_getter_cloning",
		"test_cyclic_collection_diagnostics",
		"test_fail_closed_null_matcher",
		"test_fail_closed_empty_matcher_name",
		"test_fail_closed_whitespace_matcher_name",
		"test_fail_closed_unoverridden_matcher_name",
		"test_fail_closed_unoverridden_matcher_match",
		"test_fail_closed_null_match_result",
		"test_fail_closed_direct_new_match_result",
		"test_fail_closed_empty_unmatched_message",
		"test_fail_closed_whitespace_unmatched_message",
	}

	selectedIDs := make([]string, 0, len(selectedMethods))
	for _, method := range selectedMethods {
		selectedIDs = append(selectedIDs, failureFile.Path+"::"+method)
	}

	caseFailures := make(map[string][]model.Event)
	caseStatuses := make(map[string]string)
	caseAssertions := make(map[string]int)
	var runFinished model.Event

	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:  model.ProtocolVersion,
		RunID:            "custom-matcher-failures-run",
		Generation:       1,
		SelectionApplied: true,
		SelectedIDs:      selectedIDs,
		Files:            []model.FilePlan{failureFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			caseFailures[event.TestID] = append(caseFailures[event.TestID], event)
		case model.EventCaseFinished:
			method := event.TestID
			if index := strings.LastIndex(event.TestID, "::"); index >= 0 {
				method = event.TestID[index+2:]
			}
			caseStatuses[method] = event.Status
			caseAssertions[method] = event.Assertions
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, "fail", runFinished.Status)

	for _, method := range selectedMethods {
		if method == "test_defensive_getter_cloning" {
			assert.Equal(t, "pass", caseStatuses[method], "status for %s", method)
			assert.Equal(t, 1, caseAssertions[method])
			assert.Empty(t, caseFailures[failureFile.Path+"::"+method])
			continue
		}
		assert.Equal(t, "fail", caseStatuses[method], "status for %s", method)
	}

	// 1. Mismatch without expected
	mismatchNoExpectedID := failureFile.Path + "::test_mismatch_without_expected"
	require.Len(t, caseFailures[mismatchNoExpectedID], 1)
	failureWithoutExpected := caseFailures[mismatchNoExpectedID][0]
	assert.Equal(t, "always_fails_without_expected", failureWithoutExpected.Assertion)
	assert.Equal(t, "explicit mismatch without expected", failureWithoutExpected.Message)
	assert.Nil(t, failureWithoutExpected.Expected)
	require.NotNil(t, failureWithoutExpected.Actual)
	assert.Equal(t, "sample", failureWithoutExpected.Actual.Value)
	assert.Equal(t, 1, caseAssertions["test_mismatch_without_expected"])
	assert.Equal(t, lineContaining(t, root, failureFile.Path, `expect_that("sample", AlwaysFailsWithoutExpected.new())`), failureWithoutExpected.Location.Line)

	// 2. Mismatch with expected
	mismatchExpectedID := failureFile.Path + "::test_mismatch_with_expected"
	require.Len(t, caseFailures[mismatchExpectedID], 1)
	failureWithExpected := caseFailures[mismatchExpectedID][0]
	assert.Equal(t, "matches_expected_value", failureWithExpected.Assertion)
	assert.Equal(t, "values differed", failureWithExpected.Message)
	require.NotNil(t, failureWithExpected.Expected)
	assert.Equal(t, "wanted", failureWithExpected.Expected.Value)
	require.NotNil(t, failureWithExpected.Actual)
	assert.Equal(t, "received", failureWithExpected.Actual.Value)
	assert.Equal(t, 1, caseAssertions["test_mismatch_with_expected"])

	// 3. Mismatch with expected = null and has_expected = true
	mismatchExpectedNullID := failureFile.Path + "::test_mismatch_with_expected_null"
	require.Len(t, caseFailures[mismatchExpectedNullID], 1)
	failureExpectedNull := caseFailures[mismatchExpectedNullID][0]
	assert.Equal(t, "matches_expected_value", failureExpectedNull.Assertion)
	assert.Equal(t, "values differed", failureExpectedNull.Message)
	require.NotNil(t, failureExpectedNull.Expected)
	assert.Equal(t, "null", failureExpectedNull.Expected.Type)
	assert.Nil(t, failureExpectedNull.Expected.Value)
	require.NotNil(t, failureExpectedNull.Actual)
	assert.Equal(t, "lingering_value", failureExpectedNull.Actual.Value)
	assert.Equal(t, 1, caseAssertions["test_mismatch_with_expected_null"])

	// 4. Mismatch with caller message
	mismatchCallerMessageID := failureFile.Path + "::test_mismatch_with_caller_message"
	require.Len(t, caseFailures[mismatchCallerMessageID], 1)
	failureCallerMessage := caseFailures[mismatchCallerMessageID][0]
	assert.Equal(t, "always_fails_without_expected", failureCallerMessage.Assertion)
	assert.Equal(t, "caller prefix: explicit mismatch without expected", failureCallerMessage.Message)
	assert.Equal(t, 1, caseAssertions["test_mismatch_with_caller_message"])

	// 5. Exact call site attribution through wrapper helper
	wrappedID := failureFile.Path + "::test_wrapped_caller_location_attribution"
	require.Len(t, caseFailures[wrappedID], 1)
	assert.Equal(
		t,
		lineContaining(t, root, failureFile.Path, "expect_that(actual, matcher)"),
		caseFailures[wrappedID][0].Location.Line,
	)

	// 6. Matcher with deep internal helper calls does not obscure call site
	internalHelpersID := failureFile.Path + "::test_matcher_internal_helpers_location"
	require.Len(t, caseFailures[internalHelpersID], 1)
	assert.Equal(
		t,
		lineContaining(t, root, failureFile.Path, `expect_that("sample", MatcherWithInternalHelpers.new())`),
		caseFailures[internalHelpersID][0].Location.Line,
	)

	// 7. Single evaluation on failure
	assert.Equal(t, 2, caseAssertions["test_match_called_only_once_on_failure"])

	// 8. Defensive collection mutation
	defensiveID := failureFile.Path + "::test_defensive_collection_mutations"
	require.Len(t, caseFailures[defensiveID], 1)
	failureDefensive := caseFailures[defensiveID][0]
	require.NotNil(t, failureDefensive.Expected)
	require.NotNil(t, failureDefensive.Actual)
	// WireValue serialization of Dictionary contains entries
	require.NotEmpty(t, failureDefensive.Expected.Entries)
	assert.Equal(t, "original_expected", failureDefensive.Expected.Entries[0].Value.Value)
	require.NotEmpty(t, failureDefensive.Actual.Entries)
	assert.Equal(t, "original_actual", failureDefensive.Actual.Entries[0].Value.Value)

	// 9. Fail-closed cases: assertion count is 0, assertion is "expect_that", message is actionable
	failClosedCases := []struct {
		method   string
		fragment string
	}{
		{"test_fail_closed_null_matcher", "expect_that requires a valid GDTestMatcher"},
		{"test_fail_closed_empty_matcher_name", "GDTestMatcher.name() must return a non-empty String"},
		{"test_fail_closed_whitespace_matcher_name", "GDTestMatcher.name() must return a non-empty String"},
		{"test_fail_closed_unoverridden_matcher_name", "GDTestMatcher.name() must return a non-empty String"},
		{"test_fail_closed_unoverridden_matcher_match", "GDTestMatcher.match() must be overridden"},
		{"test_fail_closed_null_match_result", "GDTestMatcher.match() must return a GDTestMatchResult"},
		{"test_fail_closed_direct_new_match_result", "GDTestMatchResult must be constructed with matched() or unmatched()"},
		{"test_fail_closed_empty_unmatched_message", "GDTestMatchResult.unmatched() requires a non-empty message"},
		{"test_fail_closed_whitespace_unmatched_message", "GDTestMatchResult.unmatched() requires a non-empty message"},
	}

	for _, failClosedCase := range failClosedCases {
		id := failureFile.Path + "::" + failClosedCase.method
		require.Len(t, caseFailures[id], 1, "failures for %s", failClosedCase.method)
		event := caseFailures[id][0]
		assert.Equal(t, "expect_that", event.Assertion, "assertion name for %s", failClosedCase.method)
		assert.Contains(t, event.Message, failClosedCase.fragment, "message for %s", failClosedCase.method)
		assert.Equal(t, 0, caseAssertions[failClosedCase.method], "assertion count for %s", failClosedCase.method)
	}
}

func TestGodotCustomMatcherRuntimeErrorsAndWorkerReset(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot custom matcher runtime error test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/custom_matcher_failures.test.gd")
	untypedFile := parseFixture(t, root, "fixtures/godot/api/invalid/untyped_matchers.test.gd")
	passingFile := parseFixture(t, root, "fixtures/godot/api/custom_matchers.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "custom-matcher-runtime-errors", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)
	initialPID := process.PID()

	errorMethods := []string{
		"test_runtime_error_inside_name",
		"test_runtime_error_inside_match",
		"test_runtime_error_coroutine_match",
	}
	selectedIDs := make([]string, 0, len(errorMethods))
	for _, method := range errorMethods {
		selectedIDs = append(selectedIDs, failureFile.Path+"::"+method)
	}

	caseStatuses := make(map[string]string)
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:  model.ProtocolVersion,
		RunID:            "custom-matcher-runtime-errors-run-1",
		Generation:       1,
		SelectionApplied: true,
		SelectedIDs:      selectedIDs,
		Files:            []model.FilePlan{failureFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			method := event.TestID
			if index := strings.LastIndex(event.TestID, "::"); index >= 0 {
				method = event.TestID[index+2:]
			}
			caseStatuses[method] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	for _, method := range errorMethods {
		assert.Equal(t, "error", caseStatuses[method], "status for %s", method)
	}

	require.NoError(t, process.Reset(ctx))
	assert.Equal(t, initialPID, process.PID(), "persistent worker PID must be preserved after Reset")

	// Untyped boundary error cases
	untypedStatuses := make(map[string]string)
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-untyped-run",
		Generation:      1,
		Files:           []model.FilePlan{untypedFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			method := event.TestID
			if index := strings.LastIndex(event.TestID, "::"); index >= 0 {
				method = event.TestID[index+2:]
			}
			untypedStatuses[method] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"test_untyped_caller_passes_dictionary_matcher": "error",
		"test_untyped_matcher_returns_dictionary":       "error",
		"test_untyped_caller_passes_freed_instance":     "error",
	}, untypedStatuses)

	require.NoError(t, process.Reset(ctx))
	assert.Equal(t, initialPID, process.PID(), "persistent worker PID must be preserved after second Reset")

	// Run passing suite to verify healthy recovery
	passingStatuses := make(map[string]string)
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-recovery-run",
		Generation:      1,
		Files:           []model.FilePlan{passingFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			method := event.TestID
			if index := strings.LastIndex(event.TestID, "::"); index >= 0 {
				method = event.TestID[index+2:]
			}
			passingStatuses[method] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, passingStatuses, 6)
	for name, status := range passingStatuses {
		assert.Equal(t, "pass", status, "recovery status for %s", name)
	}
}

func TestGodotCustomMatcherOutsideCaseRejection(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot custom matcher outside case test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	outsideCaseFile := parseFixture(t, root, "fixtures/godot/api/custom_matcher_outside_case.test.gd")
	suiteHookFile := parseFixture(t, root, "fixtures/godot/api/custom_matcher_suite_hook.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "custom-matcher-outside-case", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	// 1. Discovery outside-case expect_that
	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-discovery",
		Generation:      1,
		Files:           []model.FilePlan{outsideCaseFile},
	})
	require.NoError(t, err)
	require.Len(t, manifest.Errors, 1)
	discoveryError := manifest.Errors[0]
	assert.Equal(t, "worker.discovery", discoveryError.Code)
	assert.Equal(t, "expect_that is only valid while a case is executing", discoveryError.Message)
	require.NotNil(t, discoveryError.Location)
	assert.Equal(t, outsideCaseFile.Path, discoveryError.Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, outsideCaseFile.Path, `expect_that("sample", matcher)`),
		discoveryError.Location.Line,
	)

	// Running the case verifies matcher.match_called was false
	caseStatuses := make(map[string]string)
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-outside-case-run",
		Generation:      1,
		Files:           []model.FilePlan{outsideCaseFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			caseStatuses[event.Name] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	for _, status := range caseStatuses {
		assert.Equal(t, "pass", status)
	}

	require.NoError(t, process.Reset(ctx))

	// 2. Suite hook outside-case expect_that
	var hookErrors []model.Event
	var hookRunFinished model.Event
	var hookCasesExecuted int
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "custom-matcher-suite-hook-run",
		Generation:      1,
		Files:           []model.FilePlan{suiteHookFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerError:
			hookErrors = append(hookErrors, event)
		case model.EventCaseStarted, model.EventCaseFinished:
			hookCasesExecuted++
		case model.EventRunFinished:
			hookRunFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Zero(t, hookCasesExecuted, "suite hook error must prevent case execution")
	assert.Equal(t, "error", hookRunFinished.Status)
	require.Len(t, hookErrors, 1)
	assert.Equal(t, "worker.framework", hookErrors[0].Name)
	assert.Equal(t, "framework", hookErrors[0].Assertion)
	assert.Equal(t, "expect_that is only valid while a case is executing", hookErrors[0].Message)
	require.NotNil(t, hookErrors[0].Location)
	assert.Equal(t, suiteHookFile.Path, hookErrors[0].Location.File)
	assert.Equal(
		t,
		lineContaining(t, root, suiteHookFile.Path, `expect_that("sample", matcher)`),
		hookErrors[0].Location.Line,
	)
}
