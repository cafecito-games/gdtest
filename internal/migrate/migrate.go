// Package migrate performs syntax-aware source migrations into gdtest.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
)

// Mode controls whether safe edits are reported or atomically published.
type Mode string

const (
	Check Mode = "check"
	Write Mode = "write"
)

// Severity distinguishes planned edits from required manual work.
type Severity string

const (
	Info    Severity = "info"
	Warning Severity = "warning"
)

// Finding is one deterministic source-located migration result.
type Finding struct {
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Planned  bool     `json:"planned,omitempty"`
}

// FileResult is a complete, non-destructive transformation of one source.
type FileResult struct {
	Path     string    `json:"path"`
	Original []byte    `json:"-"`
	Output   []byte    `json:"-"`
	Changed  bool      `json:"changed"`
	Findings []Finding `json:"findings,omitempty"`
}

// Report summarizes a check or write operation.
type Report struct {
	Mode     Mode         `json:"mode"`
	Files    []FileResult `json:"files"`
	Findings []Finding    `json:"findings,omitempty"`
	Changed  int          `json:"changed"`
}

// Run prepares every transformation before publishing any file. Write mode
// uses same-directory temporary files and atomic rename for each changed file.
func Run(ctx context.Context, root string, paths []string, mode Mode) (Report, error) {
	if mode != Check && mode != Write {
		return Report{}, fmt.Errorf("migration mode %q must be check or write", mode)
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Report{}, err
	}
	ordered := append([]string(nil), paths...)
	for index, name := range ordered {
		ordered[index], err = normalizePath(name)
		if err != nil {
			return Report{}, err
		}
	}
	sort.Strings(ordered)
	ordered = compact(ordered)
	report := Report{Mode: mode, Files: make([]FileResult, 0, len(ordered))}
	for _, name := range ordered {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		filename := filepath.Join(canonical, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(filename)
		if err != nil {
			return Report{}, fmt.Errorf("resolve migration source %s: %w", name, err)
		}
		if !withinRoot(canonical, resolved) {
			return Report{}, fmt.Errorf("migration source %s resolves outside project root", name)
		}
		source, err := os.ReadFile(resolved)
		if err != nil {
			return Report{}, fmt.Errorf("read migration source %s: %w", name, err)
		}
		result, err := Transform(name, source)
		if err != nil {
			return Report{}, err
		}
		report.Files = append(report.Files, result)
		report.Findings = append(report.Findings, result.Findings...)
		if result.Changed {
			report.Changed++
		}
	}
	if mode == Write {
		for _, file := range report.Files {
			if !file.Changed {
				continue
			}
			if err := atomicWrite(filepath.Join(canonical, filepath.FromSlash(file.Path)), file.Output); err != nil {
				return Report{}, fmt.Errorf("write migrated source %s: %w", file.Path, err)
			}
		}
	}
	return report, nil
}

// Transform parses one GDScript file and prepares non-overlapping edits.
func Transform(path string, source []byte) (FileResult, error) {
	path, err := normalizePath(path)
	if err != nil {
		return FileResult{}, err
	}
	tree, err := gdparser.ParseFile(path, source)
	if err != nil {
		return FileResult{}, fmt.Errorf("parse migration source %s: %w", path, err)
	}
	state := transformer{path: path, source: source, functions: make(map[string]*ast.FunctionDeclaration)}
	for _, statement := range tree.Statements {
		if function, ok := statement.(*ast.FunctionDeclaration); ok {
			state.functions[function.Name] = function
		}
	}
	state.directives(tree)
	state.hookTypes(tree)
	state.parameterProviders(tree)
	state.fluentAssertions(tree)
	state.utilitiesAndUnsupported(tree)
	if len(state.wrappers) > 0 {
		sort.Strings(state.wrappers)
		prefix := "\n\n"
		if len(source) == 0 || source[len(source)-1] != '\n' {
			prefix = "\n\n\n"
		}
		state.addEdit(edit{start: len(source), end: len(source), text: prefix + strings.Join(state.wrappers, "\n\n") + "\n"})
	}
	output, err := applyEdits(source, state.edits)
	if err != nil {
		return FileResult{}, fmt.Errorf("migrate %s: %w", path, err)
	}
	sort.SliceStable(state.findings, func(i, j int) bool {
		left, right := state.findings[i], state.findings[j]
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.Code < right.Code
	})
	return FileResult{
		Path: path, Original: append([]byte(nil), source...), Output: output,
		Changed: len(state.edits) > 0, Findings: state.findings,
	}, nil
}

type edit struct {
	start int
	end   int
	text  string
}

type transformer struct {
	path        string
	source      []byte
	functions   map[string]*ast.FunctionDeclaration
	edits       []edit
	findings    []Finding
	wrappers    []string
	unsupported map[string]struct{}
}

func (t *transformer) directives(tree *ast.File) {
	for _, statement := range tree.Statements {
		directive, ok := statement.(*ast.Directive)
		if !ok || directive.Name != "extends" {
			continue
		}
		identifier, ok := directive.Value.(*ast.Identifier)
		if ok && identifier.Name == "VestTest" {
			t.rewrite(identifier.Span(), "GDTest", "rewrite.base-class", "replace VestTest base class with GDTest")
			continue
		}
		if ok && identifier.Name == "VestTestMixin" {
			t.manual(identifier.Span(), "unsupported.mixin", "Vest test mixins have no gdtest v1 equivalent")
			continue
		}
		if literal, literalOK := directive.Value.(*ast.Literal); literalOK && strings.Contains(literal.Raw, "_generated-mixins") {
			t.manual(literal.Span(), "unsupported.mixin", "generated Vest mixin inheritance requires manual migration")
		}
	}
}

func (t *transformer) hookTypes(tree *ast.File) {
	for _, statement := range tree.Statements {
		function, ok := statement.(*ast.FunctionDeclaration)
		if !ok || len(function.Parameters) != 1 {
			continue
		}
		var replacement string
		switch function.Name {
		case "before_suite", "after_suite":
			if function.Parameters[0].Type == "VestDefs.Suite" {
				replacement = "GDTestSuiteInfo"
			}
		case "before_case", "after_case", "before_each", "after_each":
			if function.Parameters[0].Type == "VestDefs.Case" {
				replacement = "GDTestCaseInfo"
			}
		}
		if replacement != "" {
			t.rewrite(function.Parameters[0].TypeSpan, replacement, "rewrite.hook-type", "replace Vest lifecycle metadata type")
		}
	}
}

func (t *transformer) parameterProviders(tree *ast.File) {
	usedTargets := make(map[string]bool)
	for _, statement := range tree.Statements {
		function, ok := statement.(*ast.FunctionDeclaration)
		if !ok || !strings.HasPrefix(function.Name, "test_") || len(function.Parameters) == 0 {
			continue
		}
		parameter := function.Parameters[len(function.Parameters)-1]
		literal, ok := parameter.Default.(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral || !strings.HasPrefix(parameter.Name, "_") {
			continue
		}
		provider, ok := stringLiteral(literal)
		if !ok || provider == "" {
			t.manual(parameter.Span(), "unsupported.parameter-provider", "parameter provider name is not a simple string literal")
			continue
		}
		providerFunction := t.functions[provider]
		if providerFunction == nil || len(providerFunction.Parameters) != 0 {
			t.manual(parameter.Span(), "unsupported.parameter-provider", fmt.Sprintf("provider %q is missing or requires arguments", provider))
			continue
		}
		target := strings.TrimSuffix(function.Name, "__only") + "__cases"
		if existing := t.functions[target]; existing != nil && existing != providerFunction {
			t.manual(parameter.Span(), "unsupported.parameter-provider", fmt.Sprintf("target provider %q already exists", target))
			continue
		}
		start := parameter.Span().Start.Offset
		if len(function.Parameters) > 1 {
			previous := function.Parameters[len(function.Parameters)-2].Span().End.Offset
			comma := strings.LastIndexByte(string(t.source[previous:start]), ',')
			if comma < 0 {
				t.manual(parameter.Span(), "unsupported.parameter-provider", "cannot locate provider parameter separator")
				continue
			}
			start = previous + comma
		}
		if !t.addEdit(edit{start: start, end: parameter.Span().End.Offset}) {
			continue
		}
		t.plan(parameter.Span(), "rewrite.parameter-provider", fmt.Sprintf("use %s as the parameter provider", target))
		if target != provider && !usedTargets[target] {
			returnType := ""
			if providerReturnTypeAllowed(providerFunction.ReturnType) && providerFunction.ReturnType != "" {
				returnType = " -> " + providerFunction.ReturnType
			}
			t.wrappers = append(t.wrappers, fmt.Sprintf(
				"## Supplies parameter rows for `%s`.\n# gdkit:ignore = function-name\nfunc %s()%s:\n\treturn %s()",
				function.Name, target, returnType, provider,
			))
			usedTargets[target] = true
		}
	}
}

func providerReturnTypeAllowed(value string) bool {
	return value == "" || value == "Array" || value == "Dictionary" ||
		strings.HasPrefix(value, "Array[") || strings.HasPrefix(value, "Dictionary[")
}

func (t *transformer) fluentAssertions(tree *ast.File) {
	ast.Inspect(tree, func(node ast.Node) bool {
		statement, ok := node.(*ast.ExpressionStatement)
		if !ok {
			return true
		}
		chain, ok := parseFluent(statement.Expression)
		if !ok {
			return true
		}
		if len(chain.matchers) != 1 {
			t.manual(statement.Expression.Span(), "unsupported.fluent-assertion", "fluent assertion chains with multiple matchers require manual conversion")
			return false
		}
		matcher := chain.matchers[0]
		target, arguments, ok := directMatcher(matcher.name, chain.actual, matcher.arguments)
		if !ok {
			t.manual(matcher.span, "unsupported.fluent-assertion", fmt.Sprintf("fluent matcher %q has no safe direct rewrite", matcher.name))
			return false
		}
		parts := make([]string, 0, len(arguments))
		for _, argument := range arguments {
			parts = append(parts, t.text(argument.Span()))
		}
		t.rewrite(statement.Expression.Span(), target+"("+strings.Join(parts, ", ")+")", "rewrite.fluent-assertion", "replace simple fluent assertion with direct assertion")
		return false
	})
}

func (t *transformer) utilitiesAndUnsupported(tree *ast.File) {
	if t.unsupported == nil {
		t.unsupported = make(map[string]struct{})
	}
	ast.Inspect(tree, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpression:
			name := callName(value.Callee)
			if member, ok := value.Callee.(*ast.MemberExpression); ok {
				if object, ok := member.Object.(*ast.Identifier); ok && object.Name == "Vest" {
					switch member.Property {
					case "message", "get_tree", "sleep", "until":
						t.rewrite(member.Span(), member.Property, "rewrite.utility", "remove Vest singleton qualifier")
					case "timeout":
						t.manualOnce(value.Span(), "unsupported.timeout", "Vest.timeout has no gdtest v1 equivalent")
					}
				}
			}
			switch name {
			case "mock", "when", "get_calls_of":
				t.manualOnce(value.Span(), "unsupported.runtime-mock", "runtime-generated mocks have no gdtest v1 equivalent")
			case "benchmark":
				t.manualOnce(value.Span(), "unsupported.benchmark", "benchmarks have no gdtest v1 equivalent")
			case "capture_signal", "get_signal_emissions":
				t.manualOnce(value.Span(), "unsupported.signal-capture", "signal capture has no gdtest v1 equivalent")
			}
		case *ast.Identifier:
			switch value.Name {
			case "on_begin", "on_finish", "on_suite_begin", "on_suite_finish", "on_case_begin", "on_case_finish", "on_partial_result", "on_event":
				t.manualOnce(value.Span(), "unsupported.lifecycle-signal", "Vest lifecycle signals require manual hook migration")
			}
		case *ast.MemberExpression:
			object, ok := value.Object.(*ast.Identifier)
			if !ok {
				break
			}
			switch object.Name {
			case "Vest":
				switch value.Property {
				case "message", "get_tree", "sleep", "until", "timeout":
				default:
					t.manualOnce(value.Span(), "unsupported.vest-singleton", "Vest runner, settings, and traversal helpers are not gdtest v1 test APIs")
				}
			case "VestDefs", "VestResult":
				t.manualOnce(value.Span(), "unsupported.mutable-definition", "Vest mutable suite and result definitions have no gdtest v1 equivalent")
			}
		case *ast.VariableDeclaration:
			if strings.HasPrefix(value.Type, "VestDefs.") || strings.HasPrefix(value.Type, "VestResult.") {
				t.manualOnce(value.TypeSpan, "unsupported.mutable-definition", "Vest mutable suite and result definitions have no gdtest v1 equivalent")
			}
		}
		return true
	})
}

