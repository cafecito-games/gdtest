package benchmark

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeDeterministicBoundaries(t *testing.T) {
	t.Parallel()

	statistics, err := Summarize([]int64{90, 10, 50, 30})
	require.NoError(t, err)
	assert.Equal(t, int64(10), statistics.MinNS)
	assert.Equal(t, int64(40), statistics.MedianNS)
	assert.Equal(t, int64(90), statistics.P95NS)
	assert.Equal(t, int64(90), statistics.MaxNS)
	assert.Equal(t, []int64{90, 10, 50, 30}, statistics.RawSamplesNS)

	_, err = Summarize(nil)
	require.Error(t, err)
	_, err = Summarize([]int64{-1})
	require.Error(t, err)
}

func TestSummarizeP95UsesNearestRankAtBoundaryCounts(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		count int
		want  int64
	}{
		{count: 1, want: 1},
		{count: 2, want: 2},
		{count: 19, want: 19},
		{count: 20, want: 19},
		{count: 21, want: 20},
	} {
		samples := make([]int64, testCase.count)
		for index := range samples {
			samples[index] = int64(index + 1)
		}
		statistics, err := Summarize(samples)
		require.NoError(t, err)
		assert.Equal(t, testCase.want, statistics.P95NS, "sample count %d", testCase.count)
	}
}

func TestBaselineRoundTripIsCanonicalAndSorted(t *testing.T) {
	t.Parallel()

	baseline := ResultSet{
		SchemaVersion: SchemaVersion,
		Environment:   Environment{GDTestVersion: "1.2.3", OS: "linux", Architecture: "amd64", LogicalCPUCount: 8},
		Benchmarks: []Result{
			{ID: "z.bench.gd::benchmark_z", Generation: 1, ContentFingerprint: "z", Samples: Statistics{RawSamplesNS: []int64{2}, MinNS: 2, MedianNS: 2, P95NS: 2, MaxNS: 2}, SampleCount: 1, Iterations: 1},
			{ID: "a.bench.gd::benchmark_a", Generation: 1, ContentFingerprint: "a", Samples: Statistics{RawSamplesNS: []int64{1}, MinNS: 1, MedianNS: 1, P95NS: 1, MaxNS: 1}, SampleCount: 1, Iterations: 1},
		},
	}
	first, err := MarshalCanonical(baseline)
	require.NoError(t, err)
	second, err := MarshalCanonical(baseline)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Less(t, bytes.Index(first, []byte(`"id":"a.bench.gd`)), bytes.Index(first, []byte(`"id":"z.bench.gd`)))

	decoded, err := Decode(first)
	require.NoError(t, err)
	assert.Equal(t, "a.bench.gd::benchmark_a", decoded.Benchmarks[0].ID)
}

func TestDecodeRejectsMalformedBaseline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{"schema", `{"schema_version":99,"environment":{"logical_cpu_count":1},"benchmarks":[]}`, "schema"},
		{"duplicate", validBaselineJSON(`[ {"id":"same","generation":1,"content_fingerprint":"x","sample_count":1,"iterations":1,"samples":{"min_ns":1,"median_ns":1,"p95_ns":1,"max_ns":1,"raw_samples_ns":[1]}}, {"id":"same","generation":1,"content_fingerprint":"x","sample_count":1,"iterations":1,"samples":{"min_ns":1,"median_ns":1,"p95_ns":1,"max_ns":1,"raw_samples_ns":[1]}} ]`), "duplicate"},
		{"negative", validBaselineJSON(`[ {"id":"bad","generation":1,"content_fingerprint":"x","sample_count":1,"iterations":1,"samples":{"min_ns":-1,"median_ns":1,"p95_ns":1,"max_ns":1,"raw_samples_ns":[1]}} ]`), "negative"},
		{"unknown", `{"schema_version":1,"environment":{"logical_cpu_count":1},"benchmarks":[],"unknown":true}`, "unknown"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(testCase.data))
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestCompareReportsMatchedAddedAndRemoved(t *testing.T) {
	t.Parallel()

	baseline := ResultSet{SchemaVersion: SchemaVersion, Benchmarks: []Result{
		{ID: "kept", Samples: Statistics{MedianNS: 100}},
		{ID: "removed", Samples: Statistics{MedianNS: 50}},
	}}
	current := ResultSet{SchemaVersion: SchemaVersion, Benchmarks: []Result{
		{ID: "added", Samples: Statistics{MedianNS: 10}},
		{ID: "kept", Samples: Statistics{MedianNS: 125}},
	}}
	comparison := Compare(current, baseline)
	require.Len(t, comparison.Matched, 1)
	assert.Equal(t, int64(25), comparison.Matched[0].DeltaNS)
	assert.Equal(t, 25.0, comparison.Matched[0].DeltaPercent)
	assert.Equal(t, []string{"added"}, comparison.Added)
	assert.Equal(t, []string{"removed"}, comparison.Removed)
}

func TestAtomicWritePreservesExistingBaselineOnFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "baseline.json")
	require.NoError(t, os.WriteFile(target, []byte("original"), 0o600))
	bad := ResultSet{SchemaVersion: SchemaVersion, Benchmarks: []Result{{
		ID: "bad", SampleCount: 1, Iterations: 1,
		Samples: Statistics{MinNS: 1, MedianNS: 1, P95NS: 1, MaxNS: 1},
	}}}
	err := WriteAtomic(target, bad)
	require.Error(t, err)
	data, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(data))
}

func validBaselineJSON(benchmarks string) string {
	return `{"schema_version":1,"environment":{"logical_cpu_count":1},"benchmarks":` + benchmarks + `}`
}
