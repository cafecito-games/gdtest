package worker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/dynamic"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotDynamicDiscoveryAndExactExecution(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot dynamic-discovery test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	conformance := parseFixture(t, root, "fixtures/godot/api/conformance.test.gd")
	focus := parseFixture(t, root, "fixtures/godot/api/focus.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "dynamic-discovery", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "discover",
		Generation: 1, ContentFingerprint: "api-content",
		Files: []model.FilePlan{conformance, focus},
	}
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	identity := dynamic.Identity{
		ToolVersion: "test-build", RuntimeVersion: "test-runtime", GodotIdentity: "godot-test",
		Configuration: []byte(`{"only_mode":"auto"}`), ContentFingerprint: plan.ContentFingerprint,
		InputFingerprint: "fixture-inputs", Generation: plan.Generation,
	}
	computed := 0
	manifest, hit, err := dynamic.LoadOrCompute(ctx, store, identity, func(computeCtx context.Context) (model.DiscoveryManifest, error) {
		computed++
		return process.Discover(computeCtx, plan)
	})
	require.NoError(t, err)
	assert.False(t, hit)
	cached, hit, err := dynamic.LoadOrCompute(ctx, store, identity, nil)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, manifest, cached)
	assert.Equal(t, 1, computed)
	assert.Empty(t, manifest.Errors)
	require.Len(t, manifest.Files, 2)
	require.NotEmpty(t, manifest.Files[0].Cases)
	dynamicID := manifest.Files[0].Cases[0].ID
	assert.Contains(t, dynamicID, "::dynamic@")
	assert.Equal(t, "async case", manifest.Files[0].Cases[0].Name)
	assert.NotEmpty(t, manifest.Files[0].Cases[0].Suites)
	caseByName := make(map[string]model.DiscoveryCase)
	for _, testCase := range manifest.Files[0].Cases {
		caseByName[testCase.Name] = testCase
	}
	require.Contains(t, caseByName, "slow child")
	require.Contains(t, caseByName, "fast child")
	require.Len(t, caseByName["slow child"].Suites, 1)
	require.Len(t, caseByName["fast child"].Suites, 1)
	assert.Equal(t, "slow sibling", caseByName["slow child"].Suites[0].Name)
	assert.Equal(t, "fast sibling", caseByName["fast child"].Suites[0].Name)
	require.Contains(t, caseByName, "first duplicate")
	require.Contains(t, caseByName, "second duplicate")
	assert.NotEqual(
		t, caseByName["first duplicate"].Suites[0].ID,
		caseByName["second duplicate"].Suites[0].ID,
	)
	assert.NotContains(t, dynamicID, root)
	assert.Equal(t, "api-content", manifest.ContentFingerprint)
	copyRoot := copyGodotProject(t, root)
	copyProcess, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: copyRoot, SessionID: "dynamic-discovery-copy", Generation: 1,
	})
	require.NoError(t, err)
	copyManifest, discoverErr := copyProcess.Discover(ctx, plan)
	shutdown(t, copyProcess)
	require.NoError(t, discoverErr)
	assert.Equal(t, manifestCaseIDs(manifest), manifestCaseIDs(copyManifest))
	var dynamicRow model.DiscoveryCase
	for _, testCase := range manifest.Files[0].Cases {
		if strings.Contains(testCase.Name, "dynamic rows [a/b]") {
			dynamicRow = testCase
			break
		}
	}
	require.NotEmpty(t, dynamicRow.ID)
	require.Len(t, dynamicRow.Parameters, 3)
	var sideEffectGuard model.DiscoveryCase
	for _, testCase := range manifest.Files[0].Cases {
		if testCase.MethodName == "test_discovery_has_no_case_side_effects" {
			sideEffectGuard = testCase
			break
		}
	}
	require.Len(t, sideEffectGuard.Parameters, 1)
	assert.Equal(t, false, sideEffectGuard.Parameters[0].Value)
	var staticRow model.DiscoveryCase
	for _, testCase := range manifest.Files[0].Cases {
		if testCase.MethodName == "test_named_provider" && testCase.ParameterName == "a/first" {
			staticRow = testCase
			break
		}
	}
	require.NotEmpty(t, staticRow.ID)
	assert.True(t, strings.HasSuffix(staticRow.ID, "[a%2Ffirst]"))

	plan.RunID = "selected-dynamic"
	plan.SelectionApplied = true
	plan.SelectedIDs = []string{dynamicID}
	plan.OnlyMode = "auto"
	started, diagnostics := run(t, ctx, process, plan)
	assert.Equal(t, []string{dynamicID}, started)
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "selected-dynamic-row"
	plan.SelectedIDs = []string{dynamicRow.ID}
	started, diagnostics = run(t, ctx, process, plan)
	assert.Equal(t, []string{dynamicRow.ID}, started)
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "selected-static-provider-row"
	plan.SelectedIDs = []string{staticRow.ID}
	started, diagnostics = run(t, ctx, process, plan)
	assert.Equal(t, []string{staticRow.ID}, started)
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "selected-empty"
	plan.SelectedIDs = nil
	started, diagnostics = run(t, ctx, process, plan)
	assert.Empty(t, started)
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "focus-auto"
	plan.SelectionApplied = false
	plan.SelectedIDs = nil
	plan.Files = []model.FilePlan{focus}
	started, diagnostics = run(t, ctx, process, plan)
	require.Len(t, started, 1)
	assert.Contains(t, started[0], "test_focused_method[0]")
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "focus-ignore"
	plan.OnlyMode = "ignore"
	started, diagnostics = run(t, ctx, process, plan)
	assert.Len(t, started, 2)
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "focus-forbid"
	plan.OnlyMode = "forbid"
	started, diagnostics = run(t, ctx, process, plan)
	assert.Empty(t, started)
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0], "forbidden")
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "focus-required-present"
	plan.OnlyMode = "require"
	started, diagnostics = run(t, ctx, process, plan)
	require.Len(t, started, 1)
	assert.Contains(t, started[0], "test_focused_method[0]")
	assert.Empty(t, diagnostics)
	require.NoError(t, process.Reset(ctx))

	plan.RunID = "focus-required-missing"
	plan.Files = []model.FilePlan{conformance}
	started, diagnostics = run(t, ctx, process, plan)
	assert.Empty(t, started)
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0], "requires at least one focused")
	require.NoError(t, process.Reset(ctx))

	invalid := parseFixture(t, root, "fixtures/godot/api/discovery_invalid.test.gd")
	invalidManifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "invalid-discovery", Generation: 1,
		ContentFingerprint: "invalid-content", Files: []model.FilePlan{invalid},
	})
	require.NoError(t, err)
	require.Len(t, invalidManifest.Errors, 3)
	var invalidMessages []string
	for _, discoveryError := range invalidManifest.Errors {
		assert.Equal(t, "worker.discovery", discoveryError.Code)
		require.NotNil(t, discoveryError.Location)
		assert.Equal(t, "fixtures/godot/api/discovery_invalid.test.gd", discoveryError.Location.File)
		invalidMessages = append(invalidMessages, discoveryError.Message)
	}
	assert.Contains(t, invalidMessages, "expect is only valid while a case is executing")
}

