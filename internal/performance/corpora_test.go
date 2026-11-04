package performance

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiredCorpusSizesAndIdentities(t *testing.T) {
	t.Parallel()
	assert.Contains(t, PassingAssertionsScript(), "range(10000)")
	assert.Contains(t, DynamicCasesScript(), "range(10000)")
	assert.Len(t, ThousandFiles(), FileCount)
	assert.Len(t, TenThousandCases().Files[0].Cases, LargeCount)
	assert.Len(t, DeepSuiteManifest().Files[0].Cases[0].Suites, SuiteDepth)
	assert.Len(t, RichFailures(LargeCount), LargeCount)
	assert.Len(t, SessionPlans(), SessionCount)
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

func BenchmarkSerializeTenThousandCases(b *testing.B) {
	manifest := TenThousandCases()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(manifest); err != nil {
			b.StopTimer()
			require.NoError(b, err)
		}
	}
}

func BenchmarkFoldTenThousandRichFailures(b *testing.B) {
	events := RichFailures(LargeCount)
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
