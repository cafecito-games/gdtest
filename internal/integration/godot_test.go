package integration_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
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

func TestPersistentGodotWorkerReusesProcess(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot persistent-worker test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	file, err := discovery.Parse(
		"fixtures/godot/basic/passing.test.gd",
		mustRead(t, filepath.Join(root, "fixtures/godot/basic/passing.test.gd")),
	)
	if err != nil {
		t.Fatalf("discover fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-persistent", Generation: 1,
	})
	if err != nil {
		t.Fatalf("start persistent worker: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		if err := process.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown persistent worker: %v", err)
		}
	}()
	pid := process.PID()
	if err := process.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	for index := range 2 {
		finished := 0
		plan := model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "persistent-run-" + string(rune('1'+index)),
			Generation:      1,
			Files:           []model.FilePlan{file},
		}
		err := process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventCaseFinished && event.Status == "pass" {
				finished++
			}
			return nil
		}))
		if err != nil {
			t.Fatalf("run %d: %v", index+1, err)
		}
		if finished != 2 {
			t.Fatalf("run %d finished %d passing cases, want 2", index+1, finished)
		}
		if process.PID() != pid {
			t.Fatalf("worker PID changed from %d to %d", pid, process.PID())
		}
		if err := process.Reset(ctx); err != nil {
			t.Fatalf("reset after run %d: %v", index+1, err)
		}
	}
}

func TestGodotWorkerCancellationKillsOnlyLeaseAndReplacementRuns(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot cancellation test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	slowFile, err := discovery.Parse(
		"fixtures/godot/basic/slow.test.gd",
		mustRead(t, filepath.Join(root, "fixtures/godot/basic/slow.test.gd")),
	)
	if err != nil {
		t.Fatalf("discover slow fixture: %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	process, err := worker.Start(startCtx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-cancel", Generation: 1,
		CancelGrace: 200 * time.Millisecond,
	})
	startCancel()
	if err != nil {
		t.Fatalf("start cancel worker: %v", err)
	}
	runCtx, runCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = process.Run(runCtx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "cancel-run", Generation: 1,
		Files: []model.FilePlan{slowFile},
	}, executor.EventSinkFunc(func(model.Event) error { return nil }))
	runCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled run error = %v, want deadline", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	if err := process.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown canceled worker: %v", err)
	}
	shutdownCancel()

	replacementCtx, replacementCancel := context.WithTimeout(context.Background(), 10*time.Second)
	replacement, err := worker.Start(replacementCtx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-replacement", Generation: 1,
	})
	if err != nil {
		replacementCancel()
		t.Fatalf("start replacement: %v", err)
	}
	if replacement.PID() == process.PID() {
		t.Fatalf("replacement reused killed PID %d", process.PID())
	}
	shutdownCtx, shutdownCancel = context.WithTimeout(context.Background(), 3*time.Second)
	if err := replacement.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown replacement: %v", err)
	}
	shutdownCancel()
	replacementCancel()
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return data
}
