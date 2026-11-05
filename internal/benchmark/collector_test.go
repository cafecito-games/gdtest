package benchmark

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorDiscardsWarmupsAndBuildsDistribution(t *testing.T) {
	t.Parallel()

	plan := model.BenchmarkPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 3,
		ContentFingerprint: "fingerprint", Warmup: 1, Samples: 3, Iterations: 2,
		Files: []model.BenchmarkFilePlan{{Path: "bench/value.bench.gd", Benchmarks: []model.BenchmarkCasePlan{{
			ID: "bench/value.bench.gd::benchmark_value", Name: "Value", MethodName: "benchmark_value", Line: 4,
		}}}},
	}
	collector := NewCollector(plan)
	events := []model.BenchmarkEvent{
		{Type: model.EventBenchmarkRunStarted, RunID: "run"},
		{Type: model.EventBenchmarkStarted, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value"},
		{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", Warmup: true, Iterations: 2},
		{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", Warmup: true, DurationNS: 999, Iterations: 2},
		{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", Iterations: 2},
		{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", DurationNS: 30, Iterations: 2},
		{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", SampleIndex: 1, Iterations: 2},
		{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", SampleIndex: 1, DurationNS: 10, Iterations: 2},
		{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", SampleIndex: 2, Iterations: 2},
		{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", SampleIndex: 2, DurationNS: 20, Iterations: 2},
		{Type: model.EventBenchmarkFinished, RunID: "run", BenchmarkID: "bench/value.bench.gd::benchmark_value", Status: "pass"},
		{Type: model.EventBenchmarkRunFinished, RunID: "run", Status: "pass"},
	}
	for _, event := range events {
		require.NoError(t, collector.EmitBenchmark(event))
	}
	results, err := collector.Results(Environment{LogicalCPUCount: 4})
	require.NoError(t, err)
	require.Len(t, results.Benchmarks, 1)
	assert.Equal(t, []int64{30, 10, 20}, results.Benchmarks[0].Samples.RawSamplesNS)
	assert.Equal(t, int64(20), results.Benchmarks[0].Samples.MedianNS)
	assert.Equal(t, uint64(3), results.Benchmarks[0].Generation)
}

func TestCollectorRejectsIncompleteAndDuplicateSamples(t *testing.T) {
	t.Parallel()

	plan := model.BenchmarkPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "run", Generation: 1, Samples: 2, Iterations: 1,
		Files: []model.BenchmarkFilePlan{{Path: "a.bench.gd", Benchmarks: []model.BenchmarkCasePlan{{ID: "a.bench.gd::benchmark_a"}}}},
	}
	collector := NewCollector(plan)
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkRunStarted, RunID: "run"}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkStarted, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a"}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", Iterations: 1}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", DurationNS: 1, Iterations: 1}))
	err := collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", Iterations: 1})
	require.Error(t, err)
	assert.ErrorContains(t, err, "out-of-order")

	collector = NewCollector(plan)
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkRunStarted, RunID: "run"}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkStarted, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a"}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkSampleStarted, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", Iterations: 1}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkSample, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", DurationNS: 1, Iterations: 1}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkFinished, RunID: "run", BenchmarkID: "a.bench.gd::benchmark_a", Status: "pass"}))
	require.NoError(t, collector.EmitBenchmark(model.BenchmarkEvent{Type: model.EventBenchmarkRunFinished, RunID: "run", Status: "pass"}))
	_, err = collector.Results(Environment{LogicalCPUCount: 1})
	require.Error(t, err)
	assert.ErrorContains(t, err, "incomplete")
}
