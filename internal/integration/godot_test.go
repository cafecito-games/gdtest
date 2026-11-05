package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForegroundGodotPassingAndFailingFixtures(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot end-to-end test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	t.Run("passing", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, "fixtures/godot/basic/passing.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		assert.Equal(t, app.ExitOK, code, "output:\n%s\n%s", output.String(), diagnostics.String())
		assert.Contains(t, output.String(), "2 passed")
	})

	t.Run("failing", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, "fixtures/godot/basic/failing.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		assert.Equal(t, app.ExitFailures, code, "output:\n%s\n%s", output.String(), diagnostics.String())
		assert.Contains(t, output.String(), "values differ")
		assert.Contains(t, output.String(), "failing.test.gd:7")
	})

	t.Run("invalid discovery", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, "fixtures/godot/api/discovery_invalid.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		assert.Equal(t, app.ExitError, code, "output:\n%s\n%s", output.String(), diagnostics.String())
		assert.Contains(t, output.String(), "duplicate row")
		assert.Contains(t, output.String(), "only valid while a case is executing")
		assert.Contains(t, output.String(), "get_suite_name")
		assert.Contains(t, output.String(), "3 errors")
	})
}

func TestSourceChangeDuringRealRunIsStaleAndReplacementLoadsChangedBody(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot generation test skipped: godot is not on PATH")
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte(
		"config_version=5\n\n[application]\nconfig/name=\"gdtest generation fixture\"\n",
	), 0o600))
	require.NoError(t, gdtest.InstallRuntime(root))
	const initial = "extends GDTest\n\nfunc test_generation() -> void:\n\tawait sleep(0.05)\n\texpect_true(true)\n"
	const changed = "extends GDTest\n\nfunc test_generation() -> void:\n\tvar body_only: int = 1\n\tawait sleep(0.05)\n\texpect_equal(body_only, 1)\n"
	filename := filepath.Join(root, "generation.test.gd")
	require.NoError(t, os.WriteFile(filename, []byte(initial), 0o600))
	file, err := discovery.Parse("generation.test.gd", []byte(initial))
	require.NoError(t, err)
	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
	bootstrap := exec.CommandContext(
		bootstrapCtx, "godot", "--headless", "--editor", "--path", root, "--quit-after", "2",
	)
	bootstrapOutput, err := bootstrap.CombinedOutput()
	bootstrapCancel()
	require.NoError(t, err, "bootstrap output:\n%s", bootstrapOutput)

	sessions := service.NewSessionServiceWithOptions(service.ServiceOptions{MaxWorkers: 1})
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		assert.NoError(t, sessions.Close(closeCtx))
	}()
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "real-stale-generation",
		Generation:      1,
		Files:           []model.FilePlan{file},
	}
	payload, err := json.Marshal(service.RunRequest{
		SessionID: "real-generation-session", Project: root, Godot: "godot", Plan: plan,
	})
	require.NoError(t, err)
	var finished model.Event
	changedSource := false
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = sessions.Handle(ctx, daemon.Request{
		ID: "run", Method: "session.run", Payload: payload,
	}, func(value any) error {
		event, ok := value.(model.Event)
		if !ok {
			return fmt.Errorf("daemon event has type %T, want model.Event", value)
		}
		if event.Type == model.EventCaseStarted && !changedSource {
			changedSource = true
			if writeErr := os.WriteFile(filename, []byte(changed), 0o600); writeErr != nil {
				return writeErr
			}
		}
		if event.Type == model.EventRunFinished {
			finished = event
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, changedSource)
	assert.True(t, finished.Stale)
	assert.Equal(t, uint64(1), finished.Metadata["requested_generation"])
	assert.Equal(t, uint64(2), finished.Metadata["current_generation"])
	before := sessions.List()
	require.Len(t, before, 1)

	warmPayload, err := json.Marshal(service.WarmRequest{
		SessionID: "real-generation-session", Project: root, Godot: "godot", Generation: 1,
	})
	require.NoError(t, err)
	_, err = sessions.Handle(ctx, daemon.Request{
		ID: "warm", Method: "session.warm", Payload: warmPayload,
	}, nil)
	require.NoError(t, err)
	after := sessions.List()
	require.Len(t, after, 1)
	assert.NotEqual(t, before[0].WorkerPID, after[0].WorkerPID)
	assert.Equal(t, uint64(2), after[0].Generation)

	plan.RunID = "changed-body-run"
	plan.Generation = 2
	payload, err = json.Marshal(service.RunRequest{
		SessionID: "real-generation-session", Project: root, Godot: "godot", Plan: plan,
	})
	require.NoError(t, err)
	statuses := make(map[string]string)
	_, err = sessions.Handle(ctx, daemon.Request{
		ID: "changed-run", Method: "session.run", Payload: payload,
	}, func(value any) error {
		event, ok := value.(model.Event)
		if !ok {
			return fmt.Errorf("daemon event has type %T, want model.Event", value)
		}
		if event.Type == model.EventCaseFinished {
			statuses[event.TestID] = event.Status
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "pass", statuses["generation.test.gd::test_generation"])
}

func TestPersistentGodotWorkerReusesProcess(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot persistent-worker test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	file, err := discovery.Parse(
		"fixtures/godot/basic/passing.test.gd",
		mustRead(t, filepath.Join(root, "fixtures/godot/basic/passing.test.gd")),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-persistent", Generation: 1,
	})
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		assert.NoError(t, process.Shutdown(shutdownCtx))
	}()
	pid := process.PID()
	require.NoError(t, process.Ping(ctx))
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
		require.NoError(t, err)
		assert.Equal(t, 2, finished)
		assert.Equal(t, pid, process.PID())
		require.NoError(t, process.Reset(ctx))
	}
	invalidFile, err := discovery.Parse(
		"fixtures/godot/api/discovery_invalid.test.gd",
		mustRead(t, filepath.Join(root, "fixtures/godot/api/discovery_invalid.test.gd")),
	)
	require.NoError(t, err)
	diagnostics := 0
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "persistent-discovery-error",
		Generation:      1,
		Files:           []model.FilePlan{invalidFile},
	}, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventWorkerError {
			diagnostics++
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, 3, diagnostics)
	require.NoError(t, process.Reset(ctx))
	require.NoError(t, process.Ping(ctx))
}

