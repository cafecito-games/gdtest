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

func TestMigratedVestFixtureExecutesInGodot(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot migration test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repository := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "project.godot"),
		[]byte("config_version=5\n\n[application]\nconfig/name=\"migrated Vest fixture\"\n"),
		0o600,
	))
	require.NoError(t, gdtest.InstallRuntime(root))
	source, err := os.ReadFile(filepath.Join(repository, "fixtures", "migration", "common.input.gd"))
	require.NoError(t, err)
	testPath := filepath.Join(root, "tests", "migrated.test.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(testPath), 0o700))
	require.NoError(t, os.WriteFile(testPath, source, 0o600))

	var migrationOutput, migrationErrors bytes.Buffer
	code := app.Run(context.Background(), []string{
		"migrate", "vest", "--project", root, "--write", "tests/migrated.test.gd",
	}, app.IO{Out: &migrationOutput, Err: &migrationErrors})
	require.Equal(t, app.ExitOK, code, migrationErrors.String())
	assert.Contains(t, migrationOutput.String(), "1 file(s) changed")
	migrated, err := os.ReadFile(testPath)
	require.NoError(t, err)
	assert.Contains(t, string(migrated), "extends GDTest")
	assert.NotContains(t, string(migrated), "Vest.")

	bootstrapWorktree(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code = app.Run(ctx, []string{
		"run", "--no-daemon", "--project", root, "tests/migrated.test.gd",
	}, app.IO{Out: &output, Err: &diagnostics})
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "2 passed")
}
