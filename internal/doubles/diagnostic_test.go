package doubles

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiagnosticStringFormatsLocation(t *testing.T) {
	t.Parallel()

	diagnostic := Diagnostic{Path: "src/a.gd", Line: 3, Column: 7, Message: "untyped"}
	assert.Equal(t, "src/a.gd:3:7: untyped", diagnostic.String())
}

func TestDiagnosticStringOmitsMissingLocation(t *testing.T) {
	t.Parallel()

	diagnostic := Diagnostic{Path: "doubles/a.gd", Message: "refusing to overwrite"}
	assert.Equal(t, "doubles/a.gd: refusing to overwrite", diagnostic.String())
}

func TestSortDiagnosticsIsDeterministic(t *testing.T) {
	t.Parallel()

	want := []Diagnostic{
		{Path: "a.gd", Line: 1, Column: 1, Message: "first"},
		{Path: "a.gd", Line: 1, Column: 1, Message: "second"},
		{Path: "a.gd", Line: 2, Column: 1, Message: "later line"},
		{Path: "a.gd", Line: 2, Column: 4, Message: "later column"},
		{Path: "b.gd", Line: 0, Column: 0, Message: "other file"},
	}
	shuffled := []Diagnostic{want[4], want[2], want[0], want[3], want[1]}
	SortDiagnostics(shuffled)
	assert.Equal(t, want, shuffled)
}
