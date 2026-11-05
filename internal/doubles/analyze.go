package doubles

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
	"github.com/cafecito-games/gdtest/internal/config"
)

// ReturnCheck is the runtime check a generated method applies to a configured
// answer, derived from the method's declared return type.
type ReturnCheck int

const (
	// ReturnCheckUnsupported is the zero value. Analyze never returns it for a
	// method it accepted.
	ReturnCheckUnsupported ReturnCheck = iota
	// ReturnCheckVoid marks a method that returns nothing.
	ReturnCheckVoid
	// ReturnCheckAny accepts any value, for a Variant return type.
	ReturnCheckAny
	// ReturnCheckBuiltin accepts a value that is an instance of a builtin type.
	ReturnCheckBuiltin
	// ReturnCheckTypedArray accepts an Array that has the declared element type.
	ReturnCheckTypedArray
	// ReturnCheckTypedDictionary accepts a Dictionary that has the declared key and value types.
	ReturnCheckTypedDictionary
	// ReturnCheckEnum accepts an int, because enum values are integers.
	ReturnCheckEnum
	// ReturnCheckObject accepts null or an instance of the declared class.
	ReturnCheckObject
)

// Parameter is one parameter of a doubled method or constructor. Type is the
// declared type as gdparser prints it. Default is the default value copied
// verbatim from the declaring script, or empty when there is no default.
type Parameter struct {
	Name    string
	Type    string
	Default string
}

// Constructor describes the constructor of a generated double.
type Constructor struct {
	Parameters []Parameter
	// HasInit reports that some script in the chain declares _init. Without one,
	// the generated constructor must not call super.
	HasInit bool
}

// Method describes one doubled method.
type Method struct {
	Name           string
	ControllerName string
	Parameters     []Parameter
	ReturnType     string
	ReturnCheck    ReturnCheck
	// ExpectedType is the type name that a generated check reports in its
	// diagnostic. It is empty for void and Variant returns.
	ExpectedType string
	Async        bool
	Abstract     bool
	// DeclaringPath is the project-relative path of the nearest declaration.
	DeclaringPath string
}

// Model is the analyzed signature of one generated double, ready to render.
type Model struct {
	SourceResource  string
	OutputResource  string
	ClassName       string
	SourceClassName string
	Constructor     Constructor
	// Methods are sorted by name in byte order.
	Methods []Method
}

// candidate is one top-level function declaration of a script in the chain.
type candidate struct {
	declaration *ast.FunctionDeclaration
	owner       *script
}

// analysis carries the chain and the diagnostics of one Analyze call.
type analysis struct {
	index       *Index
	chain       []*script
	enumNames   map[string]bool
	diagnostics []Diagnostic
}

// Analyze derives the typed model of the double described by entry. It returns
// diagnostics for every construct the generator cannot prove safe. When any
// diagnostic is returned, the model is incomplete and must not be rendered.
// index must come from BuildIndex, and BuildIndex must have returned no diagnostics.
func Analyze(index *Index, entry config.TestDouble) (Model, []Diagnostic) {
	diagnostics := index.checkEntryPaths(entry)
	diagnostics = append(diagnostics, index.checkClassName(entry)...)
	model := Model{
		SourceResource: resourcePrefix + entry.Source,
		OutputResource: resourcePrefix + entry.Output,
		ClassName:      entry.ClassName,
	}
	source := index.scripts[entry.Source]
	if source == nil {
		message := "source script does not exist in the project"
		if index.failed[entry.Source] {
			message = "source script failed to parse"
		}
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Source, Message: message})
		SortDiagnostics(diagnostics)
		return model, diagnostics
	}
	model.SourceClassName = source.className
	chain, chainDiagnostics := index.chain(source)
	if len(chainDiagnostics) > 0 {
		diagnostics = append(diagnostics, chainDiagnostics...)
		SortDiagnostics(diagnostics)
		return model, diagnostics
	}

	state := &analysis{index: index, chain: chain, enumNames: make(map[string]bool)}
	for _, owner := range chain {
		maps.Copy(state.enumNames, owner.enums)
	}
	methods, controllers := state.buildMethods()
	constructor := state.buildConstructor()
	state.checkMemberNames(controllers)

	model.Constructor = constructor
	model.Methods = methods
	diagnostics = append(diagnostics, state.diagnostics...)
	SortDiagnostics(diagnostics)
	return model, diagnostics
}

