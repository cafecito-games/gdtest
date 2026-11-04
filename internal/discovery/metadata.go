package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
	"github.com/cafecito-games/gdtest/internal/dependency"
	"github.com/cafecito-games/gdtest/internal/model"
)

// Location identifies a declaration without embedding an absolute worktree path.
type Location struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Symbol describes a source-ordered method or hook.
type Symbol struct {
	Name     string   `json:"name"`
	Location Location `json:"location"`
}

// Provider describes a reserved parameter provider.
type Provider struct {
	Name       string   `json:"name"`
	TestMethod string   `json:"test_method"`
	ReturnType string   `json:"return_type,omitempty"`
	Location   Location `json:"location"`
}

// ScriptMetadata is the immutable static index fragment for one script.
type ScriptMetadata struct {
	Path                  string     `json:"path"`
	ClassName             string     `json:"class_name,omitempty"`
	BaseClass             string     `json:"base_class,omitempty"`
	ExtendsGDTest         bool       `json:"extends_gdtest"`
	Methods               []Symbol   `json:"methods,omitempty"`
	Hooks                 []Symbol   `json:"hooks,omitempty"`
	Providers             []Provider `json:"providers,omitempty"`
	Dependencies          []string   `json:"dependencies,omitempty"`
	HasDynamicDependency  bool       `json:"has_dynamic_dependency,omitempty"`
	HasFocus              bool       `json:"has_focus,omitempty"`
	StructuralFingerprint string     `json:"structural_fingerprint"`
	ContentFingerprint    string     `json:"content_fingerprint"`
}

type parsedScript struct {
	metadata ScriptMetadata
	plan     model.FilePlan
	tree     *ast.File
	source   []byte
	basePath string
	baseName string
}

var hookNames = map[string]bool{
	"before_all": true, "after_all": true,
	"before_suite": true, "after_suite": true,
	"before_case": true, "after_case": true,
	"before_each": true, "after_each": true,
}

