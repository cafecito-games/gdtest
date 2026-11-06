package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelingBuffer struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (b *cancelingBuffer) Write(data []byte) (int, error) {
	written, err := b.Buffer.Write(data)
	if strings.Contains(string(data), "watching generation 1") {
		b.cancel()
	}
	return written, err
}

func TestWatchWaitsAfterEmptyRunAndCancelsCleanly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	stderr := cancelingBuffer{cancel: cancel}
	exitCode := Run(ctx, []string{
		"watch", "--project", root, "--allow-empty", "--no-daemon",
	}, IO{Out: &stdout, Err: &stderr})
	require.Equal(t, ExitCanceled, exitCode, "stdout=%q stderr=%q", stdout.String(), stderr.String())
	assert.Contains(t, stdout.String(), "No tests selected.")
	assert.Contains(t, stderr.String(), "watching generation 1")
}

func TestRootHelpDescribesExactCommandTree(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--help"}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	for _, command := range []string{
		"bench", "daemon", "doctor", "init", "list", "migrate", "run", "version", "warm", "watch",
	} {
		assert.Contains(t, stdout.String(), command)
	}
	assert.NotContains(t, stdout.String(), "completion")
	assert.Contains(t, stdout.String(), "--version")
	assert.NotContains(t, stdout.String(), "-v, --version")
}

func TestBenchHelpDocumentsBenchmarkPolicy(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"bench", "--help"}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	for _, flag := range []string{
		"--warmup", "--samples", "--iterations", "--compare", "--write-baseline",
		"--json", "--no-daemon", "--isolation", "--jobs", "--allow-empty",
	} {
		assert.Contains(t, stdout.String(), flag)
	}
}

func TestBenchRejectsInvalidNumericOptionsBeforeProjectAccess(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"bench", "--project", t.TempDir(), "--warmup", "-1"},
		{"bench", "--project", t.TempDir(), "--samples", "0"},
		{"bench", "--project", t.TempDir(), "--iterations", "0"},
	}
	for _, arguments := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := Run(context.Background(), arguments, IO{Out: &stdout, Err: &stderr})
		require.Equal(t, ExitError, exitCode)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "warmup must be non-negative")
	}
}

func TestBenchAllowEmptySucceedsWithoutStartingGodot(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"bench", "--project", root, "--allow-empty", "--no-daemon",
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode, stderr.String())
	assert.Equal(t, "No benchmarks selected.\n", stdout.String())
}

func TestBenchRejectsMalformedBaselineBeforeProjectAccess(t *testing.T) {
	t.Parallel()
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	require.NoError(t, os.WriteFile(baseline, []byte(`{"schema_version":99}`), 0o600))
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"bench", "--project", filepath.Join(t.TempDir(), "missing"), "--compare", baseline,
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "benchmark baseline")
	assert.Contains(t, stderr.String(), "schema")
}

func TestBenchFailurePreservesExistingBaseline(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.bench.gd"), []byte(
		"extends GDTestBenchmark\nfunc benchmark_bad(value: int) -> void:\n\tpass\n",
	), 0o600))
	baseline := filepath.Join(root, "baseline.json")
	require.NoError(t, os.WriteFile(baseline, []byte("existing baseline"), 0o600))
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"bench", "--project", root, "--no-daemon", "--write-baseline", baseline,
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	assert.Contains(t, stderr.String(), "must take no arguments")
	data, err := os.ReadFile(baseline)
	require.NoError(t, err)
	assert.Equal(t, "existing baseline", string(data))
}

func TestBenchmarkEnvironmentDisclosesDirtyWorktreeWithoutItsPath(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for dirty-worktree benchmark metadata")
	}
	root := t.TempDir()
	runGit := func(arguments ...string) {
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		output, err := command.CombinedOutput()
		require.NoErrorf(t, err, "git %v:\n%s", arguments, output)
	}
	runGit("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("clean\n"), 0o600))
	runGit("add", "tracked.txt")
	runGit("-c", "user.name=gdtest", "-c", "user.email=gdtest@example.invalid", "commit", "-qm", "clean")
	clean := benchmarkEnvironment(root)
	assert.False(t, clean.DirtyWorktree)

	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("dirty\n"), 0o600))
	dirty := benchmarkEnvironment(root)
	assert.True(t, dirty.DirtyWorktree)
	encoded, err := json.Marshal(dirty)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), root)
}