// checkClassName reports a configured class_name that a project script which is
// not a generated double already declares. Generated doubles never appear in the
// class map, so a previous run's output, including one at a renamed path, never
// blocks the class_name.
func (index *Index) checkClassName(entry config.TestDouble) []Diagnostic {
	if entry.ClassName == "" {
		return nil
	}
	existing, declared := index.classes[entry.ClassName]
	if !declared {
		return nil
	}
	return []Diagnostic{{
		Path:    config.Filename,
		Message: "test_doubles class_name " + entry.ClassName + " is already declared by " + existing.path,
	}}
}

// buildMethods returns the doubled methods in name order, plus the controller
// class names they generate, keyed by name.
func (state *analysis) buildMethods() ([]Method, map[string]string) {
	candidates := make(map[string][]candidate)
	private := make(map[string]bool)
	for _, owner := range state.chain {
		for _, function := range owner.functions {
			if strings.HasPrefix(function.Name, "_") {
				// Private methods are inherited unchanged, so the nearest declaration
				// must be concrete; otherwise the double would be uninstantiable.
				if !private[function.Name] && function.Abstract {
					state.report(owner.path, function.NameSpan, fmt.Sprintf(
						"abstract method %s cannot be doubled because methods starting with _ are not generated", function.Name))
				}
				private[function.Name] = true
				continue
			}
			candidates[function.Name] = append(candidates[function.Name], candidate{declaration: function, owner: owner})
		}
	}
	controllers := make(map[string]string)
	var methods []Method
	for _, name := range slices.Sorted(maps.Keys(candidates)) {
		nearest := candidates[name][0]
		if nearest.declaration.Static {
			continue
		}
		if slices.Contains(objectMethodNames, name) {
			state.report(nearest.owner.path, nearest.declaration.NameSpan,
				fmt.Sprintf("method %s collides with an Object or RefCounted method", name))
			continue
		}
		method, ok := state.buildMethod(nearest, candidates[name])
		if !ok {
			continue
		}
		if other, exists := controllers[method.ControllerName]; exists {
			state.report(nearest.owner.path, nearest.declaration.NameSpan, fmt.Sprintf(
				"methods %s and %s both generate controller %s", other, name, method.ControllerName))
			continue
		}
		controllers[method.ControllerName] = name
		methods = append(methods, method)
	}
	return methods, controllers
}

func (state *analysis) buildMethod(nearest candidate, overrides []candidate) (Method, bool) {
	function := nearest.declaration
	valid := state.checkParameters(nearest.owner, function.Parameters)
	check, expected := ReturnCheckUnsupported, ""
	if function.ReturnType == "" {
		state.report(nearest.owner.path, function.NameSpan, fmt.Sprintf("method %s has no return type", function.Name))
		valid = false
	} else {
		check, expected = state.classifyReturn(function.ReturnType)
		if check == ReturnCheckUnsupported {
			state.report(nearest.owner.path, function.ReturnTypeSpan, fmt.Sprintf(
				"return type %s of method %s is not supported by generated doubles", function.ReturnType, function.Name))
			valid = false
		}
	}
	if !valid {
		return Method{}, false
	}
	parameters := state.parameters(nearest.owner, function.Parameters)
	async := false
	for _, override := range overrides {
		async = async || awaits(override.declaration)
	}
	return Method{
		Name:           function.Name,
		ControllerName: pascalCase(function.Name) + controllerSuffix,
		Parameters:     parameters,
		ReturnType:     function.ReturnType,
		ReturnCheck:    check,
		ExpectedType:   expected,
		Async:          async,
		Abstract:       function.Abstract,
		DeclaringPath:  nearest.owner.path,
	}, true
}

