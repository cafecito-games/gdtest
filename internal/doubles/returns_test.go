package doubles

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// returnProject declares a source with an enum and an inner class, a project
// class with its own enum and inner class, and a probe method returning returnType.
func returnProject(returnType string) map[string]string {
	return map[string]string{
		"src/source.gd": "class_name Source\nextends RefCounted\n\nenum Mode { ON, OFF }\n\n" +
			"class Inner extends RefCounted:\n\tpass\n\n" +
			"func probe() -> " + returnType + ":\n\treturn null\n",
		"src/calc.gd": "class_name Calc\nextends RefCounted\n\nenum Mode { A, B }\n\n" +
			"class Deep extends RefCounted:\n\tpass\n",
	}
}

func TestAnalyzeReturnCheckTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		returnType   string
		check        ReturnCheck
		expectedType string
	}{
		{name: "void", returnType: "void", check: ReturnCheckVoid},
		{name: "Variant", returnType: "Variant", check: ReturnCheckAny},
		{name: "builtin scalar", returnType: "bool", check: ReturnCheckBuiltin, expectedType: "bool"},
		{name: "builtin packed array", returnType: "PackedVector4Array", check: ReturnCheckBuiltin, expectedType: "PackedVector4Array"},
		{name: "bare Dictionary", returnType: "Dictionary", check: ReturnCheckBuiltin, expectedType: "Dictionary"},
		{name: "typed array", returnType: "Array[int]", check: ReturnCheckTypedArray, expectedType: "Array[int]"},
		{name: "typed dictionary", returnType: "Dictionary[String, int]", check: ReturnCheckTypedDictionary, expectedType: "Dictionary[String, int]"},
		{name: "chain enum unqualified", returnType: "Mode", check: ReturnCheckEnum, expectedType: "Mode"},
		{name: "project class enum qualified", returnType: "Calc.Mode", check: ReturnCheckEnum, expectedType: "Calc.Mode"},
		{name: "own class_name", returnType: "Source", check: ReturnCheckObject, expectedType: "Source"},
		{name: "chain inner class", returnType: "Inner", check: ReturnCheckObject, expectedType: "Inner"},
		{name: "project inner class qualified", returnType: "Calc.Deep", check: ReturnCheckObject, expectedType: "Calc.Deep"},
		{name: "native class unqualified", returnType: "Node", check: ReturnCheckObject, expectedType: "Node"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			model, diagnostics := analyzeEntry(t, returnProject(test.returnType), entryFor("source.gd"))
			require.Empty(t, diagnostics)
			method := methodNamed(t, model, "probe")
			assert.Equal(t, test.returnType, method.ReturnType)
			assert.Equal(t, test.check, method.ReturnCheck)
			assert.Equal(t, test.expectedType, method.ExpectedType)
		})
	}
}

func TestAnalyzeUnsupportedReturnTypesAreDiagnosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		returnType string
	}{
		{name: "native enum qualified", returnType: "Node.ProcessMode"},
		{name: "project qualifier without member", returnType: "Calc.Missing"},
		{name: "chain member of a qualifier that is not a project class", returnType: "Source.Mode.ON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diagnostics := analyzeEntry(t, returnProject(test.returnType), entryFor("source.gd"))
			require.Len(t, diagnostics, 1)
			assert.Equal(t, "src/source.gd", diagnostics[0].Path)
			assert.Equal(t, 9, diagnostics[0].Line)
			assert.Contains(t, diagnostics[0].Message, "return type "+test.returnType+" of method probe is not supported")
		})
	}
}

func TestAnalyzeReturnCheckRowsAreTheOnlyCheckedValue(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, returnProject("Array[Mode]"), entryFor("source.gd"))
	require.Empty(t, diagnostics)
	assert.Equal(t, ReturnCheckTypedArray, methodNamed(t, model, "probe").ReturnCheck)
}