func TestRunHelpDocumentsIsolationPolicies(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"run", "--help"}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "--isolation")
	assert.Contains(t, stdout.String(), "reuse, recycle, fresh, or exclusive")
	assert.Contains(t, stdout.String(), "--jobs")
	assert.Contains(t, stdout.String(), "0 uses automatic concurrency")
	assert.Contains(t, stdout.String(), "--fail-on-skip")
	assert.Contains(t, stdout.String(), "--fail-on-todo")
	assert.Contains(t, stdout.String(), "--fail-on-void")
	assert.Contains(t, stdout.String(), "--report-format")
	assert.Contains(t, stdout.String(), "terminal, jsonl, or junit")
	assert.Contains(t, stdout.String(), "--report-file")
	assert.Contains(t, stdout.String(), "--json")
}

func TestListHelpDocumentsForegroundDiscovery(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"list", "--help"}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "--no-daemon")
}

func TestRunRejectsNegativeJobsBeforeProjectAccess(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"run", "--project", t.TempDir(), "--jobs", "-1",
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "jobs must be non-negative")
}

func TestRunRejectsUnknownIsolationBeforeProjectAccess(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"run", "--project", t.TempDir(), "--isolation", "shared",
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), `isolation "shared" must be reuse, recycle, fresh, or exclusive`)
}

func TestSummaryExitCodeDistinguishesStrictPolicyFromInfrastructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary results.Summary
		want    int
	}{
		{name: "clean", summary: results.Summary{Passed: 1}, want: ExitOK},
		{
			name: "strict outcome",
			summary: results.Summary{
				Skipped:        1,
				PolicyFailures: []results.PolicyFailure{{Status: "skip", Count: 1}},
			},
			want: ExitFailures,
		},
		{
			name: "execution framework error",
			summary: results.Summary{
				Passed:  1,
				Errored: 1,
				Errors:  []model.Event{{Type: model.EventWorkerError, Name: "worker.framework"}},
			},
			want: ExitFailures,
		},
		{
			name: "infrastructure takes precedence",
			summary: results.Summary{
				Errored:        2,
				Errors:         []model.Event{{Type: model.EventWorkerError, Name: "worker.framework"}},
				PolicyFailures: []results.PolicyFailure{{Status: "void", Count: 1}},
				Infrastructure: []model.Event{{Type: model.EventWorkerError, Name: "worker.discovery"}},
			},
			want: ExitError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, summaryExitCode(test.summary))
		})
	}
}

func TestDaemonHelpDescribesNestedCommands(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"daemon", "--help"}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	for _, command := range []string{"serve", "status", "sessions", "stop", "logs"} {
		assert.Contains(t, stdout.String(), command)
	}
	assert.NotContains(t, stdout.String(), "completion")
}

func TestUnknownCommandsFlagsAndInvalidArgumentsReturnUsageError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "root command", args: []string{"not-a-command"}, want: "unknown command"},
		{name: "root flag", args: []string{"--not-a-flag"}, want: "unknown flag"},
		{name: "command flag", args: []string{"run", "--not-a-flag"}, want: "unknown flag"},
		{name: "nested command", args: []string{"daemon", "not-a-command"}, want: "unknown command"},
		{name: "nested positional", args: []string{"daemon", "status", "extra"}, want: "unknown command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(context.Background(), test.args, IO{Out: &stdout, Err: &stderr})

			require.Equal(t, ExitError, exitCode)
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), "gdtest:")
			assert.Contains(t, stderr.String(), test.want)
			assert.NotContains(t, stderr.String(), "Usage:", "usage is available on demand, not on every input error")
		})
	}
}

