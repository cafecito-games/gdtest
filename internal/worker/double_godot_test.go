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
	require.Len(t, statuses, 9)
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

	expected := map[string]struct {
		file     string
		snippet  string
		fragment string
	}{
		"test_unexpected_call_reports_at_test_line": {
			file:     "fixtures/godot/api/double_failures.test.gd",
			snippet:  `inventory.add("ghost")`,
			fragment: "unexpected call to Inventory.add",
		},
		"test_wrong_typed_answer_reports_at_test_line": {
			file:     "fixtures/godot/api/double_failures.test.gd",
			snippet:  `inventory.add("typed")`,
			fragment: "Inventory.add answer returned String, expected bool",
		},
		"test_wrong_arity_answer_reports_at_test_line": {
			file:     "fixtures/godot/api/double_failures.test.gd",
			snippet:  `inventory.gdtest.add.then_answer(_answer_with_one)`,
			fragment: "then_answer callable takes 1 arguments; expected 2",
		},
		"test_constructor_call_reports_at_source_constructor": {
			file:     "fixtures/godot/api/ledger.gd",
			snippet:  "\tclear()",
			fragment: "unexpected call to Ledger.clear",
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
		require.Len(t, events, 1, "diagnostics for %s", method)
		diagnostic := events[0]
		assert.Equal(t, "framework", diagnostic.Assertion)
		assert.Contains(t, diagnostic.Message, want.fragment)
		require.NotNil(t, diagnostic.Location)
		assert.Equal(t, want.file, diagnostic.Location.File)
		assert.Equal(t, lineContaining(t, root, want.file, want.snippet), diagnostic.Location.Line, "line for %s", method)
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
	require.Equal(t, 1, found, "snippet %q must occur exactly once", snippet)
	return line
}
