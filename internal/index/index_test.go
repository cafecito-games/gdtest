package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverUsesPathNeutralContentIdentity(t *testing.T) {
	t.Parallel()
	one := fixture(t)
	two := fixture(t)
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	options := discovery.Options{Patterns: []string{"**/*.test.gd"}, Exclude: []string{".godot/**"}}
	first, err := discoverWithStore(context.Background(), one, options, store)
	require.NoError(t, err)
	second, err := discoverWithStore(context.Background(), two, options, store)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	require.Len(t, first.Files, 1)
	require.Len(t, first.Files[0].Cases, 1)
	assert.Equal(t, "example.test.gd::test_value", first.Files[0].Cases[0].ID)
}

func TestDecodeManifestRejectsMalformedApplicationPayload(t *testing.T) {
	t.Parallel()
	for _, payload := range [][]byte{
		[]byte(`{"files":[]}`),
		[]byte(`{"files":[],"structural_fingerprint":"s","content_fingerprint":"c"} trailing`),
		[]byte(`{"files":[],"structural_fingerprint":"s","content_fingerprint":"c","unknown":true}`),
	} {
		_, err := decodeManifest(payload)
		require.Error(t, err, string(payload))
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		filename := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
		require.NoError(t, os.WriteFile(filename, []byte(value), 0o600))
	}
	write("project.godot", "[application]\n")
	write("example.test.gd", "extends GDTest\nfunc test_value() -> void:\n\tpass\n")
	return root
}
