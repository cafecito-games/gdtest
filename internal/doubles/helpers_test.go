package doubles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

// writeProject creates a project under a fresh temporary root. Keys are
// project-relative slash paths.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return root
}

// buildProject indexes a fresh project and fails the test when the index itself
// reports diagnostics.
func buildProject(t *testing.T, files map[string]string) *Index {
	t.Helper()
	index, diagnostics := BuildIndex(writeProject(t, files), nil)
	require.Empty(t, diagnostics)
	require.NotNil(t, index)
	return index
}

// analyzeEntry analyzes an entry against a fresh project and fails the test on
// index diagnostics, so each test controls only the source it asserts about.
func analyzeEntry(t *testing.T, files map[string]string, entry config.TestDouble) (Model, []Diagnostic) {
	t.Helper()
	return Analyze(buildProject(t, files), entry)
}

// entryFor returns the usual entry for a source in src/ with an output in doubles/.
func entryFor(source string) config.TestDouble {
	return config.TestDouble{
		Source:    "src/" + source,
		Output:    "doubles/" + source[:len(source)-len(".gd")] + "_double.gd",
		ClassName: "",
	}
}

// messagesOf returns the messages of diagnostics, for compact assertions.
func messagesOf(diagnostics []Diagnostic) []string {
	messages := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	return messages
}

// readRepositoryFile reads a repository-relative slash path from the module root.
func readRepositoryFile(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(relative)))
	require.NoError(t, err)
	return string(data)
}
