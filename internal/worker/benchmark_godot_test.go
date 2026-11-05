package worker_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotBenchmarkTaskFakeClockExcludesHooks(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot benchmark fake-clock test skipped: godot is not on PATH")
	}
	project := copyGodotProject(t, repositoryRoot(t))
	runner := filepath.Join(project, "benchmark_fake_clock_runner.gd")
	require.NoError(t, os.WriteFile(runner, []byte(`extends SceneTree

const BenchmarkTask: GDScript = preload("res://addons/gdtest/worker_benchmark_task.gd")

class FakeClock:
	extends RefCounted
	var values: Array[int] = [100, 130, 200, 250]

	func next() -> int:
		return values.pop_front()

class Fixture:
	extends GDTestBenchmark
	var calls: Array[String] = []

	func before_sample(_info: GDTestBenchmarkInfo) -> void:
		calls.append("before")

	func benchmark_work() -> void:
		calls.append("body")

	func after_sample(_info: GDTestBenchmarkInfo) -> void:
		calls.append("after")

func _initialize() -> void:
	_run()

func _run() -> void:
	var fixture := Fixture.new()
	var info := GDTestBenchmarkInfo.new("fake::benchmark_work", "Work", "fake", 1, &"benchmark_work", false, 0, 2)
	var clock := FakeClock.new()
	var task: RefCounted = BenchmarkTask.new()
	task.start(fixture, info, "", clock.next)
	while not task.finished:
		await process_frame
	if task.duration_usec != 80:
		printerr("expected exactly 80 measured usec, got %d" % task.duration_usec)
		quit(1)
		return
	if fixture.calls != ["before", "body", "body", "after"]:
		printerr("benchmark hooks or iterations ran out of order: %s" % [fixture.calls])
		quit(1)
		return
	quit(0)
`), 0o600))
	command := exec.Command("godot", "--headless", "--path", project, "--script", "res://benchmark_fake_clock_runner.gd")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "fake-clock benchmark task:\n%s", output)
}

