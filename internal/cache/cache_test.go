package cache

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cacheProcessHelperEnvironment = "GO_WANT_GDTEST_CACHE_HELPER"

func TestKeyIncludesEverySemanticInput(t *testing.T) {
	t.Parallel()
	base := Key("gdtest-1", "parser-1", []byte("config"), []byte("content"))
	keys := []string{
		Key("gdtest-2", "parser-1", []byte("config"), []byte("content")),
		Key("gdtest-1", "parser-2", []byte("config"), []byte("content")),
		Key("gdtest-1", "parser-1", []byte("other"), []byte("content")),
		Key("gdtest-1", "parser-1", []byte("config"), []byte("other")),
	}
	for _, key := range keys {
		assert.NotEqual(t, base, key)
	}
}

func TestGetOrComputeCoalescesConcurrentCallers(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir(), 1024)
	require.NoError(t, err)
	key := Key("tool", "parser", []byte("config"), []byte("source"))
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	compute := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("artifact"), nil
	}

	const goroutines = 32
	results := make(chan []byte, goroutines)
	errorsFound := make(chan error, goroutines)
	var group sync.WaitGroup
	for range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			result, callErr := store.GetOrCompute(context.Background(), key, compute)
			if callErr != nil {
				errorsFound <- callErr
				return
			}
			results <- result
		}()
	}
	<-started
	close(release)
	group.Wait()
	close(results)
	close(errorsFound)
	for callErr := range errorsFound {
		require.NoError(t, callErr)
	}
	for result := range results {
		assert.Equal(t, "artifact", string(result))
	}
	assert.Equal(t, int64(1), calls.Load())
}

func TestCorruptionIsRejectedAndRecomputed(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir(), 1024)
	require.NoError(t, err)
	key := Key("tool", "parser", nil, []byte("source"))
	require.NoError(t, os.MkdirAll(filepath.Dir(store.entryPath(key)), 0o700))
	require.NoError(t, os.WriteFile(store.entryPath(key), []byte(`{"format":1,"key":"wrong","checksum":"wrong","payload":"eA=="}`), 0o600))
	data, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, data)
	_, err = os.Stat(store.entryPath(key))
	require.ErrorIs(t, err, os.ErrNotExist)
	var calls int
	data, err = store.GetOrCompute(context.Background(), key, func(context.Context) ([]byte, error) {
		calls++
		return []byte("recomputed"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "recomputed", string(data))
	assert.Equal(t, 1, calls)
	data, hit, err = store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "recomputed", string(data))
}

func TestConcurrentPublicationNeverExposesPartialEntry(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir(), 4096)
	require.NoError(t, err)
	key := Key("tool", "parser", nil, []byte("source"))
	payloads := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	var group sync.WaitGroup
	for index := range 90 {
		group.Add(1)
		go func() {
			defer group.Done()
			assert.NoError(t, store.Put(context.Background(), key, payloads[index%len(payloads)]))
		}()
	}
	group.Wait()
	data, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Contains(t, []string{"one", "two", "three"}, string(data))
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(store.entryPath(key)), ".publish-*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestCacheDirectoryDisappearanceIsSafe(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "cache"), 1024)
	require.NoError(t, err)
	key := Key("tool", "parser", nil, []byte("source"))
	require.NoError(t, store.Put(context.Background(), key, []byte("first")))
	require.NoError(t, os.RemoveAll(store.Root()))
	_, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	require.NoError(t, store.Put(context.Background(), key, []byte("second")))
	data, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "second", string(data))
}

func TestLimitsAndKeys(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir(), 3)
	require.NoError(t, err)
	require.ErrorIs(t, store.Put(context.Background(), "bad", []byte("x")), ErrInvalidKey)
	key := Key("tool", "parser", nil, nil)
	require.ErrorIs(t, store.Put(context.Background(), key, []byte("four")), ErrEntryTooLarge)
}

func TestDeleteRemovesApplicationRejectedEntry(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir(), 1024)
	require.NoError(t, err)
	key := Key("tool", "schema", nil, []byte("source"))
	require.NoError(t, store.Put(context.Background(), key, []byte("valid envelope, invalid schema")))
	require.NoError(t, store.Delete(context.Background(), key))
	_, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, hit)
	require.NoError(t, store.Delete(context.Background(), key), "deleting a miss is idempotent")
	require.ErrorIs(t, store.Delete(context.Background(), "invalid"), ErrInvalidKey)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, store.Delete(canceled, key), context.Canceled)
}

func TestStorePublishesAtomicallyAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	key := Key("tool", "parser", []byte("config"), []byte("content"))
	commands := make([]*exec.Cmd, 12)
	for index := range commands {
		command := exec.Command(os.Args[0], "-test.run", "^TestCacheProcessHelper$")
		command.Env = append(os.Environ(),
			cacheProcessHelperEnvironment+"=1",
			"GDTEST_CACHE_HELPER_ROOT="+root,
			"GDTEST_CACHE_HELPER_KEY="+key,
		)
		require.NoError(t, command.Start())
		commands[index] = command
	}
	for _, command := range commands {
		require.NoError(t, command.Wait())
	}

	store, err := Open(root, 1024)
	require.NoError(t, err)
	data, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, []byte("shared-payload"), data)
	matches, err := filepath.Glob(filepath.Join(root, key[:2], ".publish-*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestCacheProcessHelper(t *testing.T) {
	if os.Getenv(cacheProcessHelperEnvironment) != "1" {
		t.Skip("cache subprocess helper")
	}
	store, err := Open(os.Getenv("GDTEST_CACHE_HELPER_ROOT"), 1024)
	require.NoError(t, err)
	require.NoError(t, store.Put(
		context.Background(),
		os.Getenv("GDTEST_CACHE_HELPER_KEY"),
		[]byte("shared-payload"),
	))
}