func TestCommandsWithoutRequiredNestedCommandAreErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"daemon"}, want: "daemon requires"},
		{args: []string{"migrate"}, want: "migrate requires"},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run(context.Background(), test.args, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitError, exitCode)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), test.want)
	}
}

func TestNoCommandPreservesUsageError(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), nil, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "usage: gdtest <init|run|bench|list|watch|warm|doctor|migrate|daemon> [options]\n", stderr.String())
}

func TestMigrateVestModesAndMutuallyExclusiveFlags(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	testPath := filepath.Join(root, "tests", "example.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(testPath), 0o700))
	input := []byte("extends VestTest\n")
	require.NoError(t, os.WriteFile(testPath, input, 0o600))

	var checkOut bytes.Buffer
	var checkErr bytes.Buffer
	checkExit := Run(context.Background(), []string{
		"migrate", "vest", "--project", root, "--check", "tests/example.gd",
	}, IO{Out: &checkOut, Err: &checkErr})
	require.Equal(t, ExitOK, checkExit, checkErr.String())
	assert.Contains(t, checkOut.String(), "1 file(s) would change")
	contents, err := os.ReadFile(testPath)
	require.NoError(t, err)
	assert.Equal(t, input, contents, "check mode must not write")

	var invalidOut bytes.Buffer
	var invalidErr bytes.Buffer
	invalidExit := Run(context.Background(), []string{
		"migrate", "vest", "--project", root, "--check", "--write", "tests/example.gd",
	}, IO{Out: &invalidOut, Err: &invalidErr})
	require.Equal(t, ExitError, invalidExit)
	assert.Empty(t, invalidOut.String())
	assert.Contains(t, invalidErr.String(), "[check write]")
	contents, err = os.ReadFile(testPath)
	require.NoError(t, err)
	assert.Equal(t, input, contents, "invalid flag combinations must not write")

	var writeOut bytes.Buffer
	var writeErr bytes.Buffer
	writeExit := Run(context.Background(), []string{
		"migrate", "vest", "--project", root, "--write", "tests/example.gd",
	}, IO{Out: &writeOut, Err: &writeErr})
	require.Equal(t, ExitOK, writeExit, writeErr.String())
	assert.Contains(t, writeOut.String(), "1 file(s) changed")
	contents, err = os.ReadFile(testPath)
	require.NoError(t, err)
	assert.Contains(t, string(contents), "extends GDTest")
}

func TestHelpDoesNotExecuteOrWriteCommand(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"init", "--project", root, "--help"}, IO{
		Out: &stdout,
		Err: &stderr,
	})

	require.Equal(t, ExitOK, exitCode)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "--project")
	_, err := os.Stat(filepath.Join(root, "addons", "gdtest"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestVersionCommandAndFlagUseStableOutput(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run(context.Background(), args, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitOK, exitCode, "args=%v stderr=%s", args, stderr.String())
		assert.Equal(t, versionString()+"\n", stdout.String())
		assert.Empty(t, stderr.String())
	}
}

func TestRunExitCodesPropagateThroughCobraBoundary(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"run", "--project", root, "--allow-empty", "--no-daemon",
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode, stderr.String())
	assert.Contains(t, stdout.String(), "No tests selected.")
}

func TestRunAffectedEmptySelectionUsesStableSuccessfulMessage(t *testing.T) {
	t.Parallel()
	root := newTestProject(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"run", "--project", root, "--affected", "README.md", "--no-daemon",
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitOK, exitCode, stderr.String())
	assert.Equal(t, "no tests were affected\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestWriteTimingsIsDeterministicForTerminalAndMachineOutput(t *testing.T) {
	t.Parallel()
	timings := map[string]time.Duration{"worker": 2 * time.Millisecond, "client": time.Millisecond}
	var terminal bytes.Buffer
	require.NoError(t, writeTimings(IO{Err: &terminal}, timings, false))
	assert.Equal(t, "gdtest: timing client=1ms\ngdtest: timing worker=2ms\n", terminal.String())

	var machine bytes.Buffer
	require.NoError(t, writeTimings(IO{Out: &machine}, timings, true))
	assert.JSONEq(t, `{"type":"timings","timings_ns":{"client":1000000,"worker":2000000}}`, machine.String())
}

func newTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	return root
}

