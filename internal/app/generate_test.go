package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const inventorySource = "## Holds items.\nclass_name Inventory\nextends RefCounted\n\nfunc count() -> int:\n\treturn 0\n"

const inventoryConfiguration = `{"test_doubles":[{"source":"src/inventory.gd","output":"doubles/inventory_double.gd","class_name":"InventoryDouble"}]}`

// projectState records the content and modification time of every file under a
// project, so that a test can prove that a command changed nothing.
type projectState struct {
	content  string
	modified time.Time
}

func writeGenerateProject(t *testing.T, configuration string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "project.godot", "[application]\n")
	if configuration != "" {
		writeTestFile(t, root, "gdtest.json", configuration)
	}
	for relative, content := range files {
		writeTestFile(t, root, relative, content)
	}
	return root
}

func writeTestFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func snapshotProject(t *testing.T, root string) map[string]projectState {
	t.Helper()
	states := make(map[string]projectState)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		states[filepath.ToSlash(relative)] = projectState{content: string(content), modified: info.ModTime()}
		return nil
	})
	require.NoError(t, err)
	return states
}

func runGenerate(t *testing.T, arguments ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), append([]string{"generate", "doubles"}, arguments...), IO{Out: &stdout, Err: &stderr})
	return exitCode, stdout.String(), stderr.String()
}

func TestGenerateDoublesFailClosedWritesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configuration string
		files         map[string]string
		setup         func(t *testing.T, root string)
		diagnostic    string
	}{
		{
			name:          "unknown configuration field",
			diagnostic:    `unknown field "surprise"`,
			configuration: `{"test_doubles":[{"source":"src/inventory.gd","output":"doubles/inventory_double.gd","surprise":1}]}`,
			files:         map[string]string{"src/inventory.gd": inventorySource},
		},
		{
			name:          "duplicate output",
			diagnostic:    "duplicates test_doubles[0].output",
			configuration: `{"test_doubles":[{"source":"src/inventory.gd","output":"doubles/a.gd"},{"source":"src/other.gd","output":"doubles/a.gd"}]}`,
			files:         map[string]string{"src/inventory.gd": inventorySource, "src/other.gd": inventorySource},
		},
		{
			name:          "duplicate class name",
			diagnostic:    "duplicates test_doubles[0].class_name",
			configuration: `{"test_doubles":[{"source":"src/inventory.gd","output":"doubles/a.gd","class_name":"Same"},{"source":"src/other.gd","output":"doubles/b.gd","class_name":"Same"}]}`,
			files:         map[string]string{"src/inventory.gd": inventorySource, "src/other.gd": inventorySource},
		},
		{
			name:          "escaping output path",
			diagnostic:    "must not contain a parent directory segment",
			configuration: `{"test_doubles":[{"source":"src/inventory.gd","output":"../escape.gd"}]}`,
			files:         map[string]string{"src/inventory.gd": inventorySource},
		},
		{
			name:          "output under the runtime",
			diagnostic:    "must not be under addons/gdtest/",
			configuration: `{"test_doubles":[{"source":"src/inventory.gd","output":"addons/gdtest/double.gd"}]}`,
			files:         map[string]string{"src/inventory.gd": inventorySource},
		},
		{
			name:          "source symbolic link outside the project",
			diagnostic:    "source resolves outside the project root",
			configuration: inventoryConfiguration,
			setup: func(t *testing.T, root string) {
				outside := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(outside, "inventory.gd"), []byte(inventorySource), 0o600))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
				require.NoError(t, os.Symlink(filepath.Join(outside, "inventory.gd"), filepath.Join(root, "src", "inventory.gd")))
			},
		},
		{
			name:          "output is a symbolic link",
			diagnostic:    "refusing to write through a symbolic link",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": inventorySource, "doubles/real.gd": "extends RefCounted\n"},
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "doubles"), 0o755))
				require.NoError(t, os.Symlink(filepath.Join(root, "doubles", "real.gd"), filepath.Join(root, "doubles", "inventory_double.gd")))
			},
		},
		{
			name:          "missing source",
			diagnostic:    "source script does not exist in the project",
			configuration: inventoryConfiguration,
		},
		{
			name:          "unparseable source",
			diagnostic:    "source script failed to parse",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "func (:\n"},
		},
		{
			name:          "unparseable ancestor",
			diagnostic:    "expected function name",
			configuration: inventoryConfiguration,
			files: map[string]string{
				"src/inventory.gd": "extends \"res://src/base.gd\"\n",
				"src/base.gd":      "func (:\n",
			},
		},
		{
			name:          "non-literal base",
			diagnostic:    "base class must be a string path or a class_name identifier",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "extends Foo.Bar\n"},
		},
		{
			name:          "inheritance cycle",
			diagnostic:    "inheritance cycle through",
			configuration: inventoryConfiguration,
			files: map[string]string{
				"src/inventory.gd": "extends \"res://src/base.gd\"\n",
				"src/base.gd":      "extends \"res://src/inventory.gd\"\n",
			},
		},
		{
			name:          "untyped parameter",
			diagnostic:    "parameter item is untyped",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "extends RefCounted\n\nfunc add(item) -> void:\n\tpass\n"},
		},
		{
			name:          "missing return type",
			diagnostic:    "method count has no return type",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "extends RefCounted\n\nfunc count():\n\treturn 0\n"},
		},
		{
			name:          "unsupported return type",
			diagnostic:    "return type Foo.Bar of method count is not supported",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "extends RefCounted\n\nfunc count() -> Foo.Bar:\n\treturn null\n"},
		},
		{
			name:          "reserved method name",
			diagnostic:    "method get collides with an Object or RefCounted method",
			configuration: inventoryConfiguration,
			files:         map[string]string{"src/inventory.gd": "extends RefCounted\n\nfunc get() -> int:\n\treturn 0\n"},
		},
		{
			name:          "pascal name collision",
			diagnostic:    "generate controller AddItemMethod",
			configuration: inventoryConfiguration,
			files: map[string]string{"src/inventory.gd": "extends RefCounted\n\nfunc add_item() -> void:\n\tpass\n\n" +
				"func addItem() -> void:\n\tpass\n"},
		},
		{
			name:          "unmarked output",
			diagnostic:    "missing gdtest generated-double marker",
			configuration: inventoryConfiguration,
			files: map[string]string{
				"src/inventory.gd":            inventorySource,
				"doubles/inventory_double.gd": "extends RefCounted\n",
			},
		},
		{
			name:          "sidecar without its script",
			diagnostic:    "refusing to overwrite doubles/inventory_double.gd.uid",
			configuration: inventoryConfiguration,
			files: map[string]string{
				"src/inventory.gd":                inventorySource,
				"doubles/inventory_double.gd.uid": "uid://abc\n",
			},
		},
		{
			name:       "one entry fails",
			diagnostic: "source script does not exist in the project",
			configuration: `{"test_doubles":[` +
				`{"source":"src/ledger.gd","output":"doubles/ledger_double.gd"},` +
				`{"source":"src/missing.gd","output":"doubles/missing_double.gd"}]}`,
			files: map[string]string{"src/ledger.gd": strings.Replace(inventorySource, "Inventory", "Ledger", 1)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := writeGenerateProject(t, test.configuration, test.files)
			if test.setup != nil {
				test.setup(t, root)
			}
			before := snapshotProject(t, root)

			exitCode, stdout, stderr := runGenerate(t, "--project", root)

			require.Equal(t, ExitError, exitCode, "stdout=%q stderr=%q", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, test.diagnostic)
			assert.Equal(t, before, snapshotProject(t, root))
		})
	}
}