func TestGodotOutcomeMessagesCrossTheProtocolBoundary(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot outcome test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/outcomes.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "outcome-messages", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "outcomes", Generation: 1,
		ContentFingerprint: "outcome-content", Files: []model.FilePlan{file},
	}
	manifest, err := process.Discover(ctx, plan)
	require.NoError(t, err)
	for _, testCase := range manifest.Files[0].Cases {
		plan.SelectedIDs = append(plan.SelectedIDs, testCase.ID)
	}
	plan.SelectionApplied = true
	require.NoError(t, process.Reset(ctx))

	finished := make(map[string]model.Event)
	var outputEvents []model.Event
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			finished[event.TestID] = event
		}
		if event.Type == model.EventOutput && strings.Contains(event.Message, "gdtest-output-marker") {
			outputEvents = append(outputEvents, event)
		}
		return nil
	}))
	require.NoError(t, err)

	skip := finished["fixtures/godot/api/outcomes.test.gd::test_skip_outcome"]
	assert.Equal(t, "skip", skip.Status)
	require.Len(t, skip.Outcomes, 1)
	assert.Equal(t, "skip", skip.Outcomes[0].Kind)
	assert.Equal(t, "not available", skip.Outcomes[0].Message)
	assert.Equal(t, "dictionary", skip.Outcomes[0].Data.Type)
	void := finished["fixtures/godot/api/outcomes.test.gd::test_void_outcome"]
	assert.Equal(t, "void", void.Status)
	assert.Equal(t, []string{"void remains observable"}, void.Messages)
	require.NotEmpty(t, outputEvents)
	assert.Empty(t, outputEvents[0].TestID)
	assert.Equal(t, "outcomes", outputEvents[0].RunID)
}

