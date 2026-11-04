package performance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/performance"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const performanceEnvironment = "GDTEST_RUN_PERFORMANCE"

type corpusReport struct {
	Corpus       string                   `json:"corpus"`
	Items        int                      `json:"items"`
	Distribution performance.Distribution `json:"distribution"`
}

func TestGodotPerformanceCorpora(t *testing.T) {
	if os.Getenv(performanceEnvironment) != "1" {
		t.Skip("Godot performance corpora run through scripts/benchmark")
	}
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot performance corpora skipped: godot is not on PATH")
	}

	sampleCount := performanceSampleCount(t)
	root := performanceProject(t)
	assertionFile := writeCorpus(t, root, "tests/passing_assertions.test.gd", performance.PassingAssertionsScript())
	dynamicFile := writeCorpus(t, root, "tests/dynamic_cases.test.gd", performance.DynamicCasesScript())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: "godot", Project: root, SessionID: "performance-corpora", Generation: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		assert.NoError(t, process.Shutdown(shutdownCtx))
	})

	assertionPlan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "assertion-corpus", Generation: 1,
		ContentFingerprint: "assertion-corpus", Files: []model.FilePlan{assertionFile},
	}
	dynamicPlan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "dynamic-case-corpus", Generation: 1,
		ContentFingerprint: "dynamic-case-corpus", Files: []model.FilePlan{dynamicFile},
	}

	var dynamicIDs []string
	discoverySamples := sampleDurations(t, sampleCount, func() {
		manifest, discoverErr := process.Discover(ctx, dynamicPlan)
		require.NoError(t, discoverErr)
		require.Empty(t, manifest.Errors)
		require.Len(t, manifest.Files, 1)
		assert.Len(t, manifest.Files[0].Cases, performance.LargeCount)
		dynamicIDs = dynamicIDs[:0]
		for _, testCase := range manifest.Files[0].Cases {
			if testCase.MethodName == "" {
				dynamicIDs = append(dynamicIDs, testCase.ID)
			}
		}
		assert.Len(t, dynamicIDs, performance.LargeCount)
	})
	dynamicPlan.SelectionApplied = true
	dynamicPlan.SelectedIDs = append([]string(nil), dynamicIDs...)
	assertionSamples := sampleDurations(t, sampleCount, func() {
		runAssertionCorpus(t, ctx, process, assertionPlan)
		require.NoError(t, process.Reset(ctx))
	})
	dynamicSamples := sampleDurations(t, sampleCount, func() {
		runDynamicCorpus(t, ctx, process, dynamicPlan)
		require.NoError(t, process.Reset(ctx))
	})

	logCorpusReport(t, "dynamic_discovery", performance.LargeCount, discoverySamples)
	logCorpusReport(t, "passing_assertions", performance.LargeCount, assertionSamples)
	logCorpusReport(t, "dynamic_case_execution", performance.LargeCount, dynamicSamples)
}

func performanceSampleCount(t *testing.T) int {
	t.Helper()
	const fallback = 5
	value := os.Getenv("GDTEST_PERFORMANCE_SAMPLES")
	if value == "" {
		return fallback
	}
	count, err := strconv.Atoi(value)
	require.NoError(t, err, "GDTEST_PERFORMANCE_SAMPLES must be an integer")
	require.GreaterOrEqual(t, count, 1)
	require.LessOrEqual(t, count, 50)
	return count
}

func performanceProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	project := []byte("config_version=5\n\n[application]\nconfig/name=\"gdtest performance\"\n\n[rendering]\nrenderer/rendering_method=\"gl_compatibility\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), project, 0o600))
	require.NoError(t, gdtest.InstallRuntime(root))
	return root
}

func writeCorpus(t *testing.T, root, relative, source string) model.FilePlan {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(source), 0o600))
	file, err := discovery.Parse(relative, []byte(source))
	require.NoError(t, err)
	return file
}

func sampleDurations(t *testing.T, count int, operation func()) []time.Duration {
	t.Helper()
	operation() // Warm the persistent worker and discard cold script/import costs.
	samples := make([]time.Duration, count)
	for index := range samples {
		started := time.Now()
		operation()
		samples[index] = time.Since(started)
	}
	return samples
}

func runAssertionCorpus(
	t *testing.T,
	ctx context.Context,
	process *worker.Process,
	plan model.TestPlan,
) {
	t.Helper()
	finished := 0
	err := process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		if event.Type != model.EventCaseFinished {
			return nil
		}
		finished++
		if event.Status != "pass" || event.Assertions != performance.LargeCount {
			return fmt.Errorf("assertion corpus finished status=%s assertions=%d", event.Status, event.Assertions)
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, finished)
}

func runDynamicCorpus(
	t *testing.T,
	ctx context.Context,
	process *worker.Process,
	plan model.TestPlan,
) {
	t.Helper()
	started := 0
	finished := 0
	err := process.Run(ctx, plan, executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventCaseStarted:
			started++
		case model.EventCaseFinished:
			finished++
			if event.Status != "void" {
				return fmt.Errorf("dynamic case %s finished with %s", event.TestID, event.Status)
			}
		}
		return nil
	}))
	require.NoError(t, err)
	assert.Equal(t, performance.LargeCount, started)
	assert.Equal(t, performance.LargeCount, finished)
}

func logCorpusReport(t *testing.T, corpus string, items int, samples []time.Duration) {
	t.Helper()
	distribution, err := performance.Summarize(samples)
	require.NoError(t, err)
	encoded, err := json.Marshal(corpusReport{
		Corpus: corpus, Items: items, Distribution: distribution,
	})
	require.NoError(t, err)
	t.Logf("gdtest_performance=%s", encoded)
}

func TestPerformanceReportIncludesRuntimeEnvironment(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(t, runtime.GOOS)
	assert.NotEmpty(t, runtime.GOARCH)
}
