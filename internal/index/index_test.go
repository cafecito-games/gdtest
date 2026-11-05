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

func TestDiscoverReusesParsedASTWhenAggregateConfigurationChanges(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)

	_, err = discoverWithStore(context.Background(), root, discovery.Options{
		Patterns: []string{"**/*.test.gd"},
	}, store)
	require.NoError(t, err)
	assert.Equal(t, 2, cacheEntryCount(t, store.Root()), "one AST and one aggregate manifest")

	_, err = discoverWithStore(context.Background(), root, discovery.Options{
		Patterns: []string{"example.test.gd"},
	}, store)
	require.NoError(t, err)
	assert.Equal(t, 3, cacheEntryCount(t, store.Root()), "configuration miss must reuse the AST entry")
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

func cacheEntryCount(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(name) == ".json" {
			count++
		}
		return nil
	})
	require.NoError(t, err)
	return count
}
