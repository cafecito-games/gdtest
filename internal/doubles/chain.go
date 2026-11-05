package doubles

import (
	"fmt"
	"path"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// resolveBase returns the project script that current extends, or nil when the
// base is native. A script without an extends clause extends native RefCounted.
func (index *Index) resolveBase(current *script) (*script, []Diagnostic) {
	if current.extends == nil {
		return nil, nil
	}
	span := current.extends.Span()
	switch expression := current.extends.(type) {
	case *ast.Literal:
		if expression.Kind != ast.StringLiteral {
			return nil, []Diagnostic{spanDiagnostic(current.path, span, "base class must be a string path or identifier")}
		}
		target := literalString(expression)
		resolved := path.Join(path.Dir(current.path), target)
		if resourceTarget, isResource := strings.CutPrefix(target, resourcePrefix); isResource {
			resolved = path.Clean(resourceTarget)
		}
		if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
			return nil, []Diagnostic{spanDiagnostic(current.path, span,
				fmt.Sprintf("base class %q resolves outside the project", target))}
		}
		base := index.scripts[resolved]
		if base == nil {
			return nil, []Diagnostic{spanDiagnostic(current.path, span, missingBaseMessage(index, target, resolved))}
		}
		return base, nil
	case *ast.Identifier:
		return index.classes[expression.Name], nil
	default:
		return nil, []Diagnostic{spanDiagnostic(current.path, span,
			"base class must be a string path or a class_name identifier")}
	}
}

func missingBaseMessage(index *Index, target string, resolved string) string {
	if index.failed[resolved] {
		return fmt.Sprintf("base class %q failed to parse", target)
	}
	return fmt.Sprintf("base class %q does not exist in the project", target)
}

// chain returns the project scripts from source towards the root, nearest first.
// The walk stops at the first native base. A cycle or an unresolved base is
// returned as a diagnostic at the extends clause that caused it.
func (index *Index) chain(source *script) ([]*script, []Diagnostic) {
	chain := []*script{source}
	visited := map[string]bool{source.path: true}
	current := source
	for {
		base, diagnostics := index.resolveBase(current)
		if len(diagnostics) > 0 {
			return chain, diagnostics
		}
		if base == nil {
			return chain, nil
		}
		if visited[base.path] {
			return chain, []Diagnostic{spanDiagnostic(current.path, current.extends.Span(),
				"inheritance cycle through "+base.path)}
		}
		visited[base.path] = true
		chain = append(chain, base)
		current = base
	}
}
