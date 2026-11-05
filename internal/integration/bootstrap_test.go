package integration_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshCheckoutBootstrapsWithoutExternalGodotImport(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("fresh-checkout bootstrap test skipped: godot is not on PATH")
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "project.godot"),
		[]byte("config_version=5\n\n[application]\nconfig/name=\"fresh gdtest checkout\"\n"),
		0o600,
	))
	require.NoError(t, gdtest.InstallRuntime(root))
	testPath := filepath.Join(root, "tests", "fresh.test.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(testPath), 0o700))
	require.NoError(t, os.WriteFile(testPath, []byte(
		"extends GDTest\n\n\n## Runs from a checkout with no import cache.\nfunc test_fresh_checkout() -> void:\n\texpect_true(true)\n",
	), 0o600))
	_, err := os.Stat(filepath.Join(root, ".godot"))
	require.ErrorIs(t, err, os.ErrNotExist)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code := app.Run(ctx, []string{
		"run", "--no-daemon", "--verbose", "--project", root, "tests/fresh.test.gd",
	}, app.IO{Out: &output, Err: &diagnostics})

	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "1 passed")
	assert.Contains(t, diagnostics.String(), "gdtest: timing project_bootstrap_import=",
		"fresh-checkout import must be a distinct observable phase")
	_, err = os.Stat(filepath.Join(root, ".godot"))
	assert.NoError(t, err, "worker startup should establish the fresh project's Godot cache")
}
