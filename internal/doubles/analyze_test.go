package doubles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "godot", "api", name))
	require.NoError(t, err)
	return string(data)
}

func methodNamed(t *testing.T, model Model, name string) Method {
	t.Helper()
	for _, method := range model.Methods {
		if method.Name == name {
			return method
		}
	}
	require.Failf(t, "method missing", "no method %q in %v", name, model.Methods)
	return Method{}
}

func TestAnalyzeInventoryFixture(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/inventory.gd": readFixture(t, "inventory.gd"),
	}, config.TestDouble{Source: "src/inventory.gd", Output: "doubles/inventory_double.gd", ClassName: "InventoryDouble"})
	require.Empty(t, diagnostics)

	assert.Equal(t, "res://src/inventory.gd", model.SourceResource)
	assert.Equal(t, "res://doubles/inventory_double.gd", model.OutputResource)
	assert.Equal(t, "InventoryDouble", model.ClassName)
	assert.Equal(t, "Inventory", model.SourceClassName)
	assert.True(t, model.Constructor.HasInit)
	assert.Equal(t, []Parameter{
		{Name: "capacity", Type: "int"},
		{Name: "label", Type: "String", Default: `"bag"`},
	}, model.Constructor.Parameters)

	require.Len(t, model.Methods, 3)
	assert.Equal(t, "add", model.Methods[0].Name)
	assert.Equal(t, "clear", model.Methods[1].Name)
	assert.Equal(t, "load_counts", model.Methods[2].Name)

	add := model.Methods[0]
	assert.Equal(t, "AddMethod", add.ControllerName)
	assert.Equal(t, []Parameter{
		{Name: "item", Type: "String"},
		{Name: "count", Type: "int", Default: "LIMIT"},
	}, add.Parameters)
	assert.Equal(t, ReturnCheckBuiltin, add.ReturnCheck)
	assert.Equal(t, "bool", add.ExpectedType)
	assert.False(t, add.Async)
	assert.False(t, add.Abstract)

	clear := model.Methods[1]
	assert.Equal(t, ReturnCheckVoid, clear.ReturnCheck)
	assert.Empty(t, clear.ExpectedType)

	loadCounts := model.Methods[2]
	assert.Equal(t, "LoadCountsMethod", loadCounts.ControllerName)
	assert.Equal(t, ReturnCheckTypedDictionary, loadCounts.ReturnCheck)
	assert.Equal(t, "Dictionary[String, int]", loadCounts.ExpectedType)
	assert.True(t, loadCounts.Async)
}

func TestAnalyzeLedgerFixtureInitWithoutParameters(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/ledger.gd": readFixture(t, "ledger.gd"),
	}, entryFor("ledger.gd"))
	require.Empty(t, diagnostics)
	assert.True(t, model.Constructor.HasInit)
	assert.Empty(t, model.Constructor.Parameters)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "clear", model.Methods[0].Name)
}

func TestAnalyzeCalculatorFixtureAbstractMethod(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/calculator.gd": readFixture(t, "calculator.gd"),
	}, entryFor("calculator.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	compute := model.Methods[0]
	assert.Equal(t, "compute", compute.Name)
	assert.True(t, compute.Abstract)
	assert.False(t, compute.Async)
	assert.Equal(t, ReturnCheckBuiltin, compute.ReturnCheck)
	assert.Equal(t, "int", compute.ExpectedType)
}

func TestAnalyzeAsyncDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		files  map[string]string
		method string
		want   bool
	}{
		{
			name: "direct await in body",
			files: map[string]string{"src/source.gd": "extends RefCounted\n\n" +
				"func wait() -> void:\n\tawait Engine.get_main_loop().process_frame\n"},
			method: "wait",
			want:   true,
		},
		{
			name: "await only in a condition",
			files: map[string]string{"src/source.gd": "extends RefCounted\n\n" +
				"func wait() -> bool:\n\tif await probe():\n\t\treturn true\n\treturn false\n\n" +
				"func probe() -> bool:\n\treturn true\n"},
			method: "wait",
			want:   true,
		},
		{
			name: "await only inside a lambda",
			files: map[string]string{"src/source.gd": "extends RefCounted\n\n" +
				"func schedule() -> Callable:\n\treturn func(): await Engine.get_main_loop().process_frame\n"},
			method: "schedule",
			want:   false,
		},
		{
			name: "inherited async from an ancestor",
			files: map[string]string{
				"src/base.gd":  "extends RefCounted\n\nfunc load_data() -> int:\n\tawait Engine.get_main_loop().process_frame\n\treturn 1\n",
				"src/child.gd": "extends \"res://src/base.gd\"\n",
			},
			method: "load_data",
			want:   true,
		},
		{
			name: "nearer synchronous override of an async ancestor is still async",
			files: map[string]string{
				"src/base.gd":  "extends RefCounted\n\nfunc load_data() -> int:\n\tawait Engine.get_main_loop().process_frame\n\treturn 1\n",
				"src/child.gd": "extends \"res://src/base.gd\"\n\nfunc load_data() -> int:\n\treturn 2\n",
			},
			method: "load_data",
			want:   true,
		},
		{
			name:   "synchronous method",
			files:  map[string]string{"src/source.gd": "extends RefCounted\n\nfunc plain() -> int:\n\treturn 1\n"},
			method: "plain",
			want:   false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			entry := entryFor("source.gd")
			if _, exists := test.files["src/child.gd"]; exists {
				entry = entryFor("child.gd")
			}
			model, diagnostics := analyzeEntry(t, test.files, entry)
			require.Empty(t, diagnostics)
			require.NotEmpty(t, model.Methods)
			assert.Equal(t, test.want, methodNamed(t, model, test.method).Async)
		})
	}
}

func TestAnalyzeNearestOverrideOfAbstractMethod(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "@abstract\nextends RefCounted\n\n@abstract\nfunc compute(value: int) -> int\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n\nfunc compute(value: int) -> int:\n\treturn value\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.False(t, model.Methods[0].Abstract)
	assert.Equal(t, "src/child.gd", model.Methods[0].DeclaringPath)
}

func TestAnalyzeAbstractInheritedMethodIsAbstract(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "@abstract\nextends RefCounted\n\n@abstract\nfunc compute(value: int) -> int\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.True(t, model.Methods[0].Abstract)
}

func TestAnalyzeCopiesDefaultsVerbatimFromDeclaringScript(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd": "extends RefCounted\n\nconst LIMIT: int = 5\n\n" +
			"func configure(label: String = \"bag\", limit: int = LIMIT, tags: Array[String] = [ \"a\" , \"b\" ]) -> void:\n\tpass\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, []Parameter{
		{Name: "label", Type: "String", Default: `"bag"`},
		{Name: "limit", Type: "int", Default: "LIMIT"},
		{Name: "tags", Type: "Array[String]", Default: `[ "a" , "b" ]`},
	}, model.Methods[0].Parameters)
}

func TestAnalyzeRefusesUnprovableSignatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		message  string
		line     int
		filename string
	}{
		{
			name:    "untyped parameter",
			source:  "extends RefCounted\n\nfunc add(item) -> bool:\n\treturn true\n",
			message: "parameter item is untyped",
			line:    3,
		},
		{
			name:    "inferred parameter",
			source:  "extends RefCounted\n\nfunc add(item := 1) -> bool:\n\treturn true\n",
			message: "parameter item is inferred with := and must declare its type",
			line:    3,
		},
		{
			name:    "variadic parameter",
			source:  "extends RefCounted\n\nfunc add(...items) -> bool:\n\treturn true\n",
			message: "parameter items is variadic",
			line:    3,
		},
		{
			name:    "missing return type",
			source:  "extends RefCounted\n\nfunc add(item: int):\n\tpass\n",
			message: "method add has no return type",
			line:    3,
		},
		{
			name:    "untyped parameter of an abstract method",
			source:  "@abstract\nextends RefCounted\n\n@abstract\nfunc add(item) -> bool\n",
			message: "parameter item is untyped",
			line:    5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diagnostics := analyzeEntry(t, map[string]string{"src/source.gd": test.source}, entryFor("source.gd"))
			require.Len(t, diagnostics, 1)
			assert.Equal(t, "src/source.gd", diagnostics[0].Path)
			assert.Equal(t, test.line, diagnostics[0].Line)
			assert.Contains(t, diagnostics[0].Message, test.message)
		})
	}
}

