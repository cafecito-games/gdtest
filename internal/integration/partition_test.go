package integration_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// partitioningFixture mirrors fixtures/godot/api/partitioning.test.gd inside a
// disposable project so CLI-level partitioning runs against a real Godot
// project without touching the repository tree.
const partitioningFixture = `extends GDTest

var _before_all_runs: int = 0
var _suite_entries: Dictionary[String, int] = {}

func get_suite_name() -> String:
	return "Partitioning"

func suite() -> void:
	define(
			"Alpha",
			func() -> void:
				for index: int in range(30):
					test(
							"alpha case %02d" % index,
							func() -> void:
								expect_hook_state("Alpha")
					)
	)
	define(
			"Beta",
			func() -> void:
				for index: int in range(6):
					test(
							"beta case %02d" % index,
							func() -> void:
								expect_hook_state("Beta")
					)
	)

func expect_hook_state(suite_name: String) -> void:
	expect_equal(1, _before_all_runs)
	expect_equal(1, int(_suite_entries.get(suite_name, 0)))

func before_all() -> void:
	_before_all_runs += 1

func before_suite(suite_info: GDTestSuiteInfo) -> void:
	_suite_entries[suite_info.name] = int(_suite_entries.get(suite_info.name, 0)) + 1
`

func setupPartitionedProject(t *testing.T, configurationJSON string) string {
	t.Helper()
	return setupTempGodotProject(
		t,
		"",
		configurationJSON,
		map[string]string{"tests/big.test.gd": partitioningFixture},
	)
}

func countPartitionedRuns(t *testing.T, events []decodedJSONEvent) (passes int, suiteFinished int) {
	t.Helper()
	for _, event := range events {
		switch event.Type {
		case "case_finished":
			require.Equal(t, "pass", event.Status, "case %s status", event.TestID)
			passes++
		case "suite_finished":
			suiteFinished++
		}
	}
	return passes, suiteFinished
}

func TestCLIPartitionSuiteMatchesUnpartitionedRun(t *testing.T) {
	root := setupPartitionedProject(t, "")

	code, baselineEvents, diagnostics := runCLIJSON(t, root)
	require.Equal(t, app.ExitOK, code, "diagnostics:\n%s", diagnostics)
	baselinePasses, baselineSuites := countPartitionedRuns(t, baselineEvents)
	require.Equal(t, 36, baselinePasses)
	require.Equal(t, 1, baselineSuites, "an unpartitioned file completes one root suite")

	code, shardedEvents, diagnostics := runCLIJSON(t, root, "--partition", "suite", "--jobs", "2")
	require.Equal(t, app.ExitOK, code, "diagnostics:\n%s", diagnostics)
	shardedPasses, shardedSuites := countPartitionedRuns(t, shardedEvents)
	assert.Equal(t, 36, shardedPasses, "per-case statuses and totals match the unpartitioned run")
	assert.Equal(t, 2, shardedSuites, "two same-path shards run before_all once each")

	baselineIDs := caseFinishOrder(t, baselineEvents)
	assert.Equal(t, baselineIDs, caseFinishOrder(t, shardedEvents),
		"report ordering follows the selection, not the shard interleaving")
}

func caseFinishOrder(t *testing.T, events []decodedJSONEvent) []string {
	t.Helper()
	ids := make([]string, 0, 36)
	for _, event := range events {
		if event.Type == "case_finished" {
			ids = append(ids, event.TestID)
		}
	}
	return ids
}

func TestCLIPartitionConfigAndFlagPrecedence(t *testing.T) {
	root := setupPartitionedProject(t, `{"partition": "suite"}`)

	code, events, diagnostics := runCLIJSON(t, root)
	require.Equal(t, app.ExitOK, code, "diagnostics:\n%s", diagnostics)
	_, suiteFinished := countPartitionedRuns(t, events)
	assert.Equal(t, 2, suiteFinished, "gdtest.json partitions without a flag")

	code, events, diagnostics = runCLIJSON(t, root, "--partition", "file")
	require.Equal(t, app.ExitOK, code, "diagnostics:\n%s", diagnostics)
	_, suiteFinished = countPartitionedRuns(t, events)
	assert.Equal(t, 1, suiteFinished, "the CLI flag overrides gdtest.json")

	invalid := setupPartitionedProject(t, `{"partition": "shard"}`)
	exitCode, _, invalidDiagnostics := runCLITerminal(t, invalid)
	assert.Equal(t, app.ExitError, exitCode)
	assert.Contains(t, invalidDiagnostics, `partition "shard" must be file, suite, or case`)
}

