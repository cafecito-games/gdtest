package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePreservesMethodSourceOrderAndStableIDs(t *testing.T) {
	t.Parallel()

	source := []byte(`extends GDTest

func test_second_name() -> void:
	pass

func helper() -> void:
	pass

func test_first_alphabetically__only() -> void:
	pass
`)
	manifest, err := Parse("tests/example.test.gd", source)
	require.NoError(t, err)
	require.Len(t, manifest.Cases, 2)
	assert.Equal(t, "tests/example.test.gd::test_second_name", manifest.Cases[0].ID)
	assert.Equal(t, "tests/example.test.gd::test_first_alphabetically", manifest.Cases[1].ID)
	assert.True(t, manifest.Cases[1].IsOnly)
}

func TestParseRejectsArgumentsWithoutProvider(t *testing.T) {
	t.Parallel()

	_, err := Parse("bad.test.gd", []byte("func test_bad(value: int) -> void:\n\tpass\n"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "test_bad__cases")
}

func TestSelectUnknownSelector(t *testing.T) {
	t.Parallel()

	file, err := Parse("one.test.gd", []byte("func test_one() -> void:\n\tpass\n"))
	require.NoError(t, err)
	_, err = Select(Manifest{Files: []model.FilePlan{file}}, []string{"missing"}, false)
	require.Error(t, err)
}

func TestDiscoverResolvesTransitiveGDTestAndMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "framework/base.gd", `class_name ProjectTest
extends GDTest
`)
	writeFixture(t, root, "tests/example.test.gd", `class_name ExampleTest extends ProjectTest

signal completed(value: int)

func before_case(case: GDTestCaseInfo) -> void:
	pass

func test_default_argument(value: int = 1) -> void:
	pass

func test_rows__only(value: int) -> void:
	pass

func test_rows__cases() -> Array[Variant]:
	return [[1]]

var helper = preload("res://src/helper.gd")
`)
	writeFixture(t, root, "src/helper.gd", "extends RefCounted\n")
	writeFixture(t, root, "tests/not_a_test.test.gd", "extends Node\nfunc test_ignored():\n\tpass\n")

	manifest, err := Discover(root, Options{})
	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	assert.Equal(t, "tests/example.test.gd", manifest.Files[0].Path)
	require.Len(t, manifest.Files[0].Cases, 2)
	assert.Equal(t, "tests/example.test.gd::test_rows", manifest.Files[0].Cases[1].ID)
	metadata := scriptByPath(t, manifest, "tests/example.test.gd")
	assert.True(t, metadata.ExtendsGDTest)
	assert.Equal(t, "ExampleTest", metadata.ClassName)
	assert.Equal(t, "ProjectTest", metadata.BaseClass)
	assert.True(t, metadata.HasFocus)
	require.Len(t, metadata.Hooks, 1)
	assert.Equal(t, "before_case", metadata.Hooks[0].Name)
	require.Len(t, metadata.Providers, 1)
	assert.Equal(t, "test_rows", metadata.Providers[0].TestMethod)
	assert.Equal(t, []string{"framework/base.gd", "src/helper.gd"}, metadata.Dependencies)
	assert.False(t, metadata.HasDynamicDependency)
	assert.NotEmpty(t, metadata.StructuralFingerprint)
	assert.NotEmpty(t, metadata.ContentFingerprint)
	assert.NotEmpty(t, manifest.ContentFingerprint)
}

func TestDiscoverResolvesPathBasedInheritance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "base.gd", "extends GDTest\n")
	writeFixture(t, root, "path.test.gd", "extends \"res://base.gd\"\nfunc test_path():\n\tpass\n")
	manifest, err := Discover(root, Options{})
	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	require.Len(t, manifest.Files[0].Cases, 1)
	assert.Equal(t, "path.test.gd::test_path", manifest.Files[0].Cases[0].ID)
}

func TestDiscoverManifestIsDeterministicAcrossWorktrees(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	for _, root := range []string{first, second} {
		writeFixture(t, root, "tests/stable.test.gd", "extends GDTest\nfunc test_stable():\n\tpass\n")
		writeFixture(t, root, "src/value.gd", "class_name Value\nextends RefCounted\n")
	}
	one, err := Discover(first, Options{})
	require.NoError(t, err)
	two, err := Discover(second, Options{})
	require.NoError(t, err)
	assert.Equal(t, one, two)
}

func TestStructuralFingerprintIgnoresFunctionBodies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filename := filepath.Join(root, "example.test.gd")
	writeFixture(t, root, "example.test.gd", "extends GDTest\nfunc test_value() -> void:\n\tvar value := 1\n")
	first, err := Discover(root, Options{})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, []byte("extends GDTest\nfunc test_value() -> void:\n\tvar value := 2\n"), 0o600))
	second, err := Discover(root, Options{})
	require.NoError(t, err)
	assert.Equal(t, first.StructuralFingerprint, second.StructuralFingerprint)
	assert.NotEqual(t, first.ContentFingerprint, second.ContentFingerprint)
}