func TestAnalyzeRefusesReservedAndCollidingNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		message string
	}{
		{
			name:    "reserved member gdtest",
			source:  "extends RefCounted\n\nvar gdtest: int\n",
			message: "member gdtest is reserved by generated doubles",
		},
		{
			name:    "reserved member GDTestControls",
			source:  "extends RefCounted\n\nconst GDTestControls = 1\n",
			message: "member GDTestControls is reserved by generated doubles",
		},
		{
			name:    "reserved member _GDTEST_DOUBLE_PATH",
			source:  "extends RefCounted\n\nconst _GDTEST_DOUBLE_PATH = \"x\"\n",
			message: "member _GDTEST_DOUBLE_PATH is reserved by generated doubles",
		},
		{
			name:    "reserved enum member",
			source:  "extends RefCounted\n\nenum State { gdtest }\n",
			message: "member gdtest is reserved by generated doubles",
		},
		{
			name:    "inner class collides with generated controller",
			source:  "extends RefCounted\n\nclass AddMethod:\n\tpass\n\nfunc add(item: int) -> bool:\n\treturn true\n",
			message: "member AddMethod collides with a generated controller",
		},
		{
			name:    "Pascal case clash between methods",
			source:  "extends RefCounted\n\nfunc load_counts() -> int:\n\treturn 1\n\nfunc loadCounts() -> int:\n\treturn 2\n",
			message: "methods loadCounts and load_counts both generate controller LoadCountsMethod",
		},
		{
			name:    "parameter named gdtest",
			source:  "extends RefCounted\n\nfunc add(gdtest: int) -> bool:\n\treturn true\n",
			message: "parameter gdtest collides with generated double locals",
		},
		{
			name:    "parameter with gdtest prefix",
			source:  "extends RefCounted\n\nfunc add(gdtest_value: int) -> bool:\n\treturn true\n",
			message: "parameter gdtest_value collides with generated double locals",
		},
		{
			name:    "method named like an Object method",
			source:  "extends RefCounted\n\nfunc get(key: String) -> Variant:\n\treturn null\n",
			message: "method get collides with an Object or RefCounted method",
		},
		{
			name:    "constructor parameter named context",
			source:  "extends RefCounted\n\nfunc _init(context: int) -> void:\n\tpass\n",
			message: "constructor parameter context collides with the generated context parameter",
		},
		{
			name:    "constructor parameter with gdtest prefix",
			source:  "extends RefCounted\n\nfunc _init(gdtest_seed: int) -> void:\n\tpass\n",
			message: "parameter gdtest_seed collides with generated double locals",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diagnostics := analyzeEntry(t, map[string]string{"src/source.gd": test.source}, entryFor("source.gd"))
			assert.Contains(t, messagesOf(diagnostics), test.message)
		})
	}
}

func TestAnalyzeNearestDeclarationDecidesDoubledMethodNotNames(t *testing.T) {
	t.Parallel()

	model, diagnostics := analyzeEntry(t, map[string]string{
		"src/base.gd":  "extends RefCounted\n\nfunc load_data(value: int) -> int:\n\treturn value\n",
		"src/child.gd": "extends \"res://src/base.gd\"\n\nfunc load_data(value: int) -> int:\n\treturn value + 1\n",
	}, entryFor("child.gd"))
	require.Empty(t, diagnostics)
	require.Len(t, model.Methods, 1)
	assert.Equal(t, "load_data", model.Methods[0].Name)
}

func TestAnalyzeReportsEveryDiagnosticInSourceOrder(t *testing.T) {
	t.Parallel()

	_, diagnostics := analyzeEntry(t, map[string]string{
		"src/source.gd": "extends RefCounted\n\nfunc first(item) -> bool:\n\treturn true\n\nfunc second() -> Node.ProcessMode:\n\treturn 0\n",
	}, entryFor("source.gd"))
	require.Len(t, diagnostics, 2)
	assert.Equal(t, 3, diagnostics[0].Line)
	assert.Equal(t, 6, diagnostics[1].Line)
}

func TestAnalyzeIsDeterministicAcrossRootPaths(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"src/base.gd":            "class_name Base\nextends RefCounted\n\nfunc zeta(value: int = 1) -> int:\n\treturn value\n",
		"src/child.gd":           "class_name Child\nextends Base\n\nfunc alpha() -> bool:\n\treturn true\n",
		"src/other/unrelated.gd": "func (:\n",
	}
	entry := entryFor("child.gd")
	first, _ := Analyze(mustIndex(t, writeProject(t, files)), entry)
	second, _ := Analyze(mustIndex(t, writeProject(t, files)), entry)
	assert.Equal(t, first, second)
}

func mustIndex(t *testing.T, root string) *Index {
	t.Helper()
	index, diagnostics := BuildIndex(root)
	require.NotNil(t, index)
	require.Len(t, diagnostics, 1, "unrelated parse failure is reported by the index, not the entry")
	return index
}
