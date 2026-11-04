package dynamic

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrComputeCachesBySemanticIdentity(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	identity := testIdentity()
	computed := 0
	compute := func(context.Context) (model.DiscoveryManifest, error) {
		computed++
		return testManifest(), nil
	}
	first, hit, err := LoadOrCompute(context.Background(), store, identity, compute)
	require.NoError(t, err)
	assert.False(t, hit)
	second, hit, err := LoadOrCompute(context.Background(), store, identity, compute)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, computed)
}

func TestLoadOrComputeRefusesAndRecomputesCorruptPayload(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	identity := testIdentity()
	key, err := Key(identity)
	require.NoError(t, err)
	require.NoError(t, store.Put(context.Background(), key, []byte(`{"not":"a manifest"}`)))

	computed := 0
	manifest, hit, err := LoadOrCompute(context.Background(), store, identity, func(context.Context) (model.DiscoveryManifest, error) {
		computed++
		return testManifest(), nil
	})
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, testManifest(), manifest)
	assert.Equal(t, 1, computed)

	manifest, hit, err = LoadOrCompute(context.Background(), store, identity, nil)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, testManifest(), manifest)

	entry := filepath.Join(store.Root(), key[:2], key+".json")
	require.NoError(t, os.WriteFile(entry, []byte("truncated"), 0o600))
	manifest, hit, err = LoadOrCompute(context.Background(), store, identity, func(context.Context) (model.DiscoveryManifest, error) {
		return testManifest(), nil
	})
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, testManifest(), manifest)
}

func TestKeyIncludesEveryCompatibilityInput(t *testing.T) {
	t.Parallel()
	base := testIdentity()
	want, err := Key(base)
	require.NoError(t, err)
	variants := []Identity{base, base, base, base, base}
	variants[0].RuntimeVersion = "runtime-b"
	variants[1].GodotIdentity = "godot-b"
	variants[2].Configuration = []byte("config-b")
	variants[3].ContentFingerprint = "content-b"
	variants[4].InputFingerprint = "inputs-b"
	for _, variant := range variants {
		got, keyErr := Key(variant)
		require.NoError(t, keyErr)
		assert.NotEqual(t, want, got)
	}
	newGeneration := base
	newGeneration.Generation++
	reused, err := Key(newGeneration)
	require.NoError(t, err)
	assert.Equal(t, want, reused, "live generations reuse identical semantic content")
}

func TestLoadOrComputeRebindsCachedManifestGeneration(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	identity := testIdentity()
	_, _, err = LoadOrCompute(context.Background(), store, identity, func(context.Context) (model.DiscoveryManifest, error) {
		return testManifest(), nil
	})
	require.NoError(t, err)

	identity.Generation = 9
	manifest, hit, err := LoadOrCompute(context.Background(), store, identity, nil)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.EqualValues(t, 9, manifest.Generation)
}

func TestLoadOrComputeRejectsComputedStaleGeneration(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	identity := testIdentity()
	identity.Generation = 2
	_, _, err = LoadOrCompute(context.Background(), store, identity, func(context.Context) (model.DiscoveryManifest, error) {
		return testManifest(), nil
	})
	require.ErrorIs(t, err, ErrInvalidManifest)
}

func testIdentity() Identity {
	return Identity{
		ToolVersion: "tool-a", RuntimeVersion: "runtime-a", GodotIdentity: "godot-a",
		Configuration: []byte("config-a"), ContentFingerprint: "content-a", Generation: 1,
		InputFingerprint: "inputs-a",
	}
}

func testManifest() model.DiscoveryManifest {
	return model.DiscoveryManifest{
		ProtocolVersion: model.ProtocolVersion, Generation: 1, ContentFingerprint: "content-a",
		Files: []model.DiscoveryFile{{
			Path: "tests/example.test.gd", Name: "example", Line: 1,
			Cases: []model.DiscoveryCase{{
				ID: "tests/example.test.gd::test_value", Name: "value",
				File: "tests/example.test.gd",
			}},
		}},
	}
}
