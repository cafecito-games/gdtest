package app

import (
	"strconv"
	"testing"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/shard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discoveryCases(prefix string, total int, suite string, onlyEvery int) []model.DiscoveryCase {
	cases := make([]model.DiscoveryCase, 0, total)
	for index := range total {
		testCase := model.DiscoveryCase{
			ID:   prefix + "-case-" + strconv.Itoa(index),
			Name: prefix + " case " + strconv.Itoa(index),
		}
		if suite != "" {
			testCase.Suites = []model.DiscoverySuite{{ID: suite, Name: suite}}
		}
		if onlyEvery > 0 && index%onlyEvery == 0 {
			testCase.IsOnly = true
		}
		cases = append(cases, testCase)
	}
	return cases
}

func staticFile(path string, caseCount int) model.FilePlan {
	file := model.FilePlan{Path: path, Cases: []model.CasePlan{}}
	for index := range caseCount {
		file.Cases = append(file.Cases, model.CasePlan{
			ID:   path + "::test_" + strconv.Itoa(index),
			Name: "Test " + strconv.Itoa(index), MethodName: "test_" + strconv.Itoa(index),
		})
	}
	return file
}

func selectedManifest(files ...model.DiscoveryFile) []model.DiscoveryFile {
	return files
}

func TestResolvePartitionSplitsLargeFilesIntoSamePathEntries(t *testing.T) {
	t.Parallel()

	staticFiles := []model.FilePlan{
		staticFile("big.test.gd", 3),
		staticFile("small.test.gd", 2),
		staticFile("unselected.test.gd", 4),
	}
	selected := selectedManifest(
		model.DiscoveryFile{
			Path: "big.test.gd",
			Cases: append(
				discoveryCases("a", 30, "suite_a", 0),
				discoveryCases("b", 6, "suite_b", 0)...,
			),
		},
		model.DiscoveryFile{Path: "small.test.gd", Cases: discoveryCases("s", 10, "suite_s", 0)},
	)
	selectedIDs := make([]string, 0, 46)
	for _, file := range selected {
		for _, testCase := range file.Cases {
			selectedIDs = append(selectedIDs, testCase.ID)
		}
	}

	resolved := resolvePartition(staticFiles, selected, selectedIDs, "auto", shard.ModeSuite, 2)

	require.Len(t, resolved.files, 4,
		"big splits into two shards; small and unselected files stay whole")
	bigShards := resolved.files[:2]
	for _, entry := range bigShards {
		assert.Equal(t, "big.test.gd", entry.Path)
		require.Len(t, entry.Cases, 3, "each shard carries the file's static method cases")
		require.NotEmpty(t, entry.CaseIDs)
	}
	assert.Equal(t, 30, len(bigShards[0].CaseIDs), "suite_a's cases form one shard")
	assert.Equal(t, 6, len(bigShards[1].CaseIDs), "suite_b's cases form the other shard")
	assert.Equal(t, "small.test.gd", resolved.files[2].Path)
	assert.Nil(t, resolved.files[2].CaseIDs, "files below the threshold stay byte-identical")
	assert.Equal(t, "unselected.test.gd", resolved.files[3].Path,
		"static files with no selected cases keep their whole entry")

	assert.Equal(t, selectedIDs, resolved.selectedIDs, "selection union drives report ordering")
	require.NotNil(t, resolved.hasFocus)
	assert.False(t, *resolved.hasFocus)
	assert.Empty(t, resolved.policyError)
	assert.Equal(t, map[string]struct{}{"big.test.gd": {}}, resolved.sharded)
}

func TestResolvePartitionKeepsSingleSuiteFileWhole(t *testing.T) {
	t.Parallel()

	staticFiles := []model.FilePlan{staticFile("mono.test.gd", 1)}
	selected := selectedManifest(model.DiscoveryFile{
		Path:  "mono.test.gd",
		Cases: discoveryCases("m", shard.MinCasesPerShard+5, "suite_a", 0),
	})

	resolved := resolvePartition(staticFiles, selected, nil, "auto", shard.ModeSuite, 4)

	require.Len(t, resolved.files, 1)
	assert.Nil(t, resolved.files[0].CaseIDs)
	assert.Empty(t, resolved.sharded)
}

func TestResolvePartitionDoesNotOvershardAnAlreadyParallelRun(t *testing.T) {
	t.Parallel()

	var staticFiles []model.FilePlan
	var selected []model.DiscoveryFile
	var selectedIDs []string
	for index := range 8 {
		path := "file-" + strconv.Itoa(index) + ".test.gd"
		staticFiles = append(staticFiles, staticFile(path, 1))
		cases := discoveryCases(path, 40, "", 0)
		selected = append(selected, model.DiscoveryFile{Path: path, Cases: cases})
		for _, testCase := range cases {
			selectedIDs = append(selectedIDs, testCase.ID)
		}
	}

	resolved := resolvePartition(
		staticFiles, selected, selectedIDs, "auto", shard.ModeCase, 4,
	)

	require.Len(t, resolved.files, len(staticFiles))
	assert.Empty(t, resolved.sharded,
		"file fan-out already supplies more work units than worker lanes")
	for _, file := range resolved.files {
		assert.Nil(t, file.CaseIDs, "unsplit entries keep their wire representation")
	}
}

func TestResolvePartitionResolvesFocusAppSide(t *testing.T) {
	t.Parallel()

	build := func() ([]model.FilePlan, []model.DiscoveryFile, []string) {
		staticFiles := []model.FilePlan{staticFile("focus.test.gd", 1)}
		cases := discoveryCases("f", shard.MinCasesPerShard+4, "suite_a", 5)
		selected := selectedManifest(model.DiscoveryFile{Path: "focus.test.gd", Cases: cases})
		ids := make([]string, 0, len(cases))
		for _, testCase := range cases {
			ids = append(ids, testCase.ID)
		}
		return staticFiles, selected, ids
	}

	staticFiles, selected, ids := build()
	focused := make([]string, 0, 4)
	for _, testCase := range selected[0].Cases {
		if testCase.IsOnly {
			focused = append(focused, testCase.ID)
		}
	}
	require.NotEmpty(t, focused)

	resolved := resolvePartition(staticFiles, selected, ids, "auto", shard.ModeCase, 2)
	require.NotNil(t, resolved.hasFocus)
	assert.True(t, *resolved.hasFocus)
	assert.Equal(t, focused, resolved.selectedIDs,
		"non-only cases are dropped per file, mirroring the worker focus filter")
	require.Len(t, resolved.files, 1)
	assert.Empty(t, resolved.files[0].CaseIDs,
		"the focused remainder stays below the threshold and keeps no explicit subset")

	staticFiles, selected, ids = build()
	resolved = resolvePartition(staticFiles, selected, ids, "ignore", shard.ModeCase, 2)
	require.NotNil(t, resolved.hasFocus)
	assert.True(t, *resolved.hasFocus, "focus is stamped even when ignored")
	assert.Equal(t, ids, resolved.selectedIDs, "ignore mode runs every selected case")
}

func TestResolvePartitionPreFlightsOnlyModePolicy(t *testing.T) {
	t.Parallel()

	staticFiles := []model.FilePlan{staticFile("plain.test.gd", 1)}
	selected := selectedManifest(model.DiscoveryFile{
		Path:  "plain.test.gd",
		Cases: discoveryCases("p", 3, "suite_a", 0),
	})

	requirement := resolvePartition(staticFiles, selected, nil, "require", shard.ModeSuite, 2)
	assert.Equal(t, "only mode requires at least one focused selected case", requirement.policyError)

	forbidden := resolvePartition(
		staticFiles,
		selectedManifest(model.DiscoveryFile{
			Path:  "plain.test.gd",
			Cases: discoveryCases("p", 3, "suite_a", 1),
		}),
		nil, "forbid", shard.ModeSuite, 2,
	)
	assert.Equal(t, "focused tests are forbidden by only mode", forbidden.policyError)

	automatic := resolvePartition(staticFiles, selected, nil, "auto", shard.ModeSuite, 2)
	assert.Empty(t, automatic.policyError)
}

func TestOnlyPolicyErrorMirrorsTheWorkerContract(t *testing.T) {
	t.Parallel()

	assert.Empty(t, onlyPolicyError("", false))
	assert.Empty(t, onlyPolicyError("auto", true))
	assert.Empty(t, onlyPolicyError("ignore", true))
	assert.Empty(t, onlyPolicyError("require", true))
	assert.Empty(t, onlyPolicyError("forbid", false))
	assert.Equal(
		t, "only mode requires at least one focused selected case",
		onlyPolicyError("require", false),
	)
	assert.Equal(
		t, "focused tests are forbidden by only mode",
		onlyPolicyError("forbid", true),
	)
	assert.Equal(t, "unsupported only mode: weird", onlyPolicyError("weird", false))
}

func TestShardedFileEventSinkDeduplicatesWorkerErrorsPerShardedFile(t *testing.T) {
	t.Parallel()

	var forwarded []model.Event
	inner := executor.EventSinkFunc(func(event model.Event) error {
		forwarded = append(forwarded, event)
		return nil
	})
	sink := newShardedFileEventSink(inner, map[string]struct{}{"big.test.gd": {}})

	emissions := []model.Event{
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message:  "unable to load test script big.test.gd",
			Location: &model.Location{File: "big.test.gd"}},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message:  "unable to load test script big.test.gd",
			Location: &model.Location{File: "big.test.gd"}},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message:  "unable to load test script big.test.gd",
			Location: &model.Location{File: "other.test.gd"}},
		{Type: model.EventWorkerError, Name: "worker.discovery",
			Message:  "another diagnostic",
			Location: &model.Location{File: "big.test.gd"}},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message:  "unable to load test script big.test.gd",
			Location: &model.Location{File: "big.test.gd"}},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message: "unable to load test script res://big.test.gd"},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message: "unable to load test script res://big.test.gd"},
		{Type: model.EventWorkerError, Name: "worker.script_load",
			Message: "malformed load error without a file"},
		{Type: model.EventCaseFinished, TestID: "big.test.gd::test_one", Status: "pass",
			Location: &model.Location{File: "big.test.gd"}},
	}
	for _, event := range emissions {
		require.NoError(t, sink.Emit(event))
	}

	require.Len(t, forwarded, 6)
	assert.Equal(t, "big.test.gd", forwarded[0].Location.File)
	assert.Equal(t, "other.test.gd", forwarded[1].Location.File)
	assert.Equal(t, "another diagnostic", forwarded[2].Message)
	assert.Equal(t, "unable to load test script res://big.test.gd", forwarded[3].Message)
	assert.Equal(t, "malformed load error without a file", forwarded[4].Message,
		"unrecognized locationless errors pass through fail-safe")
	assert.Equal(t, model.EventCaseFinished, forwarded[5].Type,
		"non-worker_error events pass through untouched")
}
