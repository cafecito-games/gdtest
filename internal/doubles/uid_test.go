package doubles

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncodeUIDMatchesGodotVectors(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a", encodeUID(0))
	assert.Equal(t, "b", encodeUID(1))
	assert.Equal(t, "ba", encodeUID(34))
	assert.Equal(t, "d4n4ub6itg400", encodeUID(math.MaxInt64))
}

func TestUIDForDerivesIdentifierFromResourcePath(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "uid://duoo64ddq6q3a\n", UIDFor("res://doubles/inventory_double.gd"))
}

func TestUIDForIsStableAcrossCalls(t *testing.T) {
	t.Parallel()

	first := UIDFor("res://doubles/ledger_double.gd")
	assert.Equal(t, first, UIDFor("res://doubles/ledger_double.gd"))
	assert.NotEqual(t, first, UIDFor("res://doubles/calculator_double.gd"))
}
