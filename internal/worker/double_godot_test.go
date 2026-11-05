package worker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotTestDoublesRunPassingCasesOnPersistentWorker(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot test double test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	doubleFile := parseFixture(t, root, "fixtures/godot/api/doubles.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "test-doubles", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "test-doubles",
		Generation:      1,
		Files:           []model.FilePlan{doubleFile},
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
	require.Len(t, statuses, 10)
	for id, status := range statuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
}

func TestGodotDoubleKeptPastItsCaseRecordsNothingInLaterCases(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot double leak test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	leakFile := parseFixture(t, root, "fixtures/godot/api/double_leak.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "double-leak", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "double-leak",
		Generation:      1,
		Files:           []model.FilePlan{leakFile},
	}
	var diagnostics []model.Event
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail, model.EventWorkerError:
			diagnostics = append(diagnostics, event)
		case model.EventCaseFinished:
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, statuses, 3)
	for id, status := range statuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
	assert.Empty(t, diagnostics)
}

func TestGodotDoubleContextOutsideCaseIsClosedAndReportsDiscoveryError(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot double context discovery test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	outsideFile := parseFixture(t, root, "fixtures/godot/api/double_context_outside_case.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "double-context-outside", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "double-context-outside",
		ContentFingerprint: "double-context-outside",
		Generation:         1,
		Files:              []model.FilePlan{outsideFile},
	})
	require.NoError(t, err)
	require.Len(t, manifest.Errors, 1)
	discoveryError := manifest.Errors[0]
	assert.Equal(t, "worker.discovery", discoveryError.Code)
	assert.Equal(t, "double_context is only valid while a case is executing", discoveryError.Message)
	require.NotNil(t, discoveryError.Location)
	assert.Equal(t, outsideFile.Path, discoveryError.Location.File)
	assert.Equal(t, lineContaining(t, root, outsideFile.Path, "double_context()"), discoveryError.Location.Line)

	var statuses = map[string]string{}
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "double-context-outside-run",
		Generation:      1,
		Files:           []model.FilePlan{outsideFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			statuses[event.TestID] = event.Status
		}
		return nil
	}))
	require.NoError(t, err)
	for id, status := range statuses {
		assert.Equal(t, "pass", status, "status for %s", id)
	}
}

func TestGodotTestDoubleMisuseReportsFrameworkErrorsAtTestLines(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot test double failure test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	failureFile := parseFixture(t, root, "fixtures/godot/api/double_failures.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "test-double-failures", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	type expectation struct {
		snippet  string
		fragment string
	}
	type expectedCase struct {
		file         string
		expectations []expectation
	}
	expected := map[string]expectedCase{
		"test_unexpected_call_reports_at_test_line": {
			file: "fixtures/godot/api/double_failures.test.gd",
			expectations: []expectation{{
				snippet:  `inventory.add("ghost")`,
				fragment: "unexpected call to Inventory.add",
			}},
		},
		"test_wrong_typed_answer_reports_at_test_line": {
			file: "fixtures/godot/api/double_failures.test.gd",
			expectations: []expectation{{
				snippet:  `inventory.add("typed")`,
				fragment: "Inventory.add answer returned String, expected bool",
			}},
		},
		"test_wrong_arity_answer_reports_at_test_line": {
			file: "fixtures/godot/api/double_failures.test.gd",
			expectations: []expectation{{
				snippet:  `inventory.gdtest.add.then_answer(_answer_with_one)`,
				fragment: "then_answer callable takes 1 arguments; expected 2",
			}},
		},
		"test_constructor_call_reports_at_source_constructor": {
			file: "fixtures/godot/api/ledger.gd",
			expectations: []expectation{{
				snippet:  "\tclear()",
				fragment: "unexpected call to Ledger.clear",
			}},
		},
		"test_abstract_call_real_reports_and_enqueues_nothing": {
			file: "fixtures/godot/api/double_failures.test.gd",
			expectations: []expectation{
				{
					snippet:  `calculator.gdtest.compute.then_call_real()`,
					fragment: "cannot call an abstract method through then_call_real",
				},
				{
					snippet:  `calculator.compute(4)`,
					fragment: "unexpected call to Calculator.compute",
				},
			},
		},
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "test-double-failures",
		Generation:      1,
		Files:           []model.FilePlan{failureFile},
	}
	diagnostics := make(map[string][]model.Event)
	statuses := make(map[string]string)
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			diagnostics[event.TestID] = append(diagnostics[event.TestID], event)
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
		events := diagnostics[id]
		require.Len(t, events, len(want.expectations), "diagnostics for %s", method)
		for index, expectation := range want.expectations {
			diagnostic := events[index]
			assert.Equal(t, "framework", diagnostic.Assertion)
			assert.Contains(t, diagnostic.Message, expectation.fragment)
			require.NotNil(t, diagnostic.Location)
			assert.Equal(t, want.file, diagnostic.Location.File)
			assert.Equal(
				t,
				lineContaining(t, root, want.file, expectation.snippet),
				diagnostic.Location.Line,
				"line for %s", method,
			)
		}
	}
}

func lineContaining(t *testing.T, root, relative, snippet string) int {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	lines := strings.Split(string(source), "\n")
	found := 0
	line := 0
	for index, text := range lines {
		if strings.Contains(text, snippet) {
			found++
			line = index + 1
		}
	}
	require.Equal(t, 1, found, "snippet %q must occur exactly once in %s", snippet, relative)
	return line
}

func TestGodotDoubleCallerUnderNestedAddonsDirectoryKeepsItsLocation(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot nested double location test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	relative := "fixtures/godot/api/nested/addons/gdtest/double_location.test.gd"
	locationFile := parseFixture(t, root, relative)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "double-nested-location", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "double-nested-location",
		Generation:      1,
		Files:           []model.FilePlan{locationFile},
	}
	var diagnostics []model.Event
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventAssertionFail {
			diagnostics = append(diagnostics, event)
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	diagnostic := diagnostics[0]
	assert.Contains(t, diagnostic.Message, "unexpected call to Inventory.add")
	require.NotNil(t, diagnostic.Location)
	assert.Equal(t, relative, diagnostic.Location.File)
	assert.Equal(t, lineContaining(t, root, relative, `inventory.add("ghost")`), diagnostic.Location.Line)
}
