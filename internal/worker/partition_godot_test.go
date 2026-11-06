package worker_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/shard"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// partitionRunEvents folds one run. Root-suite completions equal dispatched
// file entries, and therefore before_all invocations, because nested suites
// emit no suite events of their own.
type partitionRunEvents struct {
	statuses      map[string]string
	suiteFinished int
	workerErrors  []string
	finished      string
}

func runPartitionPlan(
	t *testing.T,
	process *worker.Process,
	ctx context.Context,
	runID string,
	entries []model.FilePlan,
) *partitionRunEvents {
	t.Helper()
	events := &partitionRunEvents{statuses: map[string]string{}}
	sink := executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseFinished:
			events.statuses[event.TestID] = event.Status
		case model.EventSuiteFinished:
			events.suiteFinished++
		case model.EventWorkerError:
			events.workerErrors = append(events.workerErrors, event.Message)
		case model.EventRunFinished:
			events.finished = event.Status
		}
		return nil
	})
	err := process.Run(ctx, model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: runID, Generation: 1,
		ContentFingerprint: "partitioning", Files: entries,
	}, sink)
	require.NoError(t, err)
	require.NoError(t, process.Reset(ctx))
	assert.Equal(t, "pass", events.finished)
	assert.Empty(t, events.workerErrors)
	return events
}

func shardCasesFrom(cases []model.DiscoveryCase) []shard.Case {
	input := make([]shard.Case, 0, len(cases))
	for _, testCase := range cases {
		suites := make([]string, len(testCase.Suites))
		for index, suite := range testCase.Suites {
			suites[index] = suite.ID
		}
		input = append(input, shard.Case{ID: testCase.ID, Suites: suites})
	}
	return input
}

func entriesFromShards(path string, static model.FilePlan, shards [][]string) []model.FilePlan {
	entries := make([]model.FilePlan, 0, len(shards))
	for _, shardIDs := range shards {
		entries = append(entries, model.FilePlan{Path: path, Cases: static.Cases, CaseIDs: shardIDs})
	}
	return entries
}

func TestSuitePartitionedRunMatchesUnshardedSerialRun(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot suite partitioning test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/partitioning.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "suite-partitioning", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	allCases := discoverManifestCases(t, process, ctx, file)
	require.Len(t, allCases, 36)
	var alphaCases, betaCases int
	for _, testCase := range allCases {
		require.Len(t, testCase.Suites, 1, "every partitioning case lives in one suite")
		if testCase.Suites[0].Name == "Alpha" {
			alphaCases++
		} else {
			betaCases++
		}
	}
	require.Equal(t, 30, alphaCases)
	require.Equal(t, 6, betaCases)

	serial := runPartitionPlan(t, process, ctx, "partition-serial", []model.FilePlan{
		{Path: file.Path, Cases: file.Cases},
	})
	require.Len(t, serial.statuses, 36)
	for id, status := range serial.statuses {
		require.Equal(t, "pass", status, "serial reference case %s must pass", id)
	}
	assert.Equal(t, 1, serial.suiteFinished, "the unsharded file completes one root suite")

	shards := shard.Partition(shardCasesFrom(allCases), shard.ModeSuite, 2)
	require.Len(t, shards, 2, "suite mode isolates Alpha and Beta on two lanes")
	require.Len(t, shards[0], 30)
	require.Len(t, shards[1], 6)

	partitioned := runPartitionPlan(
		t, process, ctx, "partition-suite", entriesFromShards(file.Path, file, shards),
	)
	assert.Equal(t, serial.statuses, partitioned.statuses,
		"per-case statuses and totals must be identical to the unsharded run")
	assert.Equal(t, 2, partitioned.suiteFinished,
		"before_all and the root suite run once per shard")
}

func TestCasePartitionedRunSplitsASingleSuiteFile(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot case partitioning test skipped: godot is not on PATH")
	}
	root := repositoryRoot(t)
	file := parseFixture(t, root, "fixtures/godot/api/partitioning.test.gd")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "case-partitioning", Generation: 1,
	})
	require.NoError(t, err)
	defer shutdown(t, process)

	allCases := discoverManifestCases(t, process, ctx, file)
	shards := shard.Partition(shardCasesFrom(allCases), shard.ModeCase, 2)
	require.Len(t, shards, 2, "case mode cuts anywhere")

	events := runPartitionPlan(
		t, process, ctx, "partition-case", entriesFromShards(file.Path, file, shards),
	)
	require.Len(t, events.statuses, 36)
	for id, status := range events.statuses {
		require.Equal(t, "pass", status,
			"case %s must observe its suite hooks once per shard containing it", id)
	}
	assert.Equal(t, 2, events.suiteFinished)
}
