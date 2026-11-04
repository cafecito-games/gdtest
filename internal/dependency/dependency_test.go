package dependency

import (
	"testing"

	"github.com/cafecito-games/gdparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeLiteralAndDynamicLoads(t *testing.T) {
	t.Parallel()

	tree, err := gdparser.ParseString(`
var direct = preload("res://src/player.gd")
var duplicate = load('res://src/player.gd')
var icon = ResourceLoader.load(r"res://art/icon.svg")
var uncertain = load(resource_path)
`)
	require.NoError(t, err)
	got := Analyze(tree)
	want := []string{"art/icon.svg", "src/player.gd"}
	assert.Equal(t, want, got.Paths)
	assert.True(t, got.Dynamic)
}

func TestGraphTransitiveDependenciesAndCycles(t *testing.T) {
	t.Parallel()

	graph := NewGraph()
	graph.Set("tests/player.test.gd", Analysis{Paths: []string{"src/player.gd"}})
	graph.Set("src/player.gd", Analysis{Paths: []string{"src/stats.gd"}})
	graph.Set("src/stats.gd", Analysis{Paths: []string{"src/player.gd"}})
	depends, dynamic := graph.DependsOn("tests/player.test.gd", "src/stats.gd")
	assert.True(t, depends)
	assert.False(t, dynamic)
	depends, _ = graph.DependsOn("tests/player.test.gd", "src/missing.gd")
	assert.False(t, depends)
	depends, _ = graph.DependsOn("tests/player.test.gd", "tests/player.test.gd")
	assert.True(t, depends)
}

func TestGraphPropagatesUnresolvedDynamicLoad(t *testing.T) {
	t.Parallel()

	graph := NewGraph()
	graph.Set("test.gd", Analysis{Paths: []string{"helper.gd"}})
	graph.Set("helper.gd", Analysis{Dynamic: true})
	depends, dynamic := graph.DependsOn("test.gd", "anything.gd")
	assert.False(t, depends)
	assert.True(t, dynamic)
}