func inspectScript(relative string, source []byte, tree *ast.File, enforceContracts bool) (parsedScript, error) {
	cleanPath := normalizeRelative(relative)
	script := parsedScript{
		metadata: ScriptMetadata{Path: cleanPath},
		plan:     model.FilePlan{Path: cleanPath},
		tree:     tree,
		source:   source,
	}
	providers := make(map[string]*ast.FunctionDeclaration)
	functions := make([]*ast.FunctionDeclaration, 0)
	declaredHooks := make(map[string]*ast.FunctionDeclaration)
	var structure strings.Builder

	for _, statement := range tree.Statements {
		switch declaration := statement.(type) {
		case *ast.Directive:
			inspectDirective(&script, declaration)
		case *ast.FunctionDeclaration:
			functions = append(functions, declaration)
			structure.WriteString(functionSignature(declaration))
			structure.WriteByte('\n')
			if strings.HasSuffix(declaration.Name, "__cases") {
				providers[declaration.Name] = declaration
			}
		case *ast.SignalDeclaration:
			structure.WriteString("signal:")
			structure.WriteString(declaration.Name)
			structure.WriteString(parametersSignature(declaration.Parameters))
			structure.WriteByte('\n')
		case *ast.ClassDeclaration:
			structure.WriteString("class:")
			structure.WriteString(declaration.Name)
			structure.WriteByte(':')
			structure.WriteString(declaration.Extends)
			structure.WriteByte('\n')
		}
	}
	structure.WriteString("class_name:")
	structure.WriteString(script.metadata.ClassName)
	structure.WriteString("\nextends:")
	structure.WriteString(script.metadata.BaseClass)
	structure.WriteByte('\n')

	for _, function := range functions {
		location := sourceLocation(cleanPath, function.NameSpan)
		script.metadata.Methods = append(script.metadata.Methods, Symbol{Name: function.Name, Location: location})
		if function.Name == "suite" && enforceContracts {
			if len(function.Parameters) != 0 {
				return parsedScript{}, fmt.Errorf(
					"discover %s:%d:%d: suite must take no arguments",
					cleanPath, function.NameSpan.Start.Line, function.NameSpan.Start.Column,
				)
			}
			if function.ReturnType != "" && function.ReturnType != "void" {
				return parsedScript{}, fmt.Errorf(
					"discover %s:%d:%d: suite must return void, got %s",
					cleanPath, function.NameSpan.Start.Line, function.NameSpan.Start.Column,
					function.ReturnType,
				)
			}
		}
		if hookNames[function.Name] {
			if err := validateHook(cleanPath, function); enforceContracts && err != nil {
				return parsedScript{}, err
			}
			declaredHooks[function.Name] = function
			script.metadata.Hooks = append(script.metadata.Hooks, Symbol{Name: function.Name, Location: location})
		}
		if strings.HasSuffix(function.Name, "__cases") {
			logical := strings.TrimSuffix(function.Name, "__cases")
			if err := validateProvider(cleanPath, function); enforceContracts && err != nil {
				return parsedScript{}, err
			}
			script.metadata.Providers = append(script.metadata.Providers, Provider{
				Name: function.Name, TestMethod: logical, ReturnType: function.ReturnType, Location: location,
			})
			continue
		}
		if !strings.HasPrefix(function.Name, "test_") {
			continue
		}
		if enforceContracts && function.ReturnType != "" && function.ReturnType != "void" {
			return parsedScript{}, fmt.Errorf(
				"discover %s:%d:%d: test method %s must return void, got %s",
				cleanPath, function.NameSpan.Start.Line, function.NameSpan.Start.Column, function.Name, function.ReturnType,
			)
		}
		logical := strings.TrimSuffix(function.Name, "__only")
		if logical == "test_" {
			continue
		}
		required := 0
		for _, parameter := range function.Parameters {
			if parameter.Default == nil && !parameter.Variadic {
				required++
			}
		}
		if enforceContracts && required > 0 {
			provider := providers[logical+"__cases"]
			if provider == nil {
				return parsedScript{}, fmt.Errorf(
					"discover %s:%d:%d: %s has required parameters but no %s__cases provider",
					cleanPath, function.NameSpan.Start.Line, function.NameSpan.Start.Column, function.Name, logical,
				)
			}
		}
		focused := strings.HasSuffix(function.Name, "__only")
		script.metadata.HasFocus = script.metadata.HasFocus || focused
		script.plan.Cases = append(script.plan.Cases, model.CasePlan{
			ID: cleanPath + "::" + logical, Name: displayName(strings.TrimPrefix(logical, "test_")),
			MethodName: function.Name, Line: function.NameSpan.Start.Line, IsOnly: focused,
		})
	}
	for _, aliases := range [][2]string{{"before_case", "before_each"}, {"after_case", "after_each"}} {
		if declaredHooks[aliases[0]] != nil && declaredHooks[aliases[1]] != nil {
			if !enforceContracts {
				continue
			}
			location := declaredHooks[aliases[1]].NameSpan.Start
			return parsedScript{}, fmt.Errorf(
				"discover %s:%d:%d: hooks %s and %s are aliases and cannot both be defined",
				cleanPath, location.Line, location.Column, aliases[0], aliases[1],
			)
		}
	}

	dependencies := dependency.Analyze(tree)
	script.metadata.Dependencies = dependencies.Paths
	script.metadata.HasDynamicDependency = dependencies.Dynamic
	script.metadata.ContentFingerprint = fingerprint(source)
	script.metadata.StructuralFingerprint = fingerprint([]byte(structure.String()))
	return script, nil
}

func inspectDirective(script *parsedScript, directive *ast.Directive) {
	if directive.Name == "class_name" {
		if identifier, ok := directive.Value.(*ast.Identifier); ok {
			script.metadata.ClassName = identifier.Name
		}
		setBase(script, directive.Extends)
		return
	}
	if directive.Name == "extends" {
		setBase(script, directive.Value)
	}
}

func setBase(script *parsedScript, expression ast.Expression) {
	if expression == nil {
		return
	}
	if literal, ok := expression.(*ast.Literal); ok && literal.Kind == ast.StringLiteral {
		value := literalString(literal)
		script.metadata.BaseClass = value
		if strings.HasPrefix(value, "res://") {
			script.basePath = normalizeRelative(strings.TrimPrefix(value, "res://"))
		}
		return
	}
	script.baseName = expressionName(expression)
	script.metadata.BaseClass = script.baseName
}

func expressionName(expression ast.Expression) string {
	switch value := expression.(type) {
	case *ast.Identifier:
		return value.Name
	case *ast.MemberExpression:
		prefix := expressionName(value.Object)
		if prefix == "" {
			return value.Property
		}
		return prefix + "." + value.Property
	default:
		return ""
	}
}

func literalString(literal *ast.Literal) string {
	raw := literal.Raw
	if literal.RawPrefix && strings.HasPrefix(raw, "r") {
		raw = raw[1:]
	}
	delimiter := 1
	if literal.Triple {
		delimiter = 3
	}
	if len(raw) < delimiter*2 {
		return ""
	}
	return raw[delimiter : len(raw)-delimiter]
}

