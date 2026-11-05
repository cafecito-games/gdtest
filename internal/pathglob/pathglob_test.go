package pathglob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDoubleStarMatchesNestedAndRootFiles(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"root.test.gd", "one/root.test.gd", "one/two/root.test.gd"} {
		assert.True(t, Match("**/*.test.gd", filename), filename)
	}
}

func TestMatchesAnyUsesEveryPattern(t *testing.T) {
	t.Parallel()

	patterns := []string{".godot/**", "addons/*.gd"}
	assert.True(t, MatchesAny(".godot/cache.gd", patterns))
	assert.True(t, MatchesAny("addons/plugin.gd", patterns))
	assert.False(t, MatchesAny("src/plugin.gd", patterns))
	assert.False(t, MatchesAny("src/plugin.gd", nil))
}

func TestMatchSingleSegmentWildcardStopsAtSlash(t *testing.T) {
	t.Parallel()

	assert.False(t, Match("addons/*.gd", "addons/nested/plugin.gd"))
	assert.True(t, Match("addons/?.gd", "addons/a.gd"))
}
