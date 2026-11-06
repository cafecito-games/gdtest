package worker_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type subsetRunEvents struct {
	startedCases   []string
	finishedStates map[string]string
	workerErrors   []string
	assertionFails int
	finishedStatus string
}

func collectSubsetRunEvents() (*subsetRunEvents, executor.EventSink) {
	events := &subsetRunEvents{finishedStates: map[string]string{}}
	return events, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseStarted:
			events.startedCases = append(events.startedCases, event.TestID)
		case model.EventCaseFinished:
			events.finishedStates[event.TestID] = event.Status
		case model.EventWorkerError:
			events.workerErrors = append(events.workerErrors, event.Message)
		case model.EventAssertionFail:
			events.assertionFails++
		case model.EventRunFinished:
			events.finishedStatus = event.Status
		}
		return nil
	})
}

func focusPointer(value bool) *bool {
	return &value
}

func discoverManifestCases(
	t *testing.T,
	process *worker.Process,
	ctx context.Context,
	file model.FilePlan,
) []model.DiscoveryCase {
	t.Helper()
	manifest, err := process.Discover(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "subset-discover",
		ContentFingerprint: "subset-discovery",
		Generation:         1,
		Files:              []model.FilePlan{file},
	})
	require.NoError(t, err)
	require.Empty(t, manifest.Errors)
	require.Len(t, manifest.Files, 1)
	require.NotEmpty(t, manifest.Files[0].Cases)
	require.NoError(t, process.Reset(ctx))
	return manifest.Files[0].Cases
}

func caseIDs(cases []model.DiscoveryCase) []string {
	ids := make([]string, 0, len(cases))
	for _, testCase := range cases {
		ids = append(ids, testCase.ID)
	}
	return ids
}

func caseIDByName(cases []model.DiscoveryCase, name string) string {
	for _, testCase := range cases {
		if testCase.Name == name {
			return testCase.ID
		}
	}
	return ""
}

func manifestOrderSubset(allIDs []string, allowed map[string]bool) []string {
	expected := make([]string, 0, len(allowed))
	for _, id := range allIDs {
		if allowed[id] {
			expected = append(expected, id)
		}
	}
	return expected
}

func TestPerFileCaseSubsetRunsExactlyThePlannedCases(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot per-file case subset test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	conformance := parseFixture(t, root, "fixtures/godot/api/conformance.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "per-file-subset", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	allCases := discoverManifestCases(t, process, ctx, conformance)
	allIDs := caseIDs(allCases)
	dynamicID := caseIDByName(allCases, "async case")
	parameterizedRowID := caseIDByName(allCases, "Named provider [a/first]")
	staticMethodID := caseIDByName(allCases, "Assertion surface")
	require.NotEmpty(t, dynamicID, "fixture must expose a dynamic case")
	require.NotEmpty(t, parameterizedRowID, "fixture must expose a parameterized row")
	require.NotEmpty(t, staticMethodID, "fixture must expose a static method case")

	events, sink := collectSubsetRunEvents()
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "per-file-subset-run",
		Generation:         1,
		ContentFingerprint: "subset-discovery",
		Files: []model.FilePlan{
			{Path: conformance.Path, Cases: conformance.Cases, CaseIDs: []string{dynamicID, parameterizedRowID}},
			{Path: conformance.Path, Cases: conformance.Cases, CaseIDs: []string{staticMethodID}},
		},
	}, sink)
	require.NoError(t, err)

	expectedOrder := append(
		manifestOrderSubset(allIDs, map[string]bool{dynamicID: true, parameterizedRowID: true}),
		manifestOrderSubset(allIDs, map[string]bool{staticMethodID: true})...,
	)
	assert.Equal(t, expectedOrder, events.startedCases, "each entry runs its subset in discovery order")
	assert.Equal(t, "pass", events.finishedStates[dynamicID])
	assert.Equal(t, "pass", events.finishedStates[parameterizedRowID])
	assert.Equal(t, "pass", events.finishedStates[staticMethodID])
	assert.Len(t, events.finishedStates, len(expectedOrder), "filtered-out cases must not finish")
	assert.Empty(t, events.workerErrors)
	assert.Equal(t, "pass", events.finishedStatus)
}