func TestGodotWorkerCancellationKillsOnlyLeaseAndReplacementRuns(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot cancellation test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	slowFile, err := discovery.Parse(
		"fixtures/godot/basic/slow.test.gd",
		mustRead(t, filepath.Join(root, "fixtures/godot/basic/slow.test.gd")),
	)
	require.NoError(t, err)
	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	process, err := worker.Start(startCtx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-cancel", Generation: 1,
		CancelGrace: 200 * time.Millisecond,
	})
	startCancel()
	require.NoError(t, err)
	runCtx, runCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = process.Run(runCtx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "cancel-run", Generation: 1,
		Files: []model.FilePlan{slowFile},
	}, executor.EventSinkFunc(func(model.Event) error { return nil }))
	runCancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	require.NoError(t, process.Shutdown(shutdownCtx))
	shutdownCancel()

	replacementCtx, replacementCancel := context.WithTimeout(context.Background(), 10*time.Second)
	replacement, err := worker.Start(replacementCtx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "integration-replacement", Generation: 1,
	})
	require.NoError(t, err)
	assert.NotEqual(t, process.PID(), replacement.PID())
	shutdownCtx, shutdownCancel = context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, replacement.Shutdown(shutdownCtx))
	shutdownCancel()
	replacementCancel()
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	require.NoError(t, err, "read %s", filename)
	return data
}

func lineContaining(t *testing.T, root, relative, snippet string) int {
	t.Helper()
	lines := strings.Split(string(mustRead(t, filepath.Join(root, filepath.FromSlash(relative)))), "\n")
	found := 0
	line := 0
	for index, text := range lines {
		if strings.Contains(text, snippet) {
			found++
			line = index + 1
		}
	}
	require.Equal(t, 1, found, "snippet %q must occur exactly once in %s", snippet, relative)
	return line
}