func validateProvider(filename string, function *ast.FunctionDeclaration) error {
	location := function.NameSpan.Start
	if len(function.Parameters) != 0 {
		return fmt.Errorf("discover %s:%d:%d: provider %s must take no arguments", filename, location.Line, location.Column, function.Name)
	}
	validReturn := function.ReturnType == "" || function.ReturnType == "Array" || function.ReturnType == "Dictionary" ||
		strings.HasPrefix(function.ReturnType, "Array[") || strings.HasPrefix(function.ReturnType, "Dictionary[")
	if !validReturn {
		return fmt.Errorf("discover %s:%d:%d: provider %s must return Array or Dictionary, got %s", filename, location.Line, location.Column, function.Name, function.ReturnType)
	}
	async := false
	for _, statement := range function.Body {
		ast.Inspect(statement, func(node ast.Node) bool {
			unary, ok := node.(*ast.UnaryExpression)
			if ok && unary.Operator == "await" {
				async = true
			}
			return true
		})
	}
	if async {
		return fmt.Errorf("discover %s:%d:%d: provider %s must be synchronous", filename, location.Line, location.Column, function.Name)
	}
	return nil
}

func validateHook(filename string, function *ast.FunctionDeclaration) error {
	wantParameters := 1
	if function.Name == "before_all" || function.Name == "after_all" {
		wantParameters = 0
	}
	location := function.NameSpan.Start
	if len(function.Parameters) != wantParameters {
		return fmt.Errorf(
			"discover %s:%d:%d: hook %s must take %d argument(s), got %d",
			filename, location.Line, location.Column, function.Name, wantParameters, len(function.Parameters),
		)
	}
	if function.ReturnType != "" && function.ReturnType != "void" {
		return fmt.Errorf(
			"discover %s:%d:%d: hook %s must return void, got %s",
			filename, location.Line, location.Column, function.Name, function.ReturnType,
		)
	}
	return nil
}

func functionSignature(function *ast.FunctionDeclaration) string {
	return fmt.Sprintf("func:%s:%t:%t%s->%s", function.Name, function.Static, function.Abstract, parametersSignature(function.Parameters), function.ReturnType)
}

func parametersSignature(parameters []ast.Parameter) string {
	var value strings.Builder
	value.WriteByte('(')
	for index, parameter := range parameters {
		if index > 0 {
			value.WriteByte(',')
		}
		value.WriteString(parameter.Name)
		value.WriteByte(':')
		value.WriteString(parameter.Type)
		if parameter.Default != nil {
			value.WriteString("=default")
		}
		if parameter.Variadic {
			value.WriteString("...")
		}
	}
	value.WriteByte(')')
	return value.String()
}

func resolveInheritance(scripts map[string]*parsedScript) {
	classes := make(map[string]string)
	ambiguous := make(map[string]bool)
	for filename, script := range scripts {
		name := script.metadata.ClassName
		if name == "" {
			continue
		}
		if _, exists := classes[name]; exists {
			ambiguous[name] = true
		} else {
			classes[name] = filename
		}
	}
	state := make(map[string]uint8)
	var resolve func(string) bool
	resolve = func(filename string) bool {
		script := scripts[filename]
		if script == nil {
			return false
		}
		if state[filename] == 2 {
			return script.metadata.ExtendsGDTest
		}
		if state[filename] == 1 {
			return false
		}
		state[filename] = 1
		base := ""
		switch {
		case script.baseName == "GDTest":
			script.metadata.ExtendsGDTest = true
		case script.basePath != "":
			base = script.basePath
		case script.baseName != "" && !ambiguous[script.baseName]:
			base = classes[script.baseName]
		}
		if base != "" {
			addDependency(&script.metadata, base)
			script.metadata.ExtendsGDTest = resolve(base)
		}
		state[filename] = 2
		return script.metadata.ExtendsGDTest
	}
	paths := make([]string, 0, len(scripts))
	for filename := range scripts {
		paths = append(paths, filename)
	}
	sort.Strings(paths)
	for _, filename := range paths {
		resolve(filename)
	}
}

func addDependency(metadata *ScriptMetadata, filename string) {
	filename = normalizeRelative(filename)
	for _, existing := range metadata.Dependencies {
		if existing == filename {
			return
		}
	}
	metadata.Dependencies = append(metadata.Dependencies, filename)
	sort.Strings(metadata.Dependencies)
}

func sourceLocation(filename string, span token.Span) Location {
	return Location{Path: filename, Line: span.Start.Line, Column: span.Start.Column}
}

func normalizeRelative(filename string) string {
	return path.Clean(strings.TrimPrefix(strings.ReplaceAll(filename, `\`, "/"), "./"))
}

func fingerprint(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
