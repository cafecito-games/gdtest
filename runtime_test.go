package gdtest

import (
	"os"
	"path/filepath"
	"testing"

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