func TestStaleGenerationErrorsAreRetryableAsDaemonText(t *testing.T) {
	t.Parallel()
	// Daemon errors reach the client as text, and multi-file runs join one
	// error per rejected file, so recognition must survive both.
	retryable := []string{
		"source generation changed from 1 to 2; rediscover and retry",
		"project session was retired before its work ran; rediscover and retry",
		"source generation changed from 3 to 4; rediscover and retry\ncontext canceled",
	}
	for _, message := range retryable {
		assert.True(t, isStaleGenerationError(errors.New(message)), message)
	}
	assert.False(t, isStaleGenerationError(errors.New("work belongs to an obsolete generation")))
	assert.False(t, isStaleGenerationError(errors.New("worker exited")))
	assert.False(t, isStaleGenerationError(nil))
}

func TestRunAndWatchRejectInvalidReportOptionsBeforeProjectAccess(t *testing.T) {
	t.Parallel()

	existingDirectory := t.TempDir()
	missingProject := filepath.Join(t.TempDir(), "does-not-exist")

	cases := []struct {
		name      string
		arguments []string
		wantError string
	}{
		{
			name:      "run unknown format",
			arguments: []string{"run", "--project", missingProject, "--report-format", "tap"},
			wantError: `report format "tap" must be terminal, jsonl, or junit`,
		},
		{
			name:      "watch unknown format",
			arguments: []string{"watch", "--project", missingProject, "--report-format", "tap"},
			wantError: `report format "tap" must be terminal, jsonl, or junit`,
		},
		{
			name:      "json and report-format mutually exclusive",
			arguments: []string{"run", "--project", missingProject, "--json", "--report-format", "junit"},
			wantError: "if any flags in the group [json report-format] are set none of the others can be",
		},
		{
			name:      "report-file without report-format",
			arguments: []string{"run", "--project", missingProject, "--report-file", filepath.Join(t.TempDir(), "junit.xml")},
			wantError: "--report-file requires --report-format",
		},
		{
			name:      "report-file points to directory",
			arguments: []string{"run", "--project", missingProject, "--report-format", "junit", "--report-file", existingDirectory},
			wantError: "is a directory",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(context.Background(), testCase.arguments, IO{Out: &stdout, Err: &stderr})

			require.Equal(t, ExitError, exitCode)
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), testCase.wantError)
		})
	}
}

func TestRunConfigurationFailurePreservesExistingReportFile(t *testing.T) {
	t.Parallel()

	root := newTestProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "gdtest.json"), []byte(`{"unknown_field": true}`), 0o600))
	reportPath := filepath.Join(t.TempDir(), "existing-junit.xml")
	require.NoError(t, os.WriteFile(reportPath, []byte("<preserved/>"), 0o600))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"run", "--project", root, "--no-daemon", "--report-format", "junit", "--report-file", reportPath,
	}, IO{Out: &stdout, Err: &stderr})

	require.Equal(t, ExitError, exitCode)
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	assert.Equal(t, "<preserved/>", string(data))
}

