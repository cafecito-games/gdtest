package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverBenchmarksResolvesInheritanceAndStableIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "bench/base.gd", "class_name ProjectBenchmark\nextends GDTestBenchmark\n")
	writeFixture(t, root, "bench/math.bench.gd", `extends ProjectBenchmark

func before_benchmark(info: GDTestBenchmarkInfo) -> void:
	pass

func benchmark_addition() -> void:
	pass

func helper() -> void:
	pass
`)
	manifest, err := DiscoverBenchmarks(root, BenchmarkOptions{})
	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	require.Len(t, manifest.Files[0].Benchmarks, 1)
	assert.Equal(t, "bench/math.bench.gd::benchmark_addition", manifest.Files[0].Benchmarks[0].ID)
	assert.Equal(t, 6, manifest.Files[0].Benchmarks[0].Line)
}

func TestDiscoverBenchmarksRejectsInvalidSignaturesWithLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"arguments", "extends GDTestBenchmark\nfunc benchmark_bad(value: int) -> void:\n\tpass\n", "bad.bench.gd:2:"},
		{"return", "extends GDTestBenchmark\nfunc benchmark_bad() -> int:\n\treturn 1\n", "must return void"},
		{"hook type", "extends GDTestBenchmark\nfunc before_sample(info: String) -> void:\n\tpass\n", "GDTestBenchmarkInfo"},
		{"hook pair", "extends GDTestBenchmark\nfunc before_benchmark() -> void:\n\tpass\n", "must take 1 argument"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFixture(t, root, "bad.bench.gd", testCase.source)
			_, err := DiscoverBenchmarks(root, BenchmarkOptions{})
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestSelectBenchmarksUsesTestSelectorSemantics(t *testing.T) {
	t.Parallel()

	manifest := BenchmarkManifest{Files: []BenchmarkFile{
		{Path: "bench/a.bench.gd", Benchmarks: []BenchmarkCase{{ID: "bench/a.bench.gd::benchmark_a", MethodName: "benchmark_a"}}},
		{Path: "bench/nested/b.bench.gd", Benchmarks: []BenchmarkCase{{ID: "bench/nested/b.bench.gd::benchmark_b", MethodName: "benchmark_b"}}},
	}}
	selected, err := SelectBenchmarks(manifest, []string{"bench/**/b.bench.gd"}, false)
	require.NoError(t, err)
	require.Len(t, selected.Files, 1)
	assert.Equal(t, "bench/nested/b.bench.gd", selected.Files[0].Path)

	selected, err = SelectBenchmarks(manifest, []string{`bench\a.bench.gd::benchmark_a`}, false)
	require.NoError(t, err)
	require.Len(t, selected.Files, 1)
	assert.Equal(t, "bench/a.bench.gd", selected.Files[0].Path)

	_, err = SelectBenchmarks(manifest, []string{"../outside.bench.gd"}, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "escapes the project root")

	_, err = SelectBenchmarks(manifest, []string{"bench/[.bench.gd"}, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid selector")
}
