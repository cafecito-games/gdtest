package doubles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIndexSkipsGodotAndGitDirectories(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{
		"src/a.gd":           "class_name A\n",
		".godot/cache.gd":    "this is not GDScript (\n",
		".git/hooks/x.gd":    "this is not GDScript (\n",
		"nested/.godot/b.gd": "this is not GDScript (\n",
	})
	index, diagnostics := BuildIndex(root, nil)
	require.Empty(t, diagnostics)
	require.Contains(t, index.scripts, "src/a.gd")
	assert.Len(t, index.scripts, 1)
}

func TestBuildIndexReportsParseFailuresWithPosition(t *testing.T) {
	t.Parallel()

	_, diagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/broken.gd": "extends RefCounted\n\nfunc (:\n",
	}), nil)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/broken.gd", diagnostics[0].Path)
	assert.Equal(t, 3, diagnostics[0].Line)
	assert.NotEmpty(t, diagnostics[0].Message)
}

func TestBuildIndexRecordsExactOwnershipMarkerOnly(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/a.gd\n"
	index := buildProject(t, map[string]string{
		"doubles/owned.gd":      marker + "class_name Owned\n",
		"doubles/spaced.gd":     "# gdtest:generated-double  v1 source=res://src/a.gd\n",
		"doubles/second.gd":     "\n" + marker,
		"doubles/no_source.gd":  "# gdtest:generated-double v1 source=src/a.gd\n",
		"doubles/extra_text.gd": "# gdtest:generated-double v1 source=res://src/a.gd trailing\n",
	})
	assert.Equal(t, []GeneratedScript{{Path: "doubles/owned.gd", Source: "src/a.gd"}}, index.GeneratedScripts())
}

func TestBuildIndexDiagnosesDuplicateClassName(t *testing.T) {
	t.Parallel()

	_, diagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/a.gd": "class_name Shared\n",
		"src/b.gd": "class_name Shared\n",
	}), nil)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/b.gd", diagnostics[0].Path)
	assert.Contains(t, diagnostics[0].Message, "class_name Shared is also declared by src/a.gd")
}

func TestBuildIndexRejectsRootThatDoesNotExist(t *testing.T) {
	t.Parallel()

	index, diagnostics := BuildIndex(filepath.Join(t.TempDir(), "missing"), nil)
	assert.Nil(t, index)
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, "resolve project root")
}

func TestBuildIndexSymlinkedScriptOutsideRootIsDiagnosed(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "external.gd"), []byte("class_name External\n"), 0o600))
	root := writeProject(t, map[string]string{})
	require.NoError(t, os.Symlink(filepath.Join(outside, "external.gd"), filepath.Join(root, "linked.gd")))

	index, diagnostics := BuildIndex(root, nil)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "linked.gd", diagnostics[0].Path)
	assert.Contains(t, diagnostics[0].Message, "symlink resolves outside the project root")
	assert.NotContains(t, index.classes, "External")
}

func TestBuildIndexFindsOwnershipInExcludedScriptsWithoutParsingThem(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/a.gd\n"
	root := writeProject(t, map[string]string{
		"vendor/owned.gd":       marker + "func (:\n",
		"vendor/plain.gd":       "func (:\n",
		"vendor/marker_late.gd": "\n" + marker,
		"src/a.gd":              "class_name A\n",
	})
	index, diagnostics := BuildIndex(root, []string{"vendor/**"})
	require.Empty(t, diagnostics)
	assert.NotContains(t, index.scripts, "vendor/owned.gd")
	assert.Equal(t, []GeneratedScript{{Path: "vendor/owned.gd", Source: "src/a.gd"}}, index.GeneratedScripts())
}

func TestBuildIndexExcludedScriptsDoNotResolveClasses(t *testing.T) {
	t.Parallel()

	index, diagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/a.gd":         "class_name Shared\n",
		"vendor/dup.gd":    "class_name Shared\n",
		"vendor/base.gd":   "extends \"res://nowhere.gd\"\n",
		"vendor/deep/x.gd": "func (:\n",
	}), []string{"vendor/**"})
	require.Empty(t, diagnostics)
	assert.Equal(t, "src/a.gd", index.classes["Shared"].path)
	assert.Empty(t, index.GeneratedScripts())
}

func TestBuildIndexIgnoresUnsafeExcludedOwnershipWithoutDiagnostics(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	marker := "# gdtest:generated-double v1 source=res://src/a.gd\n"
	require.NoError(t, os.WriteFile(filepath.Join(outside, "external.gd"), []byte(marker), 0o600))
	root := writeProject(t, map[string]string{"vendor/keep.txt": "not script"})
	require.NoError(t, os.Symlink(filepath.Join(outside, "external.gd"), filepath.Join(root, "vendor", "linked.gd")))
	require.NoError(t, os.Symlink(filepath.Join(root, "missing.gd"), filepath.Join(root, "vendor", "dangling.gd")))

	index, diagnostics := BuildIndex(root, []string{"vendor/**"})
	assert.Empty(t, diagnostics)
	assert.Empty(t, index.GeneratedScripts())
}

func TestDirectoryExcludedMatchesOnlyPlaceholderAncestors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		relative string
		exclude  []string
		want     bool
	}{
		{name: "file under double-star directory", relative: "vendor/a/b.gd", exclude: []string{"vendor/**"}, want: true},
		{name: "file matched directly", relative: "src/broken.gd", exclude: []string{"**/broken.gd"}, want: true},
		{name: "exact directory name does not prune", relative: "vendor/a.gd", exclude: []string{"vendor"}, want: false},
		{name: "root file is never excluded by directory", relative: "a.gd", exclude: []string{"vendor/**"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, isExcluded(test.relative, test.exclude))
		})
	}
}
