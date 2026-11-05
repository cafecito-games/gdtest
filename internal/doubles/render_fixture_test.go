package doubles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

// fixtureDoubleCases pair each hand-written double in fixtures/godot/api with its
// source. The resources match the fixtures' res:// paths, so the project root
// that is indexed mirrors the repository layout.
var fixtureDoubleCases = []struct {
	name      string
	className string
}{
	{name: "inventory", className: "InventoryDouble"},
	{name: "calculator", className: "CalculatorDouble"},
	{name: "ledger", className: "LedgerDouble"},
}

// TestRenderMatchesHandWrittenFixtureDoubles renders each fixture double from its
// source and asserts that the result equals the fixture, once the two generated
// header lines are stripped. The fixtures carry no ownership marker.
func TestRenderMatchesHandWrittenFixtureDoubles(t *testing.T) {
	t.Parallel()

	for _, fixture := range fixtureDoubleCases {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := "fixtures/godot/api/" + fixture.name + ".gd"
			outputPath := "fixtures/godot/api/" + fixture.name + "_double.gd"
			files := map[string]string{sourcePath: readRepositoryFile(t, sourcePath)}
			model, diagnostics := analyzeEntry(t, files, config.TestDouble{
				Source:    sourcePath,
				Output:    outputPath,
				ClassName: fixture.className,
			})
			require.Empty(t, diagnostics)
			rendered, err := Render(model, "0.0.0-dev")
			require.NoError(t, err)

			body := strings.SplitN(string(rendered), "\n", 3)
			require.Len(t, body, 3)
			if *updateGoldens {
				require.NoError(t, os.WriteFile(filepath.Join("..", "..", filepath.FromSlash(outputPath)), []byte(body[2]), 0o600))
			}
			assert.Equal(t, readRepositoryFile(t, outputPath), body[2])
		})
	}
}
