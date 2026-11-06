package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePlanAcceptsShardFields(t *testing.T) {
	t.Parallel()

	stamped := false
	assert.NoError(t, validatePlan(model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		HasFocus:        &stamped,
		Files: []model.FilePlan{
			{Path: "tests/a.test.gd", CaseIDs: []string{"tests/a.test.gd::test_one"}},
			{Path: "tests/a.test.gd", CaseIDs: []string{}},
		},
	}))
}

func TestValidatePlanRejectsInvalidPerFileCaseSubsets(t *testing.T) {
	t.Parallel()

	emptyEntry := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		Files:           []model.FilePlan{{Path: "tests/a.test.gd", CaseIDs: []string{""}}},
	}
	err := validatePlan(emptyEntry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `file "tests/a.test.gd" case_ids[0]: case ID must not be empty`)

	duplicateEntry := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		Files:           []model.FilePlan{{Path: "tests/a.test.gd", CaseIDs: []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_one"}}},
	}
	err = validatePlan(duplicateEntry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicates case_ids[0]`)
}

func TestRunRequestDecodesShardFieldsStrictly(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"session_id": "shard-session",
		"project": "/project",
		"godot": "godot",
		"plan": {
			"protocol_version": 4,
			"run_id": "shard-run",
			"generation": 1,
			"selection_applied": true,
			"selected_ids": ["tests/a.test.gd::test_one"],
			"has_focus": false,
			"files": [
				{"path": "tests/a.test.gd", "case_ids": ["tests/a.test.gd::test_one", "tests/a.test.gd::test_rows[1]"], "cases": []},
				{"path": "tests/a.test.gd", "case_ids": [], "cases": []}
			]
		}
	}`)
	var request RunRequest
	require.NoError(t, decodeStrict(payload, &request))
	require.Len(t, request.Plan.Files, 2)
	assert.Equal(t, []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_rows[1]"}, request.Plan.Files[0].CaseIDs)
	require.NotNil(t, request.Plan.Files[1].CaseIDs)
	assert.Empty(t, request.Plan.Files[1].CaseIDs)
	require.NotNil(t, request.Plan.HasFocus)
	assert.False(t, *request.Plan.HasFocus)

	var strictRejects RunRequest
	err := decodeStrict([]byte(`{"session_id":"s","project":"/p","godot":"g","plan":{"protocol_version":4,"run_id":"r","generation":1,"files":[]},"unknown_shard_field":true}`), &strictRejects)
	require.Error(t, err)
}

func TestSessionServiceMultiFileRunFoldsDuplicatePathEntriesIntoOneRunPair(t *testing.T) {
	t.Parallel()

	var processes []*fakeWorker
	var dispatched []model.FilePlan
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 890 + len(processes), generation: options.Generation}
		process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
			dispatched = append(dispatched, plan.Files...)
			for _, event := range []model.Event{
				{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted, RunID: plan.RunID},
				{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished, RunID: plan.RunID, Status: "pass"},
			} {
				if err := sink.Emit(event); err != nil {
					return err
				}
			}
			return nil
		}
		processes = append(processes, process)
		return process, nil
	}, 1)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "duplicate-path-run", Project: "/project", Godot: "godot",
		Jobs: 1,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "duplicate-path-run",
			Generation:      1,
			Files: []model.FilePlan{
				{Path: "tests/a.test.gd", CaseIDs: []string{"tests/a.test.gd::test_one"}},
				{Path: "tests/a.test.gd", CaseIDs: []string{"tests/a.test.gd::test_two"}},
			},
		},
	})
	require.NoError(t, err)

	var runStarted, runFinished int
	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "duplicate-path-request", Method: "session.run", Payload: payload,
	}, func(value any) error {
		event, ok := value.(model.Event)
		if !ok {
			return nil
		}
		switch event.Type {
		case model.EventRunStarted:
			runStarted++
		case model.EventRunFinished:
			runFinished++
		}
		return nil
	})
	require.NoError(t, err)

	require.Len(t, dispatched, 2, "each duplicate-path file entry must dispatch as its own run")
	require.Len(t, dispatched[0].CaseIDs, 1)
	require.Len(t, dispatched[1].CaseIDs, 1)
	assert.Equal(t, "tests/a.test.gd", dispatched[0].Path)
	assert.Equal(t, "tests/a.test.gd", dispatched[1].Path)
	assert.ElementsMatch(
		t,
		[]string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_two"},
		[]string{dispatched[0].CaseIDs[0], dispatched[1].CaseIDs[0]},
	)
	assert.Equal(t, 1, runStarted, "worker run_started events must fold into one daemon run_started")
	assert.Equal(t, 1, runFinished, "worker run_finished events must fold into one daemon run_finished")
}