func (state *analysis) buildConstructor() Constructor {
	var nearest *candidate
	constructor := Constructor{}
	for _, owner := range state.chain {
		for _, function := range owner.functions {
			if function.Name != "_init" {
				continue
			}
			constructor.HasInit = true
			if nearest == nil {
				nearest = &candidate{declaration: function, owner: owner}
			}
		}
	}
	if nearest == nil {
		return constructor
	}
	state.checkParameters(nearest.owner, nearest.declaration.Parameters)
	for _, parameter := range nearest.declaration.Parameters {
		if parameter.Name == constructorContextParameter {
			state.report(nearest.owner.path, parameter.NameSpan,
				fmt.Sprintf("constructor parameter %s collides with the generated context parameter", parameter.Name))
		}
	}
	constructor.Parameters = state.parameters(nearest.owner, nearest.declaration.Parameters)
	return constructor
}

// checkParameters reports each parameter whose type cannot be proven or whose
// name would shadow a generated member, and returns whether all parameters passed.
func (state *analysis) checkParameters(owner *script, parameters []ast.Parameter) bool {
	valid := true
	for _, parameter := range parameters {
		switch {
		case parameter.Variadic:
			state.report(owner.path, parameter.NameSpan,
				fmt.Sprintf("parameter %s is variadic and cannot be forwarded", parameter.Name))
			valid = false
		case parameter.Inferred:
			state.report(owner.path, parameter.NameSpan,
				fmt.Sprintf("parameter %s is inferred with := and must declare its type", parameter.Name))
			valid = false
		case parameter.Type == "":
			state.report(owner.path, parameter.NameSpan,
				fmt.Sprintf("parameter %s is untyped and must declare its type", parameter.Name))
			valid = false
		}
		if parameter.Name == reservedParameterName || strings.HasPrefix(parameter.Name, reservedParameterPrefix) {
			state.report(owner.path, parameter.NameSpan,
				fmt.Sprintf("parameter %s collides with generated double locals", parameter.Name))
			valid = false
		}
	}
	return valid
}

// parameters copies the parameters with their default values taken verbatim
// from the declaring script. A default that cannot be copied is reported.
func (state *analysis) parameters(owner *script, parameters []ast.Parameter) []Parameter {
	copied := make([]Parameter, 0, len(parameters))
	for _, parameter := range parameters {
		item := Parameter{Name: parameter.Name, Type: parameter.Type}
		if parameter.Default != nil {
			text, ok := sourceText(owner.source, parameter.Default.Span())
			if !ok {
				state.report(owner.path, parameter.NameSpan,
					fmt.Sprintf("default value of parameter %s cannot be copied", parameter.Name))
			}
			item.Default = text
		}
		copied = append(copied, item)
	}
	return copied
}

func (state *analysis) checkMemberNames(controllers map[string]string) {
	for _, owner := range state.chain {
		for _, declared := range owner.members {
			_, collides := controllers[declared.name]
			switch {
			case slices.Contains(reservedMemberNames, declared.name):
				state.report(owner.path, declared.span,
					fmt.Sprintf("member %s is reserved by generated doubles", declared.name))
			case collides:
				state.report(owner.path, declared.span,
					fmt.Sprintf("member %s collides with a generated controller", declared.name))
			}
		}
	}
}

func (state *analysis) report(path string, span token.Span, message string) {
	state.diagnostics = append(state.diagnostics, spanDiagnostic(path, span, message))
}

// awaits reports whether the body of function contains an await outside any
// nested lambda.
func awaits(function *ast.FunctionDeclaration) bool {
	found := false
	for _, statement := range function.Body {
		ast.Inspect(statement, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.LambdaExpression:
				return false
			case *ast.UnaryExpression:
				found = found || typed.Operator == "await"
			}
			return !found
		})
	}
	return found
}

func sourceText(source []byte, span token.Span) (string, bool) {
	start, end := span.Start.Offset, span.End.Offset
	if start < 0 || end < start || end > len(source) {
		return "", false
	}
	return string(source[start:end]), true
}

// pascalCase converts a snake_case method name to PascalCase, for example
// load_counts to LoadCounts.
func pascalCase(name string) string {
	var builder strings.Builder
	for _, segment := range strings.Split(name, "_") {
		first, size := utf8.DecodeRuneInString(segment)
		if size == 0 {
			continue
		}
		builder.WriteRune(unicode.ToUpper(first))
		builder.WriteString(segment[size:])
	}
	return builder.String()
}