func TestStructuralFingerprintTracksClassBaseAndSignals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			"class name",
			"class_name First extends GDTest\nfunc test_value():\n\tpass\n",
			"class_name Second extends GDTest\nfunc test_value():\n\tpass\n",
		},
		{
			"base class",
			"class_name Example extends GDTest\nfunc test_value():\n\tpass\n",
			"class_name Example extends OtherTestBase\nfunc test_value():\n\tpass\n",
		},
		{
			"signal",
			"extends GDTest\nsignal changed(value: int)\nfunc test_value():\n\tpass\n",
			"extends GDTest\nsignal changed(value: String)\nfunc test_value():\n\tpass\n",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			beforeTree, err := gdparser.ParseString(testCase.before)
			require.NoError(t, err)
			afterTree, err := gdparser.ParseString(testCase.after)
			require.NoError(t, err)
			before, err := inspectScript("example.test.gd", []byte(testCase.before), beforeTree, true)
			require.NoError(t, err)
			after, err := inspectScript("example.test.gd", []byte(testCase.after), afterTree, true)
			require.NoError(t, err)
			assert.NotEqual(t, before.metadata.StructuralFingerprint, after.metadata.StructuralFingerprint)
		})
	}
}

func TestDiscoverReportsSourceLocatedParseFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "broken.test.gd", "extends GDTest\nfunc test_broken( -> void:\n\tpass\n")
	_, err := Discover(root, Options{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "broken.test.gd:2:")
}

func TestDiscoverDoesNotApplyGDTestContractsToProductionScripts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "tests/example.test.gd", "extends GDTest\nfunc test_ok():\n\tpass\n")
	writeFixture(t, root, "src/domain.gd", `extends Node
func before_all(value: int) -> int:
	return value
func test_business_rule() -> int:
	return 1
func rows__cases(value: int) -> String:
	return str(value)
`)
	manifest, err := Discover(root, Options{})
	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	assert.Equal(t, "tests/example.test.gd", manifest.Files[0].Path)
}

func TestParseValidatesProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"arguments", "func test_x__cases(value: int) -> Array:\n\treturn []\n", "must take no arguments"},
		{"return type", "func test_x__cases() -> String:\n\treturn \"bad\"\n", "must return Array or Dictionary"},
		{"await", "func test_x__cases() -> Array:\n\treturn await rows()\n", "must be synchronous"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse("provider.test.gd", []byte(testCase.source))
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestParseRecognizesTimeoutProvidersWithoutDiscoveringThemAsTests(t *testing.T) {
	t.Parallel()

	file, err := Parse("timeout.test.gd", []byte(`func get_case_timeout() -> float:
	return 2.0

func test_slow() -> void:
	pass

func test_slow__timeout() -> float:
	return 0.25
`))
	require.NoError(t, err)
	require.Len(t, file.Cases, 1)
	assert.Equal(t, "test_slow", file.Cases[0].MethodName)
}

func TestParseValidatesTimeoutProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"arguments", "func test_x__timeout(value: int) -> float:\n\treturn 1.0\n", "must take no arguments"},
		{"return type", "func test_x__timeout() -> int:\n\treturn 1\n", "must return float"},
		{"missing return type", "func get_case_timeout():\n\treturn 1.0\n", "must return float"},
		{"await", "func test_x__timeout() -> float:\n\treturn await timeout_value()\n", "must be synchronous"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse("timeout.test.gd", []byte(testCase.source))
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestParseValidatesTestAndHookContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"test return", "func test_value() -> int:\n\treturn 1\n", "must return void"},
		{"suite arguments", "func suite(value: int) -> void:\n\tpass\n", "suite must take no arguments"},
		{"suite return", "func suite() -> int:\n\treturn 1\n", "suite must return void"},
		{"hook arguments", "func before_all(case) -> void:\n\tpass\n", "must take 0 argument"},
		{"hook return", "func after_case(case) -> bool:\n\treturn true\n", "must return void"},
		{
			"hook aliases",
			"func before_case(case) -> void:\n\tpass\nfunc before_each(case) -> void:\n\tpass\n",
			"cannot both be defined",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse("contract.test.gd", []byte(testCase.source))
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func writeFixture(t *testing.T, root, relative, contents string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
	require.NoError(t, os.WriteFile(filename, []byte(contents), 0o600))
}

func scriptByPath(t *testing.T, manifest Manifest, filename string) ScriptMetadata {
	t.Helper()
	for _, script := range manifest.Scripts {
		if script.Path == filename {
			return script
		}
	}
	require.FailNowf(t, "script not found", "script %q not found", filename)
	return ScriptMetadata{}
}
