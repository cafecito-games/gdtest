package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadProjectJSON(t *testing.T, body string) (Project, error) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(body), 0o600))
	return Load(root)
}

func TestLoadTestDoublesValidConfiguration(t *testing.T) {
	t.Parallel()

	loaded, err := loadProjectJSON(t, `{"test_doubles": [
		{"source": "src/inventory.gd", "output": "doubles/inventory_double.gd", "class_name": "InventoryDouble"},
		{"source": "src/ledger.gd", "output": "doubles/ledger_double.gd"}
	]}`)
	require.NoError(t, err)
	require.Len(t, loaded.TestDoubles, 2)
	assert.Equal(t, TestDouble{
		Source: "src/inventory.gd", Output: "doubles/inventory_double.gd", ClassName: "InventoryDouble",
	}, loaded.TestDoubles[0])
	assert.Equal(t, TestDouble{Source: "src/ledger.gd", Output: "doubles/ledger_double.gd"}, loaded.TestDoubles[1])
}

func TestLoadTestDoublesAbsentDefaultsToNil(t *testing.T) {
	t.Parallel()

	loaded, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, loaded.TestDoubles)

	loaded, err = loadProjectJSON(t, `{"godot": "custom"}`)
	require.NoError(t, err)
	assert.Nil(t, loaded.TestDoubles)
}

func TestLoadTestDoublesRejectsUnknownNestedField(t *testing.T) {
	t.Parallel()

	_, err := loadProjectJSON(t, `{"test_doubles": [{"source": "a.gd", "output": "b.gd", "mystery": true}]}`)
	require.ErrorContains(t, err, "unknown field")
}

func TestValidateTestDoublesRejectsInvalidEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doubles string
		want    string
	}{
		{
			name:    "empty source",
			doubles: `[{"source": "", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source: must not be empty",
		},
		{
			name:    "empty output",
			doubles: `[{"source": "src/a.gd", "output": ""}]`,
			want:    "test_doubles[0].output: must not be empty",
		},
		{
			name:    "backslash in source",
			doubles: `[{"source": "src\\a.gd", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source",
		},
		{
			name:    "absolute source",
			doubles: `[{"source": "/src/a.gd", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source: \"/src/a.gd\" must be project-relative",
		},
		{
			name:    "windows drive output",
			doubles: `[{"source": "src/a.gd", "output": "C:/doubles/a.gd"}]`,
			want:    "test_doubles[0].output: \"C:/doubles/a.gd\" must be project-relative",
		},
		{
			name:    "res scheme source",
			doubles: `[{"source": "res://src/a.gd", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source",
		},
		{
			name:    "unclean output",
			doubles: `[{"source": "src/a.gd", "output": "doubles//a.gd"}]`,
			want:    "test_doubles[0].output: \"doubles//a.gd\" must be a clean relative path",
		},
		{
			name:    "dot segment source",
			doubles: `[{"source": "./src/a.gd", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source: \"./src/a.gd\" must be a clean relative path",
		},
		{
			name:    "parent segment output",
			doubles: `[{"source": "src/a.gd", "output": "../doubles/a.gd"}]`,
			want:    "test_doubles[0].output: \"../doubles/a.gd\" must not contain a parent directory segment",
		},
		{
			name:    "source not gd",
			doubles: `[{"source": "src/a.txt", "output": "doubles/a.gd"}]`,
			want:    "test_doubles[0].source: \"src/a.txt\" must end in .gd",
		},
		{
			name:    "output not gd",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.tres"}]`,
			want:    "test_doubles[0].output: \"doubles/a.tres\" must end in .gd",
		},
		{
			name:    "output under runtime",
			doubles: `[{"source": "src/a.gd", "output": "addons/gdtest/a_double.gd"}]`,
			want:    "test_doubles[0].output: must not be under addons/gdtest/",
		},
		{
			name:    "output under runtime spelled in another case",
			doubles: `[{"source": "src/a.gd", "output": "Addons/GDTest/a_double.gd"}]`,
			want:    "test_doubles[0].output: must not be under addons/gdtest/",
		},
		{
			name:    "godot segment spelled in another case",
			doubles: `[{"source": "src/a.gd", "output": "doubles/.GODOT/a.gd"}]`,
			want:    `test_doubles[0].output: "doubles/.GODOT/a.gd" must not contain a .godot or .git segment`,
		},
		{
			name: "duplicate source",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"},
				{"source": "src/a.gd", "output": "doubles/b.gd"}]`,
			want: `test_doubles[1].source: "src/a.gd" duplicates test_doubles[0].source`,
		},
		{
			name: "duplicate output",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"},
				{"source": "src/b.gd", "output": "doubles/a.gd"}]`,
			want: `test_doubles[1].output: "doubles/a.gd" duplicates test_doubles[0].output`,
		},
		{
			name: "output differing only in case",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"},
				{"source": "src/b.gd", "output": "doubles/A.gd"}]`,
			want: `test_doubles[1].output: "doubles/A.gd" duplicates test_doubles[0].output`,
		},
		{
			name: "output differing from a source only in case",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"},
				{"source": "src/b.gd", "output": "SRC/A.gd"}]`,
			want: `test_doubles[1].output: "SRC/A.gd" is also test_doubles[0].source`,
		},
		{
			name: "output equals source",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"},
				{"source": "src/b.gd", "output": "src/a.gd"}]`,
			want: `test_doubles[1].output: "src/a.gd" is also test_doubles[0].source`,
		},
		{
			name:    "invalid class name",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd", "class_name": "1Double"}]`,
			want:    `test_doubles[0].class_name: "1Double" is not a valid GDScript identifier`,
		},
		{
			name:    "class name with space",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd", "class_name": "A Double"}]`,
			want:    `test_doubles[0].class_name: "A Double" is not a valid GDScript identifier`,
		},
		{
			name: "duplicate class name",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd", "class_name": "Double"},
				{"source": "src/b.gd", "output": "doubles/b.gd", "class_name": "Double"}]`,
			want: `test_doubles[1].class_name: "Double" duplicates test_doubles[0].class_name`,
		},
		{
			name:    "double quote in source",
			doubles: `[{"source": "src/a\"b.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source: "src/a\"b.gd" must not contain a double quote or control character`,
		},
		{
			name:    "double quote in output",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a\"b.gd"}]`,
			want:    `test_doubles[0].output: "doubles/a\"b.gd" must not contain a double quote or control character`,
		},
		{
			name:    "tab in source",
			doubles: `[{"source": "src/a\tb.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source: "src/a\tb.gd" must not contain a double quote or control character`,
		},
		{
			name:    "newline in output",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a\nb.gd"}]`,
			want:    `test_doubles[0].output: "doubles/a\nb.gd" must not contain a double quote or control character`,
		},
		{
			name:    "delete character in source",
			doubles: `[{"source": "src/a\u007fb.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source`,
		},
		{
			name:    "leading space in source",
			doubles: `[{"source": " src/a.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source: " src/a.gd" must not begin or end with whitespace`,
		},
		{
			name:    "godot segment in source",
			doubles: `[{"source": ".godot/a.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source: ".godot/a.gd" must not contain a .godot or .git segment`,
		},
		{
			name:    "git segment in source",
			doubles: `[{"source": "src/.git/a.gd", "output": "doubles/a.gd"}]`,
			want:    `test_doubles[0].source: "src/.git/a.gd" must not contain a .godot or .git segment`,
		},
		{
			name:    "godot segment in output",
			doubles: `[{"source": "src/a.gd", "output": ".godot/doubles/a.gd"}]`,
			want:    `test_doubles[0].output: ".godot/doubles/a.gd" must not contain a .godot or .git segment`,
		},
		{
			name:    "git segment in output",
			doubles: `[{"source": "src/a.gd", "output": ".git/a.gd"}]`,
			want:    `test_doubles[0].output: ".git/a.gd" must not contain a .godot or .git segment`,
		},
		{
			name:    "nested godot segment in output",
			doubles: `[{"source": "src/a.gd", "output": "a/.godot/b.gd"}]`,
			want:    `test_doubles[0].output: "a/.godot/b.gd" must not contain a .godot or .git segment`,
		},
		{
			name:    "nested git segment in source at a later entry",
			doubles: `[{"source": "src/a.gd", "output": "doubles/a.gd"}, {"source": "a/.git/b.gd", "output": "doubles/b.gd"}]`,
			want:    `test_doubles[1].source: "a/.git/b.gd" must not contain a .godot or .git segment`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectJSON(t, `{"test_doubles": `+test.doubles+`}`)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestValidateTestDoublesAcceptsEmptyClassNameAndValidPaths(t *testing.T) {
	t.Parallel()

	project := Defaults()
	project.TestDoubles = []TestDouble{
		{Source: "src/a.gd", Output: "doubles/a_double.gd"},
		{Source: "src/b.gd", Output: "doubles/b_double.gd", ClassName: "_B_Double2"},
		{Source: "src/item catalog.gd", Output: "doubles/item catalog_double.gd"},
		{Source: "src/.godot_notes/c.gd", Output: "doubles/godot.gd"},
	}
	require.NoError(t, project.Validate())
}
