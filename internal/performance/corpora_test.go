package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiredCorpusSizesAndIdentities(t *testing.T) {
	t.Parallel()
	assert.Contains(t, PassingAssertionsScript(), "range(10000)")
	assert.Contains(t, PassingAssertionsScript(), "expect_true(index >= 0)")
	assert.NotContains(t, PassingAssertionsScript(), "ok(index >= 0)")
	assert.Contains(t, DynamicCasesScript(), "range(10000)")
	assert.Len(t, ThousandFiles(), FileCount)
	assert.Len(t, TenThousandCases().Files[0].Cases, LargeCount)
	assert.Len(t, DeepSuiteManifest().Files[0].Cases[0].Suites, SuiteDepth)
	assert.Len(t, RichFailures(LargeCount), LargeCount)
	assert.Len(t, SessionPlans(), SessionCount)
}

func TestPassingAssertionCorpusIsStaticallyDiscoverable(t *testing.T) {
	t.Parallel()

	plan, err := discovery.Parse(
		"tests/passing_assertions.test.gd",
		[]byte(PassingAssertionsScript()),
	)
	require.NoError(t, err)
	require.Len(t, plan.Cases, 1)
	assert.Equal(t, "test_passing_assertions", plan.Cases[0].MethodName)
	assert.Equal(t, "tests/passing_assertions.test.gd::test_passing_assertions", plan.Cases[0].ID)
}

func TestDynamicCorpusIsDynamicOnlyWithTenThousandRegistrations(t *testing.T) {
	t.Parallel()

	source := DynamicCasesScript()
	plan, err := discovery.Parse("tests/dynamic_cases.test.gd", []byte(source))
	require.NoError(t, err)
	assert.Empty(t, plan.Cases)
	assert.Contains(t, source, "range(10000)")
}

func TestThousandFileCorpusParsesDeterministically(t *testing.T) {
	t.Parallel()
	files := ThousandFiles()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file, err := discovery.Parse(path, files[path])
		require.NoError(t, err)
		require.Len(t, file.Cases, 1)
		assert.Contains(t, file.Cases[0].ID, path+"::test_")
	}
}

func TestSerializedManifestGrowthTracksCaseCount(t *testing.T) {
	t.Parallel()

	small, err := json.Marshal(CaseManifest(1_000))
	require.NoError(t, err)
	large, err := json.Marshal(CaseManifest(10_000))
	require.NoError(t, err)

	ratio := float64(len(large)) / float64(len(small))
	assert.Greater(t, ratio, 9.0)
	assert.Less(t, ratio, 11.0)
}

func TestSummarizeUsesNearestRankWithoutMutatingSamples(t *testing.T) {
	t.Parallel()

	samples := []time.Duration{9, 1, 5, 3, 7, 11, 13, 15, 17, 19}
	original := append([]time.Duration(nil), samples...)
	distribution, err := Summarize(samples)
	require.NoError(t, err)

	assert.Equal(t, original, samples)
	assert.Equal(t, Distribution{
		Samples: 10,
		Min:     1,
		Median:  9,
		P95:     19,
		Max:     19,
	}, distribution)
	_, err = Summarize(nil)
	require.EqualError(t, err, "performance distribution requires at least one sample")
}

func BenchmarkParseThousandFiles(b *testing.B) {
	files := ThousandFiles()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	b.ReportAllocs()
	for b.Loop() {
		for _, path := range paths {
			if _, err := discovery.Parse(path, files[path]); err != nil {
				b.StopTimer()
				require.NoError(b, err)
			}
		}
	}
}

func BenchmarkSerializeCases(b *testing.B) {
	for _, count := range []int{1_000, 5_000, LargeCount} {
		b.Run(fmt.Sprintf("cases_%d", count), func(b *testing.B) {
			manifest := CaseManifest(count)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := json.Marshal(manifest); err != nil {
					b.StopTimer()
					require.NoError(b, err)
				}
			}
		})
	}
}

func BenchmarkFoldRichFailures(b *testing.B) {
	for _, count := range []int{1_000, 5_000, LargeCount} {
		b.Run(fmt.Sprintf("failures_%d", count), func(b *testing.B) {
			events := RichFailures(count)
			b.ReportAllocs()
			for b.Loop() {
				collector := results.NewCollector()
				for _, event := range events {
					if err := collector.Emit(event); err != nil {
						b.StopTimer()
						require.NoError(b, err)
					}
				}
				_ = collector.Summary()
			}
		})
	}
}

func BenchmarkScheduleManySessions(b *testing.B) {
	plans := SessionPlans()
	b.ReportAllocs()
	for b.Loop() {
		runner, err := scheduler.New(scheduler.Options{Capacity: 4})
		if err != nil {
			b.StopTimer()
			require.NoError(b, err)
		}
		futures := make([]scheduler.Future, 0, len(plans))
		for _, plan := range plans {
			futures = append(futures, runner.Submit(context.Background(), scheduler.Request{
				Session: plan.RunID, Generation: plan.Generation, Priority: scheduler.Interactive,
				Run: func(context.Context, scheduler.Lease) error { return nil },
			}))
		}
		for _, future := range futures {
			result, waitErr := future.Wait(context.Background())
			if waitErr != nil || result.Err != nil {
				b.StopTimer()
				require.NoError(b, waitErr)
				require.NoError(b, result.Err)
			}
		}
		if err := runner.Shutdown(context.Background()); err != nil {
			b.StopTimer()
			require.NoError(b, err)
		}
	}
}

func BenchmarkSerializeDeepSuites(b *testing.B) {
	manifest := DeepSuiteManifest()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(manifest); err != nil {
			b.StopTimer()
			require.NoError(b, err)
		}
	}
}
