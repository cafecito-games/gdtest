package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamespaceIncludesProtocolAndBuild(t *testing.T) {
	t.Parallel()

	namespace, err := NewNamespace(t.TempDir(), 3, "build-a")
	require.NoError(t, err)
	want := filepath.Join("gdtest", "daemons", "3", "build-a")
	assert.Equal(t, "build-a", filepath.Base(namespace.Root))
	assert.True(t, containsPath(namespace.Root, want), "namespace root = %q, want suffix %q", namespace.Root, want)
	require.NoError(t, namespace.Prepare())
	info, err := os.Stat(namespace.Root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestNamespaceRejectsUnsafeBuildID(t *testing.T) {
	t.Parallel()

	_, err := NewNamespace(t.TempDir(), 1, "../other")
	require.Error(t, err)
}

func TestLongNamespaceUsesBoundedPrivateSocketDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows local transport is deferred")
	}
	cacheRoot := filepath.Join(t.TempDir(), strings.Repeat("long-cache-segment-", 10))
	namespace, err := NewNamespace(cacheRoot, 1, "long-path-build")
	require.NoError(t, err)
	assert.LessOrEqual(t, len([]byte(namespace.SocketPath)), safeUnixSocketPathBytes)
	assert.NotEqual(t, filepath.Clean(namespace.Root), filepath.Clean(filepath.Dir(namespace.SocketPath)))

	require.NoError(t, namespace.Prepare())
	rootInfo, err := os.Stat(namespace.Root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), rootInfo.Mode().Perm())
	socketInfo, err := os.Stat(filepath.Dir(namespace.SocketPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), socketInfo.Mode().Perm())
}

func TestMetadataRoundTripAndMismatch(t *testing.T) {
	t.Parallel()

	namespace, err := NewNamespace(t.TempDir(), 1, "build-a")
	require.NoError(t, err)
	require.NoError(t, namespace.Prepare())
	want := Metadata{Protocol: 1, BuildID: "build-a", PID: 42, SocketPath: namespace.SocketPath, StartedAt: time.Now().UTC()}
	require.NoError(t, namespace.writeMetadata(want))
	got, err := namespace.ReadMetadata()
	require.NoError(t, err)
	assert.Equal(t, want.PID, got.PID)
	assert.Equal(t, want.BuildID, got.BuildID)
	other := namespace
	other.BuildID = "build-b"
	_, err = other.ReadMetadata()
	require.Error(t, err)
}

func TestLockIsExclusiveAndReusable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.lock")
	first, err := AcquireLock(path)
	require.NoError(t, err)
	_, err = AcquireLock(path)
	require.ErrorIs(t, err, ErrAlreadyRunning)
	require.NoError(t, first.Close())
	second, err := AcquireLock(path)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func containsPath(value, suffix string) bool {
	valueParts := filepath.Clean(value)
	suffixParts := filepath.Clean(suffix)
	return len(valueParts) >= len(suffixParts) && valueParts[len(valueParts)-len(suffixParts):] == suffixParts
}
