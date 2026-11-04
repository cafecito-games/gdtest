package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotFailureWireValuesRemainTypedAndBounded(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot wire-value test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	relative := "fixtures/godot/api/wire_values.test.gd"
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	file, err := discovery.Parse(relative, source)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "wire-values", Generation: 1,
	})
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		assert.NoError(t, process.Shutdown(shutdownCtx))
	}()

	var failures []model.Event
	var finished model.Event
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "wire-values", Generation: 1,
		Files: []model.FilePlan{file},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventAssertionFail {
			failures = append(failures, event)
		}
		if event.Type == model.EventRunFinished {
			finished = event
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, failures, 2)
	require.NotNil(t, failures[0].Actual)
	assert.Equal(t, "array", failures[0].Actual.Type)
	require.Len(t, failures[0].Actual.Items, 2)
	assert.Equal(t, "vector2", failures[0].Actual.Items[0].Type)
	assert.Equal(t, map[string]any{"x": float64(1), "y": float64(2)}, failures[0].Actual.Items[0].Value)
	assert.Equal(t, "dictionary", failures[0].Actual.Items[1].Type)
	require.Len(t, failures[0].Actual.Items[1].Entries, 1)
	assert.Equal(t, "color", failures[0].Actual.Items[1].Entries[0].Value.Type)
	require.NotNil(t, failures[1].Actual)
	require.NotNil(t, failures[1].Expected)
	assert.Equal(t, "nan", failures[1].Actual.Value)
	assert.Equal(t, "+inf", failures[1].Expected.Value)
	assert.Positive(t, finished.Duration)
	timingValues, ok := finished.Metadata["timings_usec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, timingValues, "script_load_dynamic_definition")
	assert.Contains(t, timingValues, "hooks_cases")
	assert.Contains(t, timingValues, "result_serialization")
}