func TestGodotRuntimeErrorsAreSourceLocatedCaseErrors(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot runtime-error test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/runtime_errors.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "runtime-errors", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "runtime-errors", Generation: 1,
		ContentFingerprint: "runtime-errors-content", Files: []model.FilePlan{file},
		SelectionApplied: true,
		SelectedIDs: []string{
			"fixtures/godot/api/runtime_errors.test.gd::test_builtin_assert_error",
			"fixtures/godot/api/runtime_errors.test.gd::test_runtime_error",
			"fixtures/godot/api/runtime_errors.test.gd::test_worker_continues_after_error",
		},
	}
	finished := make(map[string]model.Event)
	var runFinished model.Event
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseFinished {
			finished[event.TestID] = event
		}
		if event.Type == model.EventRunFinished {
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)

	builtIn := finished[plan.SelectedIDs[0]]
	assert.Equal(t, "error", builtIn.Status)
	assert.Contains(t, builtIn.Message, "gdtest built-in assertion marker")
	require.NotNil(t, builtIn.Location)
	assert.Equal(t, "fixtures/godot/api/runtime_errors.test.gd", builtIn.Location.File)
	assert.Equal(t, 6, builtIn.Location.Line)
	runtimeError := finished[plan.SelectedIDs[1]]
	assert.Equal(t, "error", runtimeError.Status)
	assert.Contains(t, runtimeError.Message, "Out of bounds")
	require.NotNil(t, runtimeError.Location)
	assert.Equal(t, "fixtures/godot/api/runtime_errors.test.gd", runtimeError.Location.File)
	assert.Equal(t, 12, runtimeError.Location.Line)
	assert.Equal(t, "pass", finished[plan.SelectedIDs[2]].Status)
	assert.Equal(t, "error", runFinished.Status)
	require.NoError(t, process.Reset(ctx), "ordinary script errors must not poison the worker")
}

func TestGodotAsyncHelperErrorContractsAreSourceLocated(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot async-helper test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/helpers.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "async-helper-contracts", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	expectedErrors := map[string]string{
		"test_sleep_rejects_negative_duration":   "sleep duration must be finite and non-negative",
		"test_sleep_rejects_non_finite_duration": "sleep duration must be finite and non-negative",
		"test_until_rejects_negative_timeout":    "until timeout must be finite and non-negative",
		"test_until_rejects_non_finite_timeout":  "until timeout must be finite and non-negative",
		"test_until_rejects_negative_interval":   "until interval must be finite and non-negative",
		"test_until_rejects_non_finite_interval": "until interval must be finite and non-negative",
	}
	finished := make(map[string]model.Event)
	failures := make(map[string][]model.Event)
	var runFinished model.Event
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "async-helper-contracts", Generation: 1,
		ContentFingerprint: "async-helper-contracts", Files: []model.FilePlan{file},
	}, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventAssertionFail:
			failures[event.TestID] = append(failures[event.TestID], event)
		case model.EventCaseFinished:
			finished[event.TestID] = event
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	zeroTimeoutID := file.Path + "::test_until_zero_timeout_succeeds"
	assert.Equal(t, "pass", finished[zeroTimeoutID].Status)
	assert.Empty(t, failures[zeroTimeoutID])
	for method, message := range expectedErrors {
		id := file.Path + "::" + method
		assert.Equal(t, "error", finished[id].Status)
		require.Len(t, failures[id], 1)
		failure := failures[id][0]
		assert.Equal(t, "framework", failure.Assertion)
		assert.Equal(t, message, failure.Message)
		require.NotNil(t, failure.Location)
		assert.Equal(t, file.Path, failure.Location.File)
		assert.Positive(t, failure.Location.Line)
	}
	assert.Equal(t, "error", runFinished.Status)
	require.NoError(t, process.Reset(ctx), "helper input errors must not poison the worker")
}