func TestRunEmptySelectionsRenderRequestedReportFormatAndReportFile(t *testing.T) {
	t.Parallel()

	type xmlSuites struct {
		XMLName  xml.Name `xml:"testsuites"`
		Tests    int      `xml:"tests,attr"`
		Failures int      `xml:"failures,attr"`
		Errors   int      `xml:"errors,attr"`
		Skipped  int      `xml:"skipped,attr"`
	}

	t.Run("junit stdout on allow-empty", func(t *testing.T) {
		t.Parallel()
		root := newTestProject(t)
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run(context.Background(), []string{
			"run", "--project", root, "--allow-empty", "--no-daemon", "--report-format", "junit",
		}, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitOK, exitCode, stderr.String())
		assert.Empty(t, stderr.String())
		var decoded xmlSuites
		require.NoError(t, xml.Unmarshal(stdout.Bytes(), &decoded))
		assert.Equal(t, 0, decoded.Tests)
	})

	t.Run("junit report-file on empty affected keeps terminal stdout", func(t *testing.T) {
		t.Parallel()
		root := newTestProject(t)
		reportPath := filepath.Join(t.TempDir(), "nested", "reports", "junit.xml")
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run(context.Background(), []string{
			"run", "--project", root, "--affected", "README.md", "--no-daemon",
			"--report-format", "junit", "--report-file", reportPath,
		}, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitOK, exitCode, stderr.String())
		assert.Equal(t, "no tests were affected\n", stdout.String())
		assert.Empty(t, stderr.String())

		reportBytes, err := os.ReadFile(reportPath)
		require.NoError(t, err)
		var decoded xmlSuites
		require.NoError(t, xml.Unmarshal(reportBytes, &decoded))
		assert.Equal(t, 0, decoded.Tests)
	})

	t.Run("jsonl stdout and report-file on empty selections", func(t *testing.T) {
		t.Parallel()
		root := newTestProject(t)
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := Run(context.Background(), []string{
			"run", "--project", root, "--affected", "README.md", "--no-daemon", "--report-format", "jsonl",
		}, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitOK, exitCode, stderr.String())
		assert.JSONEq(t, `{"type":"summary","passed":0,"message":"no tests were affected"}`, stdout.String())

		reportPath := filepath.Join(t.TempDir(), "results.jsonl")
		stdout.Reset()
		stderr.Reset()
		exitCode = Run(context.Background(), []string{
			"run", "--project", root, "--allow-empty", "--no-daemon",
			"--report-format", "jsonl", "--report-file", reportPath,
		}, IO{Out: &stdout, Err: &stderr})

		require.Equal(t, ExitOK, exitCode, stderr.String())
		assert.Equal(t, "No tests selected.\n", stdout.String())
		reportBytes, err := os.ReadFile(reportPath)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"summary","passed":0,"message":"No tests selected."}`, string(reportBytes))
	})
}

func TestReportSessionRoutesStdoutAndAtomicReportFileIndependently(t *testing.T) {
	t.Parallel()

	t.Run("junit report-file keeps terminal stdout and verbose stderr", func(t *testing.T) {
		t.Parallel()
		reportPath := filepath.Join(t.TempDir(), "artifacts", "junit.xml")
		configuration, err := resolveReportConfig(runOptions{
			reportFormat: "junit",
			reportFile:   reportPath,
			verbose:      true,
		})
		require.NoError(t, err)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		session := configuration.newSession(&stdout)
		collector := results.NewCollector()

		failureEvent := model.Event{
			Type:      model.EventAssertionFail,
			TestID:    "tests/math.test.gd::test_sum",
			Assertion: "equal",
			Message:   "sum mismatch",
			Location:  &model.Location{File: "tests/math.test.gd", Line: 11},
			Expected:  &model.WireValue{Type: "int", Value: "4"},
			Actual:    &model.WireValue{Type: "int", Value: "5"},
		}
		finishedEvent := model.Event{
			Type:     model.EventCaseFinished,
			TestID:   "tests/math.test.gd::test_sum",
			Name:     "sum",
			Status:   "fail",
			Location: &model.Location{File: "tests/math.test.gd", Line: 9},
			Duration: 2 * time.Millisecond,
		}
		for _, event := range []model.Event{failureEvent, finishedEvent} {
			require.NoError(t, collector.Emit(event))
			require.NoError(t, session.Emit(event))
		}

		summary := collector.Summary()
		require.NoError(t, session.Summary(summary))
		require.NoError(t, session.CompleteTimingsAndPublish(
			IO{Out: &stdout, Err: &stderr},
			map[string]time.Duration{"reporting": time.Millisecond},
		))

		assert.Contains(t, stdout.String(), "FAIL tests/math.test.gd::test_sum")
		assert.Contains(t, stdout.String(), "0 passed, 1 failed")
		assert.Contains(t, stderr.String(), "gdtest: timing reporting=1ms")

		reportBytes, err := os.ReadFile(reportPath)
		require.NoError(t, err)
		var decoded struct {
			XMLName  xml.Name `xml:"testsuites"`
			Tests    int      `xml:"tests,attr"`
			Failures int      `xml:"failures,attr"`
		}
		require.NoError(t, xml.Unmarshal(reportBytes, &decoded))
		assert.Equal(t, 1, decoded.Tests)
		assert.Equal(t, 1, decoded.Failures)
	})

	t.Run("junit stdout with verbose keeps timings off stdout", func(t *testing.T) {
		t.Parallel()
		configuration, err := resolveReportConfig(runOptions{
			reportFormat: "junit",
			verbose:      true,
		})
		require.NoError(t, err)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		session := configuration.newSession(&stdout)
		require.NoError(t, session.Summary(results.Summary{Passed: 1, Cases: []results.CaseResult{
			{ID: "tests/ok.test.gd::test_ok", Name: "ok", Status: "pass", File: "tests/ok.test.gd", Line: 3},
		}}))
		require.NoError(t, session.CompleteTimingsAndPublish(
			IO{Out: &stdout, Err: &stderr},
			map[string]time.Duration{"client_total": 3 * time.Millisecond},
		))

		var decoded struct {
			XMLName xml.Name `xml:"testsuites"`
			Tests   int      `xml:"tests,attr"`
		}
		require.NoError(t, xml.Unmarshal(stdout.Bytes(), &decoded))
		assert.Equal(t, 1, decoded.Tests)
		assert.Contains(t, stderr.String(), "gdtest: timing client_total=3ms")
	})

	t.Run("jsonl report-file appends timings record to file", func(t *testing.T) {
		t.Parallel()
		reportPath := filepath.Join(t.TempDir(), "results.jsonl")
		configuration, err := resolveReportConfig(runOptions{
			json:       true,
			reportFile: reportPath,
		})
		require.NoError(t, err)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		session := configuration.newSession(&stdout)
		require.NoError(t, session.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventCaseFinished,
			TestID:          "tests/ok.test.gd::test_ok",
			Status:          "pass",
		}))
		require.NoError(t, session.Summary(results.Summary{Passed: 1}))
		require.NoError(t, session.CompleteTimingsAndPublish(
			IO{Out: &stdout, Err: &stderr},
			map[string]time.Duration{"reporting": time.Millisecond},
		))

		assert.Contains(t, stdout.String(), "1 passed, 0 failed")
		reportBytes, err := os.ReadFile(reportPath)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(reportBytes)), "\n")
		require.Len(t, lines, 3)
		assert.Contains(t, lines[0], `"type":"case_finished"`)
		assert.Contains(t, lines[1], `"type":"summary"`)
		assert.Contains(t, lines[2], `"type":"timings"`)
	})

	t.Run("manifest errors render valid junit on stdout and report-file", func(t *testing.T) {
		t.Parallel()
		reportPath := filepath.Join(t.TempDir(), "discovery-junit.xml")
		configuration, err := resolveReportConfig(runOptions{
			reportFormat: "junit",
			reportFile:   reportPath,
		})
		require.NoError(t, err)

		var stdout bytes.Buffer
		findings := &manifestErrors{
			errors: []model.DiscoveryError{{
				Code:     "discovery.only_forbidden",
				Message:  "focused test is forbidden by --only forbid",
				Location: &model.Location{File: "tests/focus.test.gd", Line: 6, Column: 2},
			}},
		}
		require.NoError(t, configuration.writeManifestErrors(&stdout, findings))
		assert.Contains(t, stdout.String(), "tests/focus.test.gd:6:2: discovery.only_forbidden")

		reportBytes, err := os.ReadFile(reportPath)
		require.NoError(t, err)
		var decoded struct {
			XMLName xml.Name `xml:"testsuites"`
			Tests   int      `xml:"tests,attr"`
			Errors  int      `xml:"errors,attr"`
		}
		require.NoError(t, xml.Unmarshal(reportBytes, &decoded))
		assert.Equal(t, 1, decoded.Tests)
		assert.Equal(t, 1, decoded.Errors)
	})
}
