package projectmeta

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReturnsDeterministicPathNeutralRuntimeInputs(t *testing.T) {
	t.Parallel()
	metadata, err := Parse([]byte(`config_version=5

[application]
run/main_scene="res://scenes/main.tscn"

[autoload]
Zed="res://globals/zed.gd"
Alpha="*res://globals/alpha.gd"

[native_extensions]
paths=PackedStringArray("res://native/game.gdextension")

[native]
library="res://native/libgame.so"
`))
	require.NoError(t, err)
	assert.Equal(t, []Autoload{
		{Name: "Alpha", Path: "globals/alpha.gd", Singleton: true, Location: Location{File: projectFilename, Line: 8, Column: 7}},
		{Name: "Zed", Path: "globals/zed.gd", Singleton: false, Location: Location{File: projectFilename, Line: 7, Column: 5}},
	}, metadata.Autoloads)
	assert.Equal(t, []string{
		"globals/alpha.gd", "globals/zed.gd", "native/game.gdextension",
		"native/libgame.so", "scenes/main.tscn",
	}, metadata.GlobalInputs)
	assert.Equal(t, []string{"native/game.gdextension", "native/libgame.so"}, metadata.NativeInputs)
	assert.Empty(t, metadata.Diagnostics)
	assert.False(t, metadata.ConservativeAll)
}

func TestParseNativeExtensionPathsRejectsMalformedOrUnresolvableEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		line   string
		code   string
		column int
	}{
		{name: "wrong container", line: `paths=42`, code: "project.native-extensions.invalid-value", column: 7},
		{name: "wrong element type", line: `paths=["res://native/ok.gdextension", 42]`, code: "project.native-extensions.invalid-value", column: 7},
		{name: "uid", line: `paths=PackedStringArray("uid://extension.gdextension")`, code: "project.native-extensions.unresolved-path", column: 25},
		{name: "wrong suffix", line: `paths=["res://native/library.so"]`, code: "project.native-extensions.unresolved-path", column: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := Parse([]byte("config_version=5\n[native_extensions]\n" + test.line + "\n"))
			require.NoError(t, err)
			assert.True(t, metadata.ConservativeAll)
			require.Len(t, metadata.Diagnostics, 1)
			assert.Equal(t, test.code, metadata.Diagnostics[0].Code)
			assert.Equal(t, Location{File: projectFilename, Line: 3, Column: test.column}, metadata.Diagnostics[0].Location)
		})
	}
}

func TestParseIgnoresCommentedOutAutoloads(t *testing.T) {
	t.Parallel()
	metadata, err := Parse([]byte(`config_version=5
[autoload]
; Disabled="*res://globals/disabled.gd"
# AlsoDisabled="res://globals/also_disabled.gd"
Enabled="*res://globals/enabled.gd"
`))
	require.NoError(t, err)
	assert.Equal(t, []Autoload{{
		Name: "Enabled", Path: "globals/enabled.gd", Singleton: true,
		Location: Location{File: projectFilename, Line: 5, Column: 9},
	}}, metadata.Autoloads)
	assert.Equal(t, []string{"globals/enabled.gd"}, metadata.GlobalInputs)
	assert.False(t, metadata.ConservativeAll)
}

func TestParseMalformedOrUnresolvedGlobalInputsWidensConservatively(t *testing.T) {
	t.Parallel()
	metadata, err := Parse([]byte(`config_version=5

[application]
run/main_scene="uid://unresolved"

[autoload]
BadType=42
BadPath="*res://../outside.gd"
Duplicate="*res://globals/one.gd"
Duplicate="*res://globals/two.gd"
`))
	require.NoError(t, err)
	assert.True(t, metadata.ConservativeAll)
	assert.Equal(t, []Autoload{
		{Name: "Duplicate", Path: "globals/one.gd", Singleton: true, Location: Location{File: projectFilename, Line: 9, Column: 11}},
		{Name: "Duplicate", Path: "globals/two.gd", Singleton: true, Location: Location{File: projectFilename, Line: 10, Column: 11}},
	}, metadata.Autoloads)
	assert.Equal(t, []string{"globals/one.gd", "globals/two.gd"}, metadata.GlobalInputs)
	assert.Equal(t, []string{
		"project.main-scene", "project.autoload.invalid-value",
		"project.autoload.unresolved-path", "project.autoload.duplicate",
	}, diagnosticCodes(metadata.Diagnostics))
	for _, diagnostic := range metadata.Diagnostics {
		assert.Equal(t, projectFilename, diagnostic.Location.File)
		assert.Positive(t, diagnostic.Location.Line)
		assert.Positive(t, diagnostic.Location.Column)
	}
}

func TestParseNativeUIDOrExternalPathWidensConservatively(t *testing.T) {
	t.Parallel()
	metadata, err := Parse([]byte(`config_version=5

[runtime]
one="uid://native-extension.gdextension"
two="/tmp/native.dll"
`))
	require.NoError(t, err)
	assert.True(t, metadata.ConservativeAll)
	assert.Empty(t, metadata.NativeInputs)
	assert.Equal(t, []string{"project.native-input", "project.native-input"}, diagnosticCodes(metadata.Diagnostics))
}

func TestLoadReadsProjectGodotWithoutLeakingRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, projectFilename), []byte(`config_version=5
[autoload]
Global="*res://global.gd"
`), 0o600))
	metadata, err := Load(root)
	require.NoError(t, err)
	require.Len(t, metadata.Autoloads, 1)
	assert.Equal(t, "global.gd", metadata.Autoloads[0].Path)
	assert.NotContains(t, metadata.Autoloads[0].Location.File, root)
}

func TestLoadParsesGDExtensionLibrariesAndDependencies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "addons", "native", "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, projectFilename), []byte(`config_version=5
[native_extensions]
paths=PackedStringArray("res://addons/native/game.gdextension")
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons", "native", "game.gdextension"), []byte(`[configuration]
entry_symbol = "game_init"

[libraries]
macos.debug = "res://addons/native/bin/game.framework"
linux.debug = "bin/libgame.so"

[dependencies]
linux.debug = {
  "res://addons/native/bin/libsupport.so": "",
  "bin/librelative.so": ""
}
`), 0o600))

	metadata, err := Load(root)
	require.NoError(t, err)
	assert.False(t, metadata.ConservativeAll)
	assert.Empty(t, metadata.Diagnostics)
	assert.Equal(t, []string{
		"addons/native/bin/game.framework",
		"addons/native/bin/libgame.so",
		"addons/native/bin/librelative.so",
		"addons/native/bin/libsupport.so",
		"addons/native/game.gdextension",
	}, metadata.NativeInputs)
	assert.Equal(t, metadata.NativeInputs, metadata.GlobalInputs)
	for _, input := range metadata.NativeInputs {
		assert.NotContains(t, input, root)
	}
}

func TestLoadMissingGDExtensionWidensConservatively(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, projectFilename), []byte(`config_version=5
[native_extensions]
paths=["res://missing.gdextension"]
`), 0o600))

	metadata, err := Load(root)
	require.NoError(t, err)
	assert.True(t, metadata.ConservativeAll)
	require.Len(t, metadata.Diagnostics, 1)
	assert.Equal(t, "project.gdextension.read", metadata.Diagnostics[0].Code)
	assert.Equal(t, Location{File: "missing.gdextension", Line: 1, Column: 1}, metadata.Diagnostics[0].Location)
}

func TestParseErrorIncludesStableFilename(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("[autoload\nBad=true\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), projectFilename)
}

func diagnosticCodes(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Code
	}
	return result
}