func TestGenerateDoublesCheckWithInvalidInputsExitsTwoBeforeStaleness(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, `{"test_doubles":[`+
		`{"source":"src/inventory.gd","output":"doubles/inventory_double.gd"},`+
		`{"source":"src/missing.gd","output":"doubles/missing_double.gd"}]}`, map[string]string{
		"src/inventory.gd":            inventorySource,
		"doubles/inventory_double.gd": "# gdtest:generated-double v1 source=res://src/inventory.gd\nstale\n",
	})
	before := snapshotProject(t, root)

	exitCode, stdout, stderr := runGenerate(t, "--check", "--project", root)

	require.Equal(t, ExitError, exitCode, "stderr=%q", stderr)
	assert.Empty(t, stdout)
	assert.NotContains(t, stdout, "stale:")
	assert.Equal(t, before, snapshotProject(t, root))
}

func TestGenerateDoublesWriteThenWriteIsNoOpAndCheckPasses(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, inventoryConfiguration, map[string]string{"src/inventory.gd": inventorySource})

	exitCode, stdout, stderr := runGenerate(t, "--project", root)
	require.Equal(t, ExitOK, exitCode, "stderr=%q", stderr)
	assert.Equal(t, "2 file(s) written; 0 file(s) removed\n", stdout)
	written := snapshotProject(t, root)

	exitCode, stdout, stderr = runGenerate(t, "--project", root)
	require.Equal(t, ExitOK, exitCode, "stderr=%q", stderr)
	assert.Equal(t, "0 file(s) written; 0 file(s) removed\n", stdout)
	assert.Equal(t, written, snapshotProject(t, root))

	exitCode, stdout, stderr = runGenerate(t, "--check", "--project", root)
	require.Equal(t, ExitOK, exitCode, "stderr=%q", stderr)
	assert.Equal(t, "0 double(s) out of date\n", stdout)
	assert.Empty(t, stderr)
}

