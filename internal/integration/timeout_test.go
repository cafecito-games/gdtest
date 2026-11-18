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

// A case timeout retires its worker. It must cost that worker and the rest of
// its own file, never the files queued behind it, and whatever it does cost
// has to appear in the counts and in the exit status.
func TestCLICaseTimeoutDoesNotTruncateTheRemainingFiles(t *testing.T) {
	requireGodot(t, "case-timeout truncation test")

	root := t.TempDir()
	timeoutWrite(t, filepath.Join(root, "project.godot"), "config_version=5\n")
	timeoutWrite(t, filepath.Join(root, "gdtest.json"), `{"godot":"godot","case_timeout":"2s"}`+"\n")
	require.NoError(t, gdtest.InstallRuntime(root))
	timeoutWrite(t, filepath.Join(root, "tests/a.test.gd"), `extends GDTest

func test_first() -> void:
	expect_true(true)
`)
	// The busy loop never yields, so the in-worker deadline cannot fire and
	// the host watchdog has to kill the process — the harsher of the two
	// timeout paths, and the one CPU contention provokes.
	timeoutWrite(t, filepath.Join(root, "tests/b.test.gd"), `extends GDTest

func test_hangs() -> void:
	var spins: int = 0
	var deadline_usec: int = Time.get_ticks_usec() + 60000000
	while Time.get_ticks_usec() < deadline_usec:
		spins += 1
	expect_true(spins > 0)


func test_after_the_hang() -> void:
	expect_true(true)
`)
	timeoutWrite(t, filepath.Join(root, "tests/c.test.gd"), `extends GDTest

func test_third() -> void:
	expect_true(true)


func test_fourth() -> void:
	expect_true(true)
`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code := app.Run(
		ctx,
		[]string{"run", "--no-daemon", "--project", root},
		app.IO{Out: &output, Err: &diagnostics},
	)
	report := output.String()

	assert.Equal(
		t, app.ExitFailures, code,
		"a timed-out case is a test failure, never an infrastructure retry\n%s\n%s",
		report, diagnostics.String(),
	)
	assert.Contains(
		t, report, "3 passed",
		"both files after the tainting file still execute\n%s", report,
	)
	assert.Contains(
		t, report, "1 errors",
		"the timed-out case is reported as that case's error\n%s", report,
	)
	assert.Contains(
		t, report, "executed 4 of 5 selected cases",
		"the summary accounts for every selected case\n%s", report,
	)
	assert.Contains(
		t, report, "NOT RUN tests/b.test.gd::test_after_the_hang",
		"the case the retired worker skipped is named\n%s", report,
	)
}

func timeoutWrite(t *testing.T, name, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, []byte(contents), 0o600))
}

func requireGodot(t *testing.T, label string) {
	t.Helper()
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skipf("%s skipped: godot is not on PATH", label)
	}
}
