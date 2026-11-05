package doubles

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

func TestAnalyzeChainResolvesBaseByClassName(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "class_name Base\nextends RefCounted\n\nfunc inherited(value: int) -> int:\n\treturn value\n",
		"src/child.gd": "class_name Child\nextends Base\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "src/base.gd", model.Methods[0].DeclaringPath)
}

func TestAnalyzeChainResolvesBaseByResPath(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "extends RefCounted\n\nfunc inherited() -> void:\n\tpass\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "src/base.gd", model.Methods[0].DeclaringPath)
}

func TestAnalyzeChainResolvesBaseByRelativePath(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base/base.gd": "extends RefCounted\n\nfunc inherited() -> void:\n\tpass\n",
		"src/child.gd":     "extends \"base/base.gd\"\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "src/base/base.gd", model.Methods[0].DeclaringPath)
}

func TestAnalyzeChainCycleIsDiagnosed(t *testing.T) {
	t.Parallel()

	_, diagnostics := analyzeEntry(t, map[string]string{
		"src/a.gd": "class_name A\nextends B\n",
		"src/b.gd": "class_name B\nextends \"res://src/a.gd\"\n",
	}, entryFor("a.gd"))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/b.gd", diagnostics[0].Path)
	assert.Equal(t, 2, diagnostics[0].Line)
	assert.Contains(t, diagnostics[0].Message, "inheritance cycle through src/a.gd")
}

func TestAnalyzeSelfExtendsIsCycle(t *testing.T) {
	t.Parallel()

	_, diagnostics := analyzeEntry(t, map[string]string{
		"src/a.gd": "extends \"res://src/a.gd\"\n",
	}, entryFor("a.gd"))
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, "inheritance cycle")
}

func TestAnalyzeMissingBaseIsDiagnosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		extends string
		message string
	}{
		{name: "missing res path", extends: `extends "res://src/missing.gd"`, message: `base class "res://src/missing.gd" does not exist`},
		{name: "missing relative path", extends: `extends "missing.gd"`, message: `base class "missing.gd" does not exist`},
		{name: "relative path escapes project", extends: `extends "../../outside.gd"`, message: `resolves outside the project`},
		{name: "unknown identifier is native not missing", extends: "extends MissingClass", message: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diagnostics := analyzeEntry(t, map[string]string{
				"src/child.gd": test.extends + "\n",
			}, entryFor("child.gd"))
			if test.message == "" {
				assert.Empty(t, diagnostics)
				return
			}
			require.Len(t, diagnostics, 1)
			assert.Contains(t, diagnostics[0].Message, test.message)
			assert.Equal(t, 1, diagnostics[0].Line)
		})
	}
}

func TestAnalyzeBaseThatFailedToParseIsDiagnosed(t *testing.T) {
	t.Parallel()

	index, indexDiagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/base.gd":  "func (:\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n",
	}), nil)
	require.Len(t, indexDiagnostics, 1)
	assert.Equal(t, "src/base.gd", indexDiagnostics[0].Path)
	_, diagnostics := Analyze(index, entryFor("child.gd"))
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, `base class "res://src/base.gd" failed to parse`)
}

func TestAnalyzeNativeTerminalStopsChain(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/node_child.gd": "class_name NodeChild\nextends Node\n\nfunc tick(delta: float) -> void:\n\tpass\n",
	}, entryFor("node_child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "src/node_child.gd", model.Methods[0].DeclaringPath)
}

func TestAnalyzeNoExtendsMeansNativeRefCounted(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/plain.gd": "func value() -> int:\n\treturn 1\n",
	}, entryFor("plain.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "value", model.Methods[0].Name)
}

func TestAnalyzeUnsupportedBaseExpressionIsDiagnosed(t *testing.T) {
	t.Parallel()

	_, diagnostics := analyzeEntry(t, map[string]string{
		"src/child.gd": "extends Outer.Inner\n",
	}, entryFor("child.gd"))
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, "base class must be a string path or a class_name identifier")
}

func TestAnalyzeNearestOverrideWins(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd": "class_name Base\nextends RefCounted\n\nconst STEP: int = 1\n\n" +
			"func step(amount: int = STEP) -> int:\n\treturn amount\n",
		"src/child.gd": "class_name Child\nextends Base\n\n" +
			"func step(amount: int = 2) -> int:\n\treturn amount * 2\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	method := model.Methods[0]
	assert.Equal(t, "src/child.gd", method.DeclaringPath)
	require.Len(t, method.Parameters, 1)
	assert.Equal(t, "2", method.Parameters[0].Default)
}

func TestAnalyzeSkipsUnderscoreAndStaticMethods(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/service.gd": "extends RefCounted\n\n" +
			"func _init() -> void:\n\tpass\n\n" +
			"func _private() -> void:\n\tpass\n\n" +
			"static func build() -> int:\n\treturn 1\n\n" +
			"func public() -> void:\n\tpass\n",
	}, entryFor("service.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "public", model.Methods[0].Name)
}

func TestAnalyzeInnerClassMethodsAreNotDoubled(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/outer.gd": "extends RefCounted\n\nclass Inner:\n\tfunc hidden() -> void:\n\t\tpass\n\n" +
			"func visible() -> void:\n\tpass\n",
	}, entryFor("outer.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "visible", model.Methods[0].Name)
}

func TestAnalyzeConstructorIsNearestInitOfChain(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "extends RefCounted\n\nfunc _init(seed: int, label: String = \"bag\") -> void:\n\tpass\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	assert.True(t, model.Constructor.HasInit)
	require.Len(t, model.Constructor.Parameters, 2)
	assert.Equal(t, Parameter{Name: "seed", Type: "int"}, model.Constructor.Parameters[0])
	assert.Equal(t, Parameter{Name: "label", Type: "String", Default: `"bag"`}, model.Constructor.Parameters[1])
}

func TestAnalyzeConstructorAbsentWhenNoInitInChain(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/plain.gd": "extends RefCounted\n\nfunc value() -> int:\n\treturn 1\n",
	}, entryFor("plain.gd"))
	require.Empty(t, diagnostics)
	assert.False(t, model.Constructor.HasInit)
	assert.Empty(t, model.Constructor.Parameters)
}

func TestAnalyzeMissingSourceIsDiagnosed(t *testing.T) {
	t.Parallel()

	_, diagnostics := analyzeEntry(t, map[string]string{}, entryFor("absent.gd"))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/absent.gd", diagnostics[0].Path)
	assert.Equal(t, "source script does not exist in the project", diagnostics[0].Message)
}

func TestAnalyzeSourceThatFailedToParseIsDiagnosed(t *testing.T) {
	t.Parallel()

	index, indexDiagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/broken.gd": "func (:\n",
	}), nil)
	require.Len(t, indexDiagnostics, 1)
	_, diagnostics := Analyze(index, config.TestDouble{Source: "src/broken.gd", Output: "doubles/broken_double.gd"})
	assert.Equal(t, []string{"source script failed to parse"}, messagesOf(diagnostics))
}