type fluentChain struct {
	actual   ast.Expression
	matchers []fluentMatcher
}

type fluentMatcher struct {
	name      string
	arguments []ast.Expression
	span      token.Span
}

func parseFluent(expression ast.Expression) (fluentChain, bool) {
	call, ok := expression.(*ast.CallExpression)
	if !ok {
		return fluentChain{}, false
	}
	if identifier, ok := call.Callee.(*ast.Identifier); ok && identifier.Name == "assert_that" {
		if len(call.Arguments) != 1 {
			return fluentChain{}, true
		}
		return fluentChain{actual: call.Arguments[0]}, true
	}
	member, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return fluentChain{}, false
	}
	chain, ok := parseFluent(member.Object)
	if !ok {
		return fluentChain{}, false
	}
	chain.matchers = append(chain.matchers, fluentMatcher{name: member.Property, arguments: call.Arguments, span: call.Span()})
	return chain, true
}

func directMatcher(name string, actual ast.Expression, arguments []ast.Expression) (string, []ast.Expression, bool) {
	zero := map[string]string{
		"is_null": "expect_null", "is_not_null": "expect_not_null",
		"is_empty": "expect_empty", "is_not_empty": "expect_not_empty",
		"is_true": "expect_true", "is_false": "expect_false",
	}
	if target := zero[name]; target != "" && len(arguments) <= 1 {
		return target, append([]ast.Expression{actual}, arguments...), true
	}
	one := map[string]string{
		"is_equal_to": "expect_equal", "is_not_equal_to": "expect_not_equal",
		"is_same_as": "expect_same", "is_not_same_as": "expect_not_same",
		"contains": "expect_contains", "does_not_contain": "expect_does_not_contain",
	}
	if target := one[name]; target != "" && (len(arguments) == 1 || len(arguments) == 2) {
		return target, append([]ast.Expression{actual}, arguments...), true
	}
	return "", nil, false
}