func TestGodotCaseTimeoutIsAnErrorAndTaintsTheWorker(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot case-timeout test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/runtime_errors.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "case-timeout", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	timeoutID := "fixtures/godot/api/runtime_errors.test.gd::test_case_timeout"
	continuationID := "fixtures/godot/api/runtime_errors.test.gd::test_worker_continues_after_error"
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "case-timeout", Generation: 1,
		ContentFingerprint: "case-timeout-content", Files: []model.FilePlan{file},
		CaseTimeout: 100 * time.Millisecond, SelectionApplied: true,
		SelectedIDs: []string{timeoutID, continuationID},
	}
	var timeoutCase model.Event
	var runFinished model.Event
	var tainted model.Event
	started := make([]string, 0, 2)
	startedAt := time.Now()
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseStarted:
			started = append(started, event.TestID)
		case model.EventCaseFinished:
			if event.TestID == timeoutID {
				timeoutCase = event
			}
		case model.EventWorkerTainted:
			tainted = event
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Less(t, time.Since(startedAt), 2*time.Second)
	assert.Equal(t, []string{timeoutID}, started)
	assert.Equal(t, "error", timeoutCase.Status)
	assert.Contains(t, timeoutCase.Message, "timed out")
	require.NotNil(t, timeoutCase.Location)
	assert.Equal(t, "fixtures/godot/api/runtime_errors.test.gd", timeoutCase.Location.File)
	assert.Equal(t, 16, timeoutCase.Location.Line)
	assert.Equal(t, "error", runFinished.Status)
	assert.Equal(t, timeoutID, tainted.TestID)
	assert.Equal(t, "case_timeout", tainted.Metadata["reason"])
}

func TestGodotBusyLoopTimeoutKillsOnlyItsWorkerAndReturnsCaseError(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot busy-loop timeout test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/runtime_errors.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "busy-loop-timeout", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	timeoutID := "fixtures/godot/api/runtime_errors.test.gd::test_busy_loop_timeout"
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "busy-loop-timeout", Generation: 1,
		ContentFingerprint: "busy-loop-content", Files: []model.FilePlan{file},
		CaseTimeout: 100 * time.Millisecond, SelectionApplied: true,
		SelectedIDs: []string{timeoutID},
	}
	var timeoutCase model.Event
	var runFinished model.Event
	var tainted model.Event
	startedAt := time.Now()
	err = process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseFinished:
			timeoutCase = event
		case model.EventWorkerTainted:
			tainted = event
		case model.EventRunFinished:
			runFinished = event
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Less(t, time.Since(startedAt), 2*time.Second)
	assert.Equal(t, timeoutID, timeoutCase.TestID)
	assert.Equal(t, "error", timeoutCase.Status)
	assert.Contains(t, timeoutCase.Message, "timed out")
	require.NotNil(t, timeoutCase.Location)
	assert.Equal(t, "fixtures/godot/api/runtime_errors.test.gd", timeoutCase.Location.File)
	assert.Equal(t, 21, timeoutCase.Location.Line)
	assert.Equal(t, timeoutID, tainted.TestID)
	assert.Equal(t, "case_timeout", tainted.Metadata["reason"])
	assert.Equal(t, "error", runFinished.Status)
}

