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

// TestGeneratedDoubleOfSourceWithSpaceRunsInGodot proves that Godot accepts the
// quoted res:// path of a generated double whose source and output both contain
// a space, and that the generator's ownership check accepts its own output.
func TestGeneratedDoubleOfSourceWithSpaceRunsInGodot(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot double path test skipped: godot is not on PATH")
	}
	root := t.TempDir()
	files := map[string]string{
		"project.godot":        "config_version=5\n\n[application]\nconfig/name=\"gdtest double path\"\n",
		"gdtest.json":          `{"test_doubles":[{"source":"src/item catalog.gd","output":"doubles/item catalog double.gd","class_name":"ItemCatalogDouble"}]}`,
		"src/item catalog.gd":  "extends RefCounted\n\nfunc count() -> int:\n\treturn 0\n",
		"item_catalog.test.gd": "extends GDTest\n\nfunc test_spaced_source_double_runs() -> void:\n\tvar catalog: ItemCatalogDouble = ItemCatalogDouble.new(double_context())\n\tcatalog.gdtest.count.then_return(3)\n\texpect_equal(catalog.count(), 3)\n",
	}
	for relative, content := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	output, code := generateAndRunProject(t, files)
	require.Equal(t, app.ExitOK, code, "output:\n%s", output)
	assert.Contains(t, output, "1 passed")
}

// TestGeneratedErrorReturnRejectsNullAnswerInGodot proves that a null answer for
// a method declared to return the global enum Error is a framework error, because
// enum values are integers and null is not one.
func TestGeneratedErrorReturnRejectsNullAnswerInGodot(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot error return test skipped: godot is not on PATH")
	}
	files := map[string]string{
		"project.godot":  "config_version=5\n\n[application]\nconfig/name=\"gdtest error return\"\n",
		"gdtest.json":    `{"test_doubles":[{"source":"src/status.gd","output":"doubles/status_double.gd","class_name":"StatusDouble"}]}`,
		"src/status.gd":  "extends RefCounted\n\nfunc status() -> Error:\n\treturn OK\n",
		"status.test.gd": "extends GDTest\n\nfunc _no_answer() -> Variant:\n\treturn null\n\nfunc test_null_answer_for_error_return() -> void:\n\tvar status: StatusDouble = StatusDouble.new(double_context())\n\tstatus.gdtest.status.then_answer(_no_answer)\n\tstatus.status()\n",
	}
	output, code := generateAndRunProject(t, files)
	assert.NotEqual(t, app.ExitOK, code, "output:\n%s", output)
	assert.Contains(t, output, "answer returned Nil, expected Error")
}

// generateAndRunProject writes files into a temporary project, installs the
// runtime, generates its doubles, and runs its tests with --no-daemon. It returns
// the combined output and the exit code of the run.
func generateAndRunProject(t *testing.T, files map[string]string) (string, int) {
	t.Helper()
	root := t.TempDir()
	for relative, content := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	require.NoError(t, gdtest.InstallRuntime(root))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var generateOut, generateErr bytes.Buffer
	generateCode := app.Run(ctx, []string{"generate", "doubles", "--project", root}, app.IO{Out: &generateOut, Err: &generateErr})
	require.Equal(t, app.ExitOK, generateCode, "stdout:\n%s\nstderr:\n%s", generateOut.String(), generateErr.String())

	var output, diagnostics bytes.Buffer
	code := app.Run(ctx, []string{"run", "--no-daemon", "--project", root}, app.IO{Out: &output, Err: &diagnostics})
	return output.String() + diagnostics.String(), code
}