func callName(expression ast.Expression) string {
	switch value := expression.(type) {
	case *ast.Identifier:
		return value.Name
	case *ast.MemberExpression:
		return value.Property
	default:
		return ""
	}
}

func (t *transformer) rewrite(span token.Span, replacement, code, message string) {
	if t.addEdit(edit{start: span.Start.Offset, end: span.End.Offset, text: replacement}) {
		t.plan(span, code, message)
	}
}

func (t *transformer) addEdit(value edit) bool {
	for _, existing := range t.edits {
		if value.start < existing.end && existing.start < value.end {
			t.manual(token.Span{Start: position(t.source, value.start), End: position(t.source, value.end)}, "unsupported.overlapping-rewrite", "overlapping safe rewrites require manual migration")
			return false
		}
	}
	t.edits = append(t.edits, value)
	return true
}

func (t *transformer) plan(span token.Span, code, message string) {
	t.findings = append(t.findings, Finding{Path: t.path, Line: span.Start.Line, Column: span.Start.Column, Severity: Info, Code: code, Message: message, Planned: true})
}

func (t *transformer) manual(span token.Span, code, message string) {
	t.findings = append(t.findings, Finding{Path: t.path, Line: span.Start.Line, Column: span.Start.Column, Severity: Warning, Code: code, Message: message})
}

