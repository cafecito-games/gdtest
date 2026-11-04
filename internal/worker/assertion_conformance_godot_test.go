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

func TestGodotAssertionConformanceFailureAndInvalidInputMatrix(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot assertion conformance test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/assertion_failures.test.gd")
	passingFile := parseFixture(t, root, "fixtures/godot/api/conformance.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "assertion-conformance", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	expected := map[string]string{
		"test_expect_failure":                        "expect",
		"test_expect_not_failure":                    "expect_not",
		"test_expect_true_failure":                   "expect",
		"test_expect_false_failure":                  "expect_not",
		"test_expect_equal_failure":                  "expect_equal",
		"test_expect_not_equal_failure":              "expect_not_equal",
		"test_expect_same_failure":                   "expect_same",
		"test_expect_not_same_failure":               "expect_not_same",
		"test_expect_approx_failure":                 "expect_approx",
		"test_expect_not_approx_failure":             "expect_not_approx",
		"test_expect_null_failure":                   "expect_null",
		"test_expect_not_null_failure":               "expect_not_null",
		"test_expect_empty_failure":                  "expect_empty",
		"test_expect_not_empty_failure":              "expect_not_empty",
		"test_expect_contains_failure":               "expect_contains",
		"test_expect_does_not_contain_failure":       "expect_does_not_contain",
		"test_expect_equal_invalid_result":           "expect_equal",
		"test_expect_not_equal_invalid_result":       "expect_not_equal",
		"test_expect_approx_invalid_tolerance":       "expect_approx",
		"test_expect_not_approx_invalid_tolerance":   "expect_not_approx",
		"test_expect_empty_invalid_input":            "expect_empty",
		"test_expect_not_empty_invalid_input":        "expect_not_empty",
		"test_expect_contains_invalid_input":         "expect_contains",
		"test_expect_does_not_contain_invalid_input": "expect_does_not_contain",
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "assertion-failures",
		Generation:      1,
		Files:           []model.FilePlan{failureFile},
	}
	failures := make(map[string]model.Event)
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			failures[event.TestID] = event
		case model.EventCaseFinished:
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, failures, len(expected))
	require.Len(t, statuses, len(expected))
	for method, assertion := range expected {
		id := failureFile.Path + "::" + method
		diagnostic, exists := failures[id]
		require.True(t, exists, "missing diagnostic for %s", id)
		assert.Equal(t, assertion, diagnostic.Assertion)
		assert.Contains(t, diagnostic.Message, "custom ")
		require.NotNil(t, diagnostic.Location)
		assert.Equal(t, failureFile.Path, diagnostic.Location.File)
		assert.Positive(t, diagnostic.Location.Line)
		assert.NotNil(t, diagnostic.Actual)
		assert.Equal(t, "fail", statuses[id])
	}

	require.NoError(t, process.Reset(ctx))
	passingID := passingFile.Path + "::test_assertion_surface"
	plan.RunID = "assertion-passing"
	plan.Files = []model.FilePlan{passingFile}
	plan.SelectionApplied = true
	plan.SelectedIDs = []string{passingID}
	passingFailures := 0
	passingStatus := ""
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventAssertionFail {
			passingFailures++
		}
		if event.Type == model.EventCaseFinished && strings.EqualFold(event.TestID, passingID) {
			passingStatus = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Zero(t, passingFailures, "passing assertions must not serialize failure diagnostics")
	assert.Equal(t, "pass", passingStatus)
}
