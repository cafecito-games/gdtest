package astcache

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserCachesAndRoundTripsACompleteSyntaxTree(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	parser, err := New(store, "tool-a", "parser-a")
	require.NoError(t, err)
	parsed := 0
	parser.parse = func(name string, source []byte) (*ast.File, error) {
		parsed++
		return gdparser.ParseFile(name, source)
	}
	source := []byte(`extends GDTest

func test_value(input: int = 1) -> void:
	var values: Array[int] = [input, 2]
	var labels: Dictionary[String, int] = {"input": input}
	if values.size() > 1:
		expect_equal(values[0], labels["input"])
`)

	first, err := parser.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	second, err := parser.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, parsed)
}

func TestParserRejectsCorruptApplicationPayloadAndRecomputes(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	parser, err := New(store, "tool-a", "parser-a")
	require.NoError(t, err)
	source := []byte("extends GDTest\n")
	key := cache.Key("tool-a", "parser-a", []byte(schemaVersion+"\x00tests/value.test.gd"), source)
	require.NoError(t, store.Put(context.Background(), key, []byte("not a gob artifact")))
	parsed := 0
	parser.parse = func(name string, data []byte) (*ast.File, error) {
		parsed++
		return gdparser.ParseFile(name, data)
	}

	tree, err := parser.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	assert.Equal(t, "tests/value.test.gd", tree.Name)
	assert.Equal(t, 1, parsed)
	_, err = parser.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	assert.Equal(t, 1, parsed)
}

func TestParserIdentityIsPathRelativeAndVersioned(t *testing.T) {
	t.Parallel()
	store, err := cache.Open(t.TempDir(), 1<<20)
	require.NoError(t, err)
	first, err := New(store, "tool-a", "parser-a")
	require.NoError(t, err)
	second, err := New(store, "tool-a", "parser-b")
	require.NoError(t, err)
	firstCount, secondCount := 0, 0
	first.parse = func(name string, source []byte) (*ast.File, error) {
		firstCount++
		return gdparser.ParseFile(name, source)
	}
	second.parse = func(name string, source []byte) (*ast.File, error) {
		secondCount++
		return gdparser.ParseFile(name, source)
	}
	source := []byte("extends GDTest\n")

	_, err = first.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	_, err = first.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	_, err = second.Parse(context.Background(), "tests/value.test.gd", source)
	require.NoError(t, err)
	assert.Equal(t, 1, firstCount)
	assert.Equal(t, 1, secondCount)

	_, err = first.Parse(context.Background(), filepath.Join(string(filepath.Separator), "tmp", "value.gd"), source)
	require.Error(t, err)
}
