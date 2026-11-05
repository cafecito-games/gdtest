package gdtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallRuntimeIdempotentAndConflictSafe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, InstallRuntime(root))
	require.NoError(t, InstallRuntime(root))
	path := filepath.Join(root, "addons", "gdtest", "gdtest.gd")
	require.NoError(t, os.WriteFile(path, []byte("modified\n"), 0o644))
	require.ErrorContains(t, InstallRuntime(root), "refusing")
}

func TestInstallRuntimeRefusesGPMOwnership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := []byte("[addons.gdtest]\nversion = \"v1.0.0\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons.toml"), manifest, 0o644))
	require.ErrorContains(t, InstallRuntime(root), "gpm install")
}

func TestRuntimeFileContentsUsesTheBinaryProductVersion(t *testing.T) {
	t.Parallel()

	source := []byte("extends RefCounted\nconst PRODUCT_VERSION: String = \"0.0.0-dev\"\n")
	materialized, err := runtimeFileContents("addons/gdtest/version.gd", source, "1.2.3-rc.1")
	require.NoError(t, err)
	assert.Contains(t, string(materialized), `const PRODUCT_VERSION: String = "1.2.3-rc.1"`)
	assert.NotContains(t, string(materialized), "0.0.0-dev")

	unchanged, err := runtimeFileContents("addons/gdtest/worker.gd", source, "1.2.3-rc.1")
	require.NoError(t, err)
	assert.Equal(t, source, unchanged)
}

func TestRuntimeFileContentsRejectsUnsafeOrMalformedVersions(t *testing.T) {
	t.Parallel()

	source := []byte("const PRODUCT_VERSION: String = \"0.0.0-dev\"\n")
	_, err := runtimeFileContents("addons/gdtest/version.gd", source, "1.0.0\"\nprint(1)")
	require.ErrorContains(t, err, "invalid embedded")

	_, err = runtimeFileContents("addons/gdtest/version.gd", []byte("extends RefCounted\n"), "1.0.0")
	require.ErrorContains(t, err, "exactly once")
}