func TestGodotDiscoveryReportsNonInstantiableScriptAtItsSource(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot script-load test skipped: godot is not on PATH")
	}
	root := copyGodotProject(t, repositoryRoot(t))
	path := filepath.Join(root, "tests", "compile_error.test.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(
		"extends GDTest\n\nfunc test_bad() -> void:\n\tvar value: MissingType\n",
	), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "script-load-error", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "script-load-error", Generation: 1,
		ContentFingerprint: "script-load-content",
		Files:              []model.FilePlan{{Path: "tests/compile_error.test.gd"}},
	})

	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	assert.Equal(t, "tests/compile_error.test.gd", manifest.Files[0].Path)
	require.Len(t, manifest.Errors, 1)
	assert.Equal(t, "worker.script_load", manifest.Errors[0].Code)
	require.NotNil(t, manifest.Errors[0].Location)
	assert.Equal(t, "tests/compile_error.test.gd", manifest.Errors[0].Location.File)
}

func run(
	t *testing.T,
	ctx context.Context,
	process *worker.Process,
	plan model.TestPlan,
) ([]string, []string) {
	t.Helper()
	var started []string
	var diagnostics []string
	err := process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseStarted:
			started = append(started, event.TestID)
		case model.EventWorkerError:
			diagnostics = append(diagnostics, event.Message)
		}
		return nil
	}))
	require.NoError(t, err)
	return started, diagnostics
}

func parseFixture(t *testing.T, root, relative string) model.FilePlan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	file, err := discovery.Parse(relative, data)
	require.NoError(t, err)
	return file
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func copyGodotProject(t *testing.T, root string) string {
	t.Helper()
	destination := t.TempDir()
	addonFiles, err := filepath.Glob(filepath.Join(root, "addons", "gdtest", "*"))
	require.NoError(t, err)
	files := append([]string{filepath.Join(root, "project.godot")}, addonFiles...)
	files = append(files,
		filepath.Join(root, "fixtures", "godot", "api", "conformance.test.gd"),
		filepath.Join(root, "fixtures", "godot", "api", "conformance.test.gd.uid"),
		filepath.Join(root, "fixtures", "godot", "api", "focus.test.gd"),
		filepath.Join(root, "fixtures", "godot", "api", "focus.test.gd.uid"),
	)
	for _, source := range files {
		info, statErr := os.Stat(source)
		require.NoError(t, statErr)
		if info.IsDir() {
			continue
		}
		relative, relativeErr := filepath.Rel(root, source)
		require.NoError(t, relativeErr)
		target := filepath.Join(destination, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		data, readErr := os.ReadFile(source)
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(target, data, 0o600))
	}
	bootstrap := exec.Command("godot", "--headless", "--editor", "--path", destination, "--quit")
	output, err := bootstrap.CombinedOutput()
	require.NoErrorf(t, err, "bootstrap copied Godot project:\n%s", output)
	return destination
}

func manifestCaseIDs(manifest model.DiscoveryManifest) []string {
	var ids []string
	for _, file := range manifest.Files {
		for _, testCase := range file.Cases {
			ids = append(ids, testCase.ID)
		}
	}
	return ids
}

func shutdown(t *testing.T, process *worker.Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	assert.NoError(t, process.Shutdown(ctx))
}

func TestDiscoveryManifestIDsAreProjectRelative(t *testing.T) {
	t.Parallel()
	manifest := model.DiscoveryManifest{Files: []model.DiscoveryFile{{
		Path:  "tests/example.test.gd",
		Cases: []model.DiscoveryCase{{ID: "tests/example.test.gd::dynamic@4:0[case]"}},
	}}}
	for _, file := range manifest.Files {
		assert.False(t, filepath.IsAbs(file.Path))
		for _, testCase := range file.Cases {
			assert.False(t, strings.HasPrefix(testCase.ID, "/"))
		}
	}
}