func TestSessionServiceRunsShardsOfOneFileOnSeparateLanes(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var factoryMu sync.Mutex
	var processes []*fakeWorker
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 950 + len(processes), generation: options.Generation}
		process.runHook = func(plan model.TestPlan, sink executor.EventSink) error {
			started <- struct{}{}
			<-release
			return sink.Emit(model.Event{
				ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
				RunID: plan.RunID, Status: "pass",
			})
		}
		factoryMu.Lock()
		processes = append(processes, process)
		factoryMu.Unlock()
		return process, nil
	}, 4)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	var finishMetadata map[string]any
	payload, err := json.Marshal(RunRequest{
		SessionID: "sharded-single-file", Project: "/project", Godot: "godot", Jobs: 2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "sharded-single-file", Generation: 1,
			Files: []model.FilePlan{
				{Path: "tests/big.test.gd", CaseIDs: []string{"tests/big.test.gd::test_a"}},
				{Path: "tests/big.test.gd", CaseIDs: []string{"tests/big.test.gd::test_b"}},
			},
		},
	})
	require.NoError(t, err)

	handleDone := make(chan error, 1)
	go func() {
		_, handleErr := service.Handle(context.Background(), daemon.Request{
			ID: "sharded-single-file-request", Method: "session.run", Payload: payload,
		}, func(value any) error {
			event, ok := value.(model.Event)
			if ok && event.Type == model.EventRunFinished {
				finishMetadata = event.Metadata
			}
			return nil
		})
		handleDone <- handleErr
	}()
	waitServiceSignal(t, started)
	waitServiceSignal(t, started)
	close(release)
	require.NoError(t, <-handleDone)

	factoryMu.Lock()
	defer factoryMu.Unlock()
	assert.Len(t, processes, 2, "two shards of one file occupy one lane each")
	require.NotNil(t, finishMetadata)
	assert.Equal(t, 1, finishMetadata["files"], "files counts distinct paths")
	assert.Equal(t, 2, finishMetadata["shards"], "shards counts dispatched plan entries")
}

func TestSessionServiceFailingShardCancelsSiblingShards(t *testing.T) {
	t.Parallel()

	slowStarted := make(chan struct{})
	boomError := errors.New("shard worker died")
	service := newSessionServiceWithCapacity(func(_ context.Context, options worker.ProcessOptions) (managedWorker, error) {
		process := &fakeWorker{pid: 970, generation: options.Generation}
		process.runContextHook = func(ctx context.Context, plan model.TestPlan, _ executor.EventSink) error {
			if plan.Files[0].CaseIDs[0] == "tests/big.test.gd::test_boom" {
				// Wait until the sibling shard is executing so cancellation
				// provably reaches live sibling work rather than a queued one.
				<-slowStarted
				return boomError
			}
			close(slowStarted)
			<-ctx.Done()
			return ctx.Err()
		}
		return process, nil
	}, 4)
	service.sourceFactory = nil
	t.Cleanup(func() { require.NoError(t, service.Close(context.Background())) })

	payload, err := json.Marshal(RunRequest{
		SessionID: "shard-cancellation", Project: "/project", Godot: "godot", Jobs: 2,
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: "shard-cancellation", Generation: 1,
			Files: []model.FilePlan{
				{Path: "tests/big.test.gd", CaseIDs: []string{"tests/big.test.gd::test_slow"}},
				{Path: "tests/big.test.gd", CaseIDs: []string{"tests/big.test.gd::test_boom"}},
			},
		},
	})
	require.NoError(t, err)

	_, err = service.Handle(context.Background(), daemon.Request{
		ID: "shard-cancellation-request", Method: "session.run", Payload: payload,
	}, func(any) error { return nil })
	require.Error(t, err, "a failing shard must fail the whole run")
	assert.ErrorIs(t, err, boomError)
	assert.ErrorIs(t, err, context.Canceled, "the sibling shard must observe cancellation")
}