func TestExecutionHookMisuseReportsSourceLocatedFrameworkErrorsAndExitsFailures(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot execution hook misuse test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	relative := "fixtures/godot/api/hook_misuse.test.gd"
	beforeSuiteLine := lineContaining(t, root, relative, `expect_true(true, "blocked before_suite assertion")`)
	afterAllLine := lineContaining(t, root, relative, `fail("after_all explicit fail")`)
	defineInCaseLine := lineContaining(t, root, relative, `define("illegal suite"`)

	t.Run("foreground terminal", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--project", root, relative,
		}, app.IO{Out: &output, Err: &diagnostics})
		assert.Equal(t, app.ExitFailures, code, "output:\n%s\n%s", output.String(), diagnostics.String())
		assert.Empty(t, diagnostics.String())
		assert.Contains(
			t,
			output.String(),
			fmt.Sprintf("ERROR worker.framework (%s:%d): expect is only valid while a case is executing", relative, beforeSuiteLine),
		)
		assert.Contains(
			t,
			output.String(),
			fmt.Sprintf("ERROR worker.framework (%s:%d): fail is only valid while a case is executing", relative, afterAllLine),
		)
		assert.Contains(
			t,
			output.String(),
			fmt.Sprintf("(%s:%d): define is only valid during suite registration", relative, defineInCaseLine),
		)
		assert.Contains(t, output.String(), "2 passed, 0 failed, 12 errors, 0 skipped, 0 todo, 0 void, 0 canceled")
	})

	t.Run("foreground json", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		code := app.Run(ctx, []string{
			"run", "--no-daemon", "--json", "--project", root, "fixtures/godot/api/hook_misuse.test.gd",
		}, app.IO{Out: &output, Err: &diagnostics})
		assert.Equal(t, app.ExitFailures, code, "output:\n%s\n%s", output.String(), diagnostics.String())
		assert.Empty(t, diagnostics.String())

		var summary *results.Summary
		var workerErrorLocations []string
		for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			var envelope struct {
				Type     string           `json:"type"`
				Name     string           `json:"name"`
				Location *model.Location  `json:"location"`
				Summary  *results.Summary `json:"summary"`
			}
			require.NoError(t, json.Unmarshal([]byte(line), &envelope))
			if envelope.Type == string(model.EventWorkerError) {
				assert.Equal(t, "worker.framework", envelope.Name)
				require.NotNil(t, envelope.Location)
				workerErrorLocations = append(workerErrorLocations, envelope.Location.File)
			}
			if envelope.Type == "summary" {
				summary = envelope.Summary
			}
		}
		require.Len(t, workerErrorLocations, 10)
		for _, locationFile := range workerErrorLocations {
			assert.Equal(t, "fixtures/godot/api/hook_misuse.test.gd", locationFile)
		}
		require.NotNil(t, summary)
		assert.Equal(t, 2, summary.Passed)
		assert.Equal(t, 12, summary.Errored)
		assert.Len(t, summary.Errors, 10)
		assert.Empty(t, summary.Infrastructure)
	})

	t.Run("multi-file session run", func(t *testing.T) {
		passingFile, err := discovery.Parse(
			"fixtures/godot/basic/passing.test.gd",
			mustRead(t, filepath.Join(root, "fixtures/godot/basic/passing.test.gd")),
		)
		require.NoError(t, err)
		hookFile, err := discovery.Parse(
			"fixtures/godot/api/callback_probe_suite_hook.test.gd",
			mustRead(t, filepath.Join(root, "fixtures/godot/api/callback_probe_suite_hook.test.gd")),
		)
		require.NoError(t, err)

		sessions := service.NewSessionServiceWithOptions(service.ServiceOptions{MaxWorkers: 2})
		defer func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer closeCancel()
			assert.NoError(t, sessions.Close(closeCtx))
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		warmPayload, err := json.Marshal(service.WarmRequest{
			SessionID: "hook-misuse-multi-file", Project: root, Godot: "godot", Generation: 1,
		})
		require.NoError(t, err)
		_, err = sessions.Handle(ctx, daemon.Request{
			ID: "warm", Method: "session.warm", Payload: warmPayload,
		}, nil)
		require.NoError(t, err)
		listed := sessions.List()
		require.Len(t, listed, 1)

		runPayload, err := json.Marshal(service.RunRequest{
			SessionID: "hook-misuse-multi-file",
			Project:   root,
			Godot:     "godot",
			Jobs:      2,
			Plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion,
				RunID:           "multi-file-hook-misuse",
				Generation:      listed[0].Generation,
				Files:           []model.FilePlan{passingFile, hookFile},
			},
		})
		require.NoError(t, err)

		collector := results.NewCollector()
		var runFinished model.Event
		_, err = sessions.Handle(ctx, daemon.Request{
			ID: "multi-file-hook-misuse", Method: "session.run", Payload: runPayload,
		}, func(value any) error {
			event, valid := value.(model.Event)
			if !valid {
				return fmt.Errorf("unexpected event %T", value)
			}
			if event.Type == model.EventRunFinished {
				runFinished = event
			}
			return collector.Emit(event)
		})
		require.NoError(t, err)
		assert.Equal(t, "error", runFinished.Status)
		summary := collector.Summary()
		assert.Equal(t, 2, summary.Passed)
		assert.Equal(t, 1, summary.Errored)
		assert.Len(t, summary.Errors, 1)
		assert.Empty(t, summary.Infrastructure)
	})
}
