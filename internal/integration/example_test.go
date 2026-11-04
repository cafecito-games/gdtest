package integration_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentedBasicExampleExecutes(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot example test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repository := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	root := t.TempDir()
	for _, relative := range []string{"project.godot", "gdtest.json", "math.test.gd"} {
		data, err := os.ReadFile(filepath.Join(repository, "examples", "basic", relative))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, relative), data, 0o600))
	}
	require.NoError(t, gdtest.InstallRuntime(root))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code := app.Run(ctx, []string{
		"run", "--no-daemon", "--project", root,
	}, app.IO{Out: &output, Err: &diagnostics})
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "3 passed")
}