func TestGenerateDoublesCheckReportsCorruptedByteAsStale(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, inventoryConfiguration, map[string]string{"src/inventory.gd": inventorySource})
	require.Equal(t, ExitOK, mustGenerate(t, root))
	output := filepath.Join(root, "doubles", "inventory_double.gd")
	content, err := os.ReadFile(output)
	require.NoError(t, err)
	header := bytes.Index(content, []byte("Generated by"))
	require.NotEqual(t, -1, header)
	content[header] ^= 0x20
	require.NoError(t, os.WriteFile(output, content, 0o644))
	before := snapshotProject(t, root)

	exitCode, stdout, stderr := runGenerate(t, "--check", "--project", root)

	require.Equal(t, ExitFailures, exitCode, "stderr=%q", stderr)
	assert.Equal(t, "stale: doubles/inventory_double.gd\n", stdout)
	assert.Equal(t, before, snapshotProject(t, root))
}

func TestGenerateDoublesCheckReportsDeletedOutputAsMissing(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, inventoryConfiguration, map[string]string{"src/inventory.gd": inventorySource})
	require.Equal(t, ExitOK, mustGenerate(t, root))
	require.NoError(t, os.Remove(filepath.Join(root, "doubles", "inventory_double.gd")))
	require.NoError(t, os.Remove(filepath.Join(root, "doubles", "inventory_double.gd.uid")))
	before := snapshotProject(t, root)

	exitCode, stdout, _ := runGenerate(t, "--check", "--project", root)

	require.Equal(t, ExitFailures, exitCode)
	assert.Equal(t, "missing: doubles/inventory_double.gd\nmissing: doubles/inventory_double.gd.uid\n", stdout)
	assert.Equal(t, before, snapshotProject(t, root))
}

func TestGenerateDoublesRemovedEntryIsUnexpectedThenRemoved(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, inventoryConfiguration, map[string]string{"src/inventory.gd": inventorySource})
	require.Equal(t, ExitOK, mustGenerate(t, root))
	writeTestFile(t, root, "gdtest.json", `{"test_doubles":[]}`)
	before := snapshotProject(t, root)

	exitCode, stdout, _ := runGenerate(t, "--check", "--project", root)
	require.Equal(t, ExitFailures, exitCode)
	assert.Equal(t, "unexpected: doubles/inventory_double.gd\nunexpected: doubles/inventory_double.gd.uid\n", stdout)
	assert.Equal(t, before, snapshotProject(t, root))

	exitCode, stdout, _ = runGenerate(t, "--project", root)
	require.Equal(t, ExitOK, exitCode)
	assert.Equal(t, "0 file(s) written; 2 file(s) removed\n", stdout)
	assert.NoFileExists(t, filepath.Join(root, "doubles", "inventory_double.gd"))
	assert.NoFileExists(t, filepath.Join(root, "doubles", "inventory_double.gd.uid"))
}

func TestGenerateDoublesRemovedMarkerIsRefused(t *testing.T) {
	t.Parallel()

	root := writeGenerateProject(t, inventoryConfiguration, map[string]string{"src/inventory.gd": inventorySource})
	require.Equal(t, ExitOK, mustGenerate(t, root))
	output := filepath.Join(root, "doubles", "inventory_double.gd")
	content, err := os.ReadFile(output)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, bytes.TrimPrefix(content, []byte(strings.SplitN(string(content), "\n", 2)[0]+"\n")), 0o644))
	before := snapshotProject(t, root)

	exitCode, stdout, stderr := runGenerate(t, "--check", "--project", root)

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "missing gdtest generated-double marker")
	assert.Equal(t, before, snapshotProject(t, root))
}

func TestGenerateDoublesRejectsDirectoryWithoutProject(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr := runGenerate(t, "--project", t.TempDir())

	require.Equal(t, ExitError, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "is not a Godot project")
}

func TestGenerateHelpAndMissingTargetExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		exitCode  int
		stdout    string
		stderr    string
	}{
		{name: "parent help", arguments: []string{"generate", "--help"}, exitCode: ExitOK, stdout: "doubles"},
		{name: "doubles help", arguments: []string{"generate", "doubles", "--help"}, exitCode: ExitOK, stdout: "--check"},
		{name: "bare parent", arguments: []string{"generate"}, exitCode: ExitError, stderr: "generate requires a target"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer

			exitCode := Run(context.Background(), test.arguments, IO{Out: &stdout, Err: &stderr})

			require.Equal(t, test.exitCode, exitCode, "stderr=%q", stderr.String())
			assert.Contains(t, stdout.String(), test.stdout)
			assert.Contains(t, stderr.String(), test.stderr)
		})
	}
}

func mustGenerate(t *testing.T, root string) int {
	t.Helper()
	exitCode, _, stderr := runGenerate(t, "--project", root)
	require.Equal(t, ExitOK, exitCode, "stderr=%q", stderr)
	return exitCode
}