func TestPerFileCaseSubsetIntersectsSelection(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot per-file selection intersection test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	conformance := parseFixture(t, root, "fixtures/godot/api/conformance.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "subset-intersection", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	allCases := discoverManifestCases(t, process, ctx, conformance)
	allIDs := caseIDs(allCases)
	dynamicID := caseIDByName(allCases, "async case")
	parameterizedRowID := caseIDByName(allCases, "Named provider [a/first]")
	staticMethodID := caseIDByName(allCases, "Assertion surface")
	require.NotEmpty(t, dynamicID)
	require.NotEmpty(t, parameterizedRowID)
	require.NotEmpty(t, staticMethodID)

	events, sink := collectSubsetRunEvents()
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "subset-intersection-run",
		Generation:         1,
		ContentFingerprint: "subset-discovery",
		SelectionApplied:   true,
		SelectedIDs:        []string{parameterizedRowID, staticMethodID},
		Files: []model.FilePlan{{
			Path: conformance.Path, Cases: conformance.Cases,
			CaseIDs: []string{dynamicID, parameterizedRowID, staticMethodID},
		}},
	}, sink)
	require.NoError(t, err)

	expectedOrder := manifestOrderSubset(allIDs, map[string]bool{parameterizedRowID: true, staticMethodID: true})
	assert.Equal(t, expectedOrder, events.startedCases, "a case must pass both case_ids and selected_ids")
	assert.NotContains(t, events.startedCases, dynamicID, "selection must exclude cases outside selected_ids")
	assert.Equal(t, "pass", events.finishedStatus)
}

func TestPerFileCaseSubsetStampedFocusOverridesWorkerComputation(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot stamped focus test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	focus := parseFixture(t, root, "fixtures/godot/api/focus.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "stamped-focus", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	allCases := discoverManifestCases(t, process, ctx, focus)
	var unfocusedID, focusedRowID string
	for _, testCase := range allCases {
		if strings.HasSuffix(testCase.ID, "::test_unfocused_case") {
			unfocusedID = testCase.ID
		}
		if strings.Contains(testCase.ID, "[") {
			focusedRowID = testCase.ID
		}
	}
	require.NotEmpty(t, unfocusedID, "fixture must expose an unfocused case")
	require.NotEmpty(t, focusedRowID, "fixture must expose a focused parameterized row")

	events, sink := collectSubsetRunEvents()
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "stamped-focus-run",
		Generation:         1,
		ContentFingerprint: "subset-discovery",
		OnlyMode:           "auto",
		HasFocus:           focusPointer(true),
		Files: []model.FilePlan{{
			Path: focus.Path, Cases: focus.Cases, CaseIDs: []string{unfocusedID},
		}},
	}, sink)
	require.NoError(t, err)
	assert.Empty(t, events.startedCases, "stamped focus must suppress the unfocused case even when the shard holds no only case")
	assert.Zero(t, events.assertionFails, "the suppressed case body must not run")
	assert.Empty(t, events.workerErrors)
	assert.Equal(t, "pass", events.finishedStatus)

	require.NoError(t, process.Reset(ctx))
	events, sink = collectSubsetRunEvents()
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "stamped-focus-policy-run",
		Generation:         1,
		ContentFingerprint: "subset-discovery",
		OnlyMode:           "require",
		HasFocus:           focusPointer(false),
		Files: []model.FilePlan{{
			Path: focus.Path, Cases: focus.Cases, CaseIDs: []string{focusedRowID},
		}},
	}, sink)
	require.NoError(t, err)
	assert.Equal(t, []string{"only mode requires at least one focused selected case"}, events.workerErrors)
	assert.Empty(t, events.startedCases, "policy errors must skip execution")
	assert.Equal(t, "error", events.finishedStatus)
}

func TestPerFileCaseSubsetEmptyShardRunsNoHooks(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot empty shard test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	hookMisuse := parseFixture(t, root, "fixtures/godot/api/hook_misuse.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "empty-shard", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	events, sink := collectSubsetRunEvents()
	err = process.Run(ctx, model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              "empty-shard-run",
		Generation:         1,
		ContentFingerprint: "empty-shard",
		Files:              []model.FilePlan{{Path: hookMisuse.Path, Cases: hookMisuse.Cases, CaseIDs: []string{}}},
	}, sink)
	require.NoError(t, err)
	assert.Empty(t, events.startedCases, "an explicit empty case_ids must select nothing")
	assert.Empty(t, events.workerErrors, "no hooks may run for an empty shard, so hook misuse cannot fire")
	assert.Equal(t, "pass", events.finishedStatus)
}
