package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildIDIsStableWithinProcess(t *testing.T) {
	t.Parallel()

	first := BuildID()
	assert.NotEmpty(t, first)
	assert.Equal(t, first, BuildID())
}
