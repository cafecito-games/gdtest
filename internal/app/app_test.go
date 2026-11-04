package app

import (
	"bytes"
	"context"
	"os"
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
		"daemon", "doctor", "init", "list", "migrate", "run", "version", "warm", "watch",
	} {
		assert.Contains(t, stdout.String(), command)
	}
	assert.NotContains(t, stdout.String(), "completion")
	assert.Contains(t, stdout.String(), "--version")
	assert.NotContains(t, stdout.String(), "-v, --version")
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
			name: "infrastructure takes precedence",
			summary: results.Summary{
				PolicyFailures: []results.PolicyFailure{{Status: "void", Count: 1}},
				Infrastructure: []model.Event{{Type: model.EventWorkerError}},
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
	assert.Equal(t, "usage: gdtest <init|run|list|watch|warm|doctor|migrate|daemon> [options]\n", stderr.String())
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