func TestGodotBenchmarkHooksSamplesAndRuntimeErrors(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot benchmark lifecycle test skipped: godot is not on PATH")
	}
	project := copyGodotProject(t, repositoryRoot(t))
	path := filepath.Join(project, "tests", "lifecycle.bench.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`extends GDTestBenchmark

var calls: Array[String] = []

func before_all() -> void:
	calls.append("before_all")

func before_benchmark(info: GDTestBenchmarkInfo) -> void:
	if info.id.ends_with("benchmark_second"):
		assert(calls.back() == "after_benchmark:first", "after_benchmark did not run before the next benchmark")
	calls.append("before_benchmark:" + info.method_name)

func before_sample(info: GDTestBenchmarkInfo) -> void:
	calls.append("before_sample:%s" % info.warmup)

func after_sample(info: GDTestBenchmarkInfo) -> void:
	calls.append("after_sample:%s" % info.warmup)

func after_benchmark(info: GDTestBenchmarkInfo) -> void:
	calls.append("after_benchmark:" + info.name.to_lower())

func after_all() -> void:
	assert(calls.back() == "after_benchmark:second", "after_all ran out of order")

func benchmark_first() -> void:
	assert(calls[0] == "before_all", "before_all did not run first")

func benchmark_second() -> void:
	assert(calls[0] == "before_all", "before_all state was not retained")
`), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var diagnostics bytes.Buffer
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: project, SessionID: "benchmark-lifecycle", Generation: 1,
		Stderr: &diagnostics,
	})
	require.NoError(t, err)
	defer shutdown(t, process)
	plan := benchmarkPlan("benchmark-lifecycle", "tests/lifecycle.bench.gd", []string{"benchmark_first", "benchmark_second"})
	plan.Warmup = 1
	plan.Samples = 2
	plan.Iterations = 2
	var samples []model.BenchmarkEvent
	var eventTypes []model.BenchmarkEventType
	var plannedBenchmarks any
	var runStatus string
	err = process.Bench(ctx, plan, executor.BenchmarkEventSinkFunc(func(event model.BenchmarkEvent) error {
		eventTypes = append(eventTypes, event.Type)
		if event.Type == model.EventBenchmarkRunStarted {
			plannedBenchmarks = event.Metadata["planned_benchmarks"]
		}
		if event.Type == model.EventBenchmarkRunFinished {
			runStatus = event.Status
		}
		if event.Type == model.EventBenchmarkSample {
			samples = append(samples, event)
		}
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, samples, 6, "events=%v planned=%v status=%s diagnostics=%s", eventTypes, plannedBenchmarks, runStatus, diagnostics.String())
	for _, sample := range samples {
		assert.Equal(t, 2, sample.Iterations)
		assert.GreaterOrEqual(t, sample.DurationNS, int64(0))
	}
	assert.True(t, samples[0].Warmup)
	assert.False(t, samples[1].Warmup)
	require.NoError(t, process.Reset(ctx))

	hookErrorPath := filepath.Join(project, "tests", "hook_error.bench.gd")
	require.NoError(t, os.WriteFile(hookErrorPath, []byte(`extends GDTestBenchmark

func benchmark_ok() -> void:
	pass

func after_all() -> void:
	assert(false, "after_all benchmark hook boom")
`), 0o600))
	hookError := benchmarkPlan("benchmark-hook-error", "tests/hook_error.bench.gd", []string{"benchmark_ok"})
	err = process.Bench(ctx, hookError, executor.BenchmarkEventSinkFunc(func(model.BenchmarkEvent) error {
		return nil
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "after_all benchmark hook boom")
	require.NoError(t, process.Reset(ctx))

	brokenPath := filepath.Join(project, "tests", "runtime_error.bench.gd")
	require.NoError(t, os.WriteFile(brokenPath, []byte(`extends GDTestBenchmark

func benchmark_broken() -> void:
	assert(false, "benchmark runtime boom")
`), 0o600))
	broken := benchmarkPlan("benchmark-runtime-error", "tests/runtime_error.bench.gd", []string{"benchmark_broken"})
	var partialSamples int
	var runtimeFailure model.BenchmarkEvent
	err = process.Bench(ctx, broken, executor.BenchmarkEventSinkFunc(func(event model.BenchmarkEvent) error {
		if event.Type == model.EventBenchmarkSample {
			partialSamples++
		}
		if event.Type == model.EventBenchmarkWorkerError {
			runtimeFailure = event
		}
		return nil
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "benchmark runtime boom")
	assert.Zero(t, partialSamples)
	assert.Equal(t, "tests/runtime_error.bench.gd::benchmark_broken", runtimeFailure.BenchmarkID)
	require.NotNil(t, runtimeFailure.Location)
	assert.Equal(t, "tests/runtime_error.bench.gd", runtimeFailure.Location.File)
}

func TestGodotBenchmarkCancellationInvalidatesActiveSample(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot benchmark cancellation test skipped: godot is not on PATH")
	}
	project := copyGodotProject(t, repositoryRoot(t))
	path := filepath.Join(project, "tests", "cancel.bench.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`extends GDTestBenchmark

func benchmark_wait() -> void:
	await sleep(5.0)
`), 0o600))
	startup, startupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startupCancel()
	process, err := worker.Start(startup, worker.ProcessOptions{
		Godot: "godot", Project: project, SessionID: "benchmark-cancel", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	err = process.Bench(ctx, benchmarkPlan("benchmark-cancel", "tests/cancel.bench.gd", []string{"benchmark_wait"}), executor.BenchmarkEventSinkFunc(func(model.BenchmarkEvent) error {
		return nil
	}))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), err)
}

func benchmarkPlan(runID, filename string, methods []string) model.BenchmarkPlan {
	benchmarks := make([]model.BenchmarkCasePlan, 0, len(methods))
	for _, method := range methods {
		benchmarks = append(benchmarks, model.BenchmarkCasePlan{
			ID: filename + "::" + method, Name: method[len("benchmark_"):], MethodName: method, Line: 1,
		})
	}
	return model.BenchmarkPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: runID, Generation: 1,
		ContentFingerprint: "benchmark-content", Warmup: 0, Samples: 1, Iterations: 1,
		Files: []model.BenchmarkFilePlan{{Path: filename, Benchmarks: benchmarks}},
	}
}