func TestCLIPartitionSuiteEmptySelectionSucceedsAndJUnitMergesShards(t *testing.T) {
	root := setupPartitionedProject(t, `{"partition": "suite"}`)

	code, output, diagnostics := runCLITerminal(
		t, root, "--name", "matches nothing", "--allow-empty",
	)
	assert.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output, diagnostics)
	assert.Contains(t, output, "No tests selected")

	reportPath := filepath.Join(root, "junit-report.xml")
	code, output, diagnostics = runCLITerminal(
		t, root, "--jobs", "2", "--report-format", "junit", "--report-file", reportPath,
	)
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output, diagnostics)
	assert.Contains(t, output, "36 passed")

	reportData, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var document struct {
		XMLName xml.Name `xml:"testsuites"`
		Suites  []struct {
			Name      string `xml:"name,attr"`
			TestCases []struct {
				Name string `xml:"name,attr"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	require.NoError(t, xml.Unmarshal(reportData, &document))
	require.Len(t, document.Suites, 1, "a file's shards merge under one JUnit suite")
	assert.Equal(t, "tests/big.test.gd", document.Suites[0].Name)
	assert.Len(t, document.Suites[0].TestCases, 36)
	assert.True(t, strings.Contains(string(reportData), "tests/big.test.gd"))
}

func TestRealGodotShardsOneFileAcrossPersistentDaemonLanes(t *testing.T) {
	const source = `extends GDTest

func test_alpha() -> void:
	await sleep(0.05)
	expect_true(true)

func test_beta() -> void:
	await sleep(0.05)
	expect_true(true)
`
	root := setupTempGodotProject(
		t, "", "", map[string]string{"tests/sharded.test.gd": source},
	)
	file, err := discovery.Parse("tests/sharded.test.gd", []byte(source))
	require.NoError(t, err)
	require.Len(t, file.Cases, 2)
	shards := make([]model.FilePlan, 0, len(file.Cases))
	for _, testCase := range file.Cases {
		entry := file
		entry.CaseIDs = []string{testCase.ID}
		shards = append(shards, entry)
	}

	sessions := service.NewSessionServiceWithOptions(service.ServiceOptions{MaxWorkers: 2})
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, sessions.Close(closeCtx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	generation := uint64(1)
	for attempt := range 2 {
		warmPayload, marshalErr := json.Marshal(service.WarmRequest{
			SessionID: "real-sharded-lanes", Project: root, Godot: "godot", Generation: generation,
		})
		require.NoError(t, marshalErr)
		warmResponse, warmErr := sessions.Handle(ctx, daemon.Request{
			ID: fmt.Sprintf("real-sharded-warm-%d", attempt), Method: "session.warm", Payload: warmPayload,
		}, nil)
		require.NoError(t, warmErr)
		warmValues, ok := warmResponse.(map[string]any)
		require.True(t, ok)
		generation, ok = warmValues["generation"].(uint64)
		require.True(t, ok)
	}

	run := func(runID string) []int {
		payload, marshalErr := json.Marshal(service.RunRequest{
			SessionID: "real-sharded-lanes", Project: root, Godot: "godot",
			Jobs: 2, WorkerMaxRuns: 100,
			Plan: model.TestPlan{
				ProtocolVersion: model.ProtocolVersion, RunID: runID, Generation: generation,
				Files: shards,
			},
		})
		require.NoError(t, marshalErr)
		statuses := make(map[string]string)
		response, handleErr := sessions.Handle(ctx, daemon.Request{
			ID: runID, Method: "session.run", Payload: payload,
		}, func(value any) error {
			event, ok := value.(model.Event)
			if !ok {
				return fmt.Errorf("unexpected event %T", value)
			}
			if event.Type == model.EventCaseFinished {
				statuses[event.TestID] = event.Status
			}
			return nil
		})
		require.NoError(t, handleErr)
		assert.Equal(t, map[string]string{
			file.Cases[0].ID: "pass",
			file.Cases[1].ID: "pass",
		}, statuses)
		values, ok := response.(map[string]any)
		require.True(t, ok)
		pids, ok := values["worker_pids"].([]int)
		require.True(t, ok)
		require.Len(t, pids, 2)
		assert.NotEqual(t, pids[0], pids[1], "each same-path shard uses a distinct real process")
		return pids
	}

	firstPIDs := run("real-sharded-first")
	assert.Equal(t, 2, sessions.WorkerCount())
	secondPIDs := run("real-sharded-second")
	assert.ElementsMatch(t, firstPIDs, secondPIDs, "the second run reuses both real worker lanes")
	assert.Equal(t, 2, sessions.WorkerCount())
}
