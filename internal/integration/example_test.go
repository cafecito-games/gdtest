package integration_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/cafecito-games/gdtest/internal/benchmark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentedBasicExampleExecutes(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot example test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repository := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "doubles"), 0o750))
	for _, relative := range []string{
		"project.godot",
		"gdtest.json",
		"math.test.gd",
		"math.bench.gd",
		"signal_probe.test.gd",
		"inventory.gd",
		"shop.gd",
		"inventory_double.test.gd",
		"doubles/inventory_double.gd",
	} {
		data, err := os.ReadFile(filepath.Join(repository, "examples", "basic", filepath.FromSlash(relative)))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), data, 0o600))
	}
	require.NoError(t, gdtest.InstallRuntime(root))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code := app.Run(ctx, []string{
		"run", "--no-daemon", "--project", root,
	}, app.IO{Out: &output, Err: &diagnostics})
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "10 passed")

	output.Reset()
	diagnostics.Reset()
	baseline := filepath.Join(root, "benchmark-baseline.json")
	code = app.Run(ctx, []string{
		"bench", "--no-daemon", "--project", root,
		"--warmup", "1", "--samples", "3", "--iterations", "2",
		"--write-baseline", baseline,
	}, app.IO{Out: &output, Err: &diagnostics})
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "math.bench.gd::benchmark_sort_integers")
	assert.Contains(t, output.String(), "samples=3 iterations=2")
	baselineData, err := os.ReadFile(baseline)
	require.NoError(t, err)
	assert.NotContains(t, string(baselineData), root)
	decoded, err := benchmark.Decode(baselineData)
	require.NoError(t, err)
	require.Len(t, decoded.Benchmarks, 1)
	assert.Equal(t, "math.bench.gd::benchmark_sort_integers", decoded.Benchmarks[0].ID)
	assert.Len(t, decoded.Benchmarks[0].Samples.RawSamplesNS, 3)

	output.Reset()
	diagnostics.Reset()
	code = app.Run(ctx, []string{
		"bench", "--no-daemon", "--project", root,
		"--warmup", "0", "--samples", "2", "--iterations", "1",
		"--compare", baseline,
	}, app.IO{Out: &output, Err: &diagnostics})
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output.String(), diagnostics.String())
	assert.Contains(t, output.String(), "compare math.bench.gd::benchmark_sort_integers delta=")
}
