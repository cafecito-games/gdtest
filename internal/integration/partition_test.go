package integration_test

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdtest/internal/app"
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
