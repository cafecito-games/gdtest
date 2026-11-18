package integration_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Focus and the only-mode policy are properties of the whole plan, so they
// cannot be left to each worker run to re-derive: the plan is dispatched one
// file at a time, and a worker that sees only one file sees focus only in that
// file. One focused case anywhere in the selection has to suppress the
// unfocused cases everywhere in it.
func TestCLIFocusAppliesAcrossTheWholeSelection(t *testing.T) {
	requireGodot(t, "focus-scope test")

	root := t.TempDir()
	timeoutWrite(t, filepath.Join(root, "project.godot"), "config_version=5\n")
	timeoutWrite(t, filepath.Join(root, "gdtest.json"), `{"godot":"godot"}`+"\n")
	require.NoError(t, gdtest.InstallRuntime(root))
	timeoutWrite(t, filepath.Join(root, "tests/a.test.gd"), `extends GDTest

func test_unfocused_in_another_file() -> void:
	expect_true(true)
`)
	timeoutWrite(t, filepath.Join(root, "tests/b.test.gd"), `extends GDTest

func test_focused__only() -> void:
	expect_true(true)


func test_unfocused_beside_focus() -> void:
	expect_true(true)
`)

	t.Run("auto runs only the focused case", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var output, diagnostics bytes.Buffer
		code := app.Run(
			ctx,
			[]string{"run", "--no-daemon", "--project", root},
			app.IO{Out: &output, Err: &diagnostics},
		)
		report := output.String()

		assert.Equal(t, app.ExitOK, code, "%s\n%s", report, diagnostics.String())
		assert.Contains(
			t, report, "1 passed",
			"an unfocused case in another file must not run beside a focused one\n%s", report,
		)
		assert.NotContains(
			t, report, "INCOMPLETE",
			"cases suppressed by focus were never selected, so none are missing\n%s", report,
		)
	})

	t.Run("forbid reports the violation once and dispatches nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var output, diagnostics bytes.Buffer
		code := app.Run(
			ctx,
			[]string{"run", "--no-daemon", "--only", "forbid", "--project", root},
			app.IO{Out: &output, Err: &diagnostics},
		)
		report := output.String()

		assert.Equal(t, app.ExitError, code, "%s\n%s", report, diagnostics.String())
		assert.Contains(t, report, "focused tests are forbidden by only mode")
		assert.Contains(
			t, report, "0 passed",
			"a forbidden plan executes nothing rather than part of itself\n%s", report,
		)
	})
}