func (t *transformer) manualOnce(span token.Span, code, message string) {
	key := fmt.Sprintf("%s:%d", code, span.Start.Offset)
	if _, exists := t.unsupported[key]; exists {
		return
	}
	t.unsupported[key] = struct{}{}
	t.manual(span, code, message)
}

func (t *transformer) text(span token.Span) string {
	if span.Start.Offset < 0 || span.End.Offset < span.Start.Offset || span.End.Offset > len(t.source) {
		return ""
	}
	return string(t.source[span.Start.Offset:span.End.Offset])
}

func applyEdits(source []byte, edits []edit) ([]byte, error) {
	ordered := append([]edit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].start != ordered[j].start {
			return ordered[i].start > ordered[j].start
		}
		return ordered[i].end > ordered[j].end
	})
	output := append([]byte(nil), source...)
	previous := len(source) + 1
	for _, value := range ordered {
		if value.start < 0 || value.end < value.start || value.end > len(source) || value.end > previous {
			return nil, errors.New("invalid or overlapping source edits")
		}
		output = append(output[:value.start], append([]byte(value.text), output[value.end:]...)...)
		previous = value.start
	}
	return output, nil
}

func stringLiteral(value *ast.Literal) (string, bool) {
	raw := value.Raw
	if value.RawPrefix && strings.HasPrefix(raw, "r") {
		raw = raw[1:]
	}
	if value.Triple || len(raw) < 2 {
		return "", false
	}
	if value.RawPrefix {
		return raw[1 : len(raw)-1], true
	}
	if raw[0] == '\'' {
		body := raw[1 : len(raw)-1]
		body = strings.ReplaceAll(body, `\`, `\\`)
		body = strings.ReplaceAll(body, `"`, `\"`)
		body = strings.ReplaceAll(body, `\'`, `'`)
		raw = `"` + body + `"`
	}
	decoded, err := strconv.Unquote(raw)
	return decoded, err == nil
}

func atomicWrite(filename string, data []byte) error {
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".gdtest-migrate-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, filename)
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolute migration root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve migration root: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func normalizePath(name string) (string, error) {
	normalized := filepath.ToSlash(filepath.Clean(name))
	normalized = strings.TrimPrefix(normalized, "./")
	if normalized == "" || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || filepath.IsAbs(name) {
		return "", fmt.Errorf("migration path %q must be project-relative", name)
	}
	return normalized, nil
}

func withinRoot(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func compact(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func position(source []byte, offset int) token.Position {
	line, column := 1, 1
	for index := 0; index < offset && index < len(source); index++ {
		if source[index] == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return token.Position{Offset: offset, Line: line, Column: column}
}
