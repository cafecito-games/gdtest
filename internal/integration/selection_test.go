package integration_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLISelectsEngineCompletedCasesExactly(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot selector test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "exact provider row",
			args: []string{"fixtures/godot/api/conformance.test.gd::test_named_provider[a%2Ffirst]"},
		},
		{
			name: "dynamic name",
			args: []string{"--name", "dynamic rows [a/b]", "fixtures/godot/api/conformance.test.gd"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			arguments := []string{"run", "--no-daemon", "--project", root}
			arguments = append(arguments, test.args...)
			var output, diagnostics bytes.Buffer
			code := app.Run(ctx, arguments, app.IO{Out: &output, Err: &diagnostics})
			require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
			assert.Contains(t, output.String(), "1 passed")
		})
	}
}
