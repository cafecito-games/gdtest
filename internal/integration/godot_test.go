package integration_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/app"
)

func TestForegroundGodotPassingAndFailingFixtures(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot end-to-end test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	t.Run("passing", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, "fixtures/godot/basic/passing.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		if code != app.ExitOK {
			t.Fatalf("exit = %d, output:\n%s\n%s", code, output.String(), diagnostics.String())
		}
		if !strings.Contains(output.String(), "2 passed") {
			t.Fatalf("missing passing summary:\n%s", output.String())
		}
	})

	t.Run("failing", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, "fixtures/godot/basic/failing.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		if code != app.ExitFailures {
			t.Fatalf("exit = %d, output:\n%s\n%s", code, output.String(), diagnostics.String())
		}
		if !strings.Contains(output.String(), "values differ") || !strings.Contains(output.String(), "failing.test.gd:7") {
			t.Fatalf("missing source-located failure:\n%s", output.String())
		}
	})
}
