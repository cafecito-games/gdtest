// Package dependency extracts and queries conservative GDScript dependencies.
package dependency

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// Analysis is the statically resolvable dependency fragment for one script.
// Dynamic is true when a resource-loading call could not be resolved to a
// literal; callers must widen affected-test selection in that case.
type Analysis struct {
	Paths   []string `json:"paths,omitempty"`
	Dynamic bool     `json:"dynamic,omitempty"`
}

// Analyze extracts load, preload, and ResourceLoader.load calls from tree.
func Analyze(tree *ast.File) Analysis {
	seen := make(map[string]struct{})
	dynamic := false
	ast.Inspect(tree, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok || !isResourceLoad(call.Callee) {
			return true
		}
		if len(call.Arguments) == 0 {
			dynamic = true
			return true
		}
		literal, ok := call.Arguments[0].(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral {
			dynamic = true
			return true
		}
		value, ok := stringValue(literal)
		if !ok {
			dynamic = true
			return true
		}
		if normalized, ok := normalizeResource(value); ok {
			seen[normalized] = struct{}{}
		} else {
			// uid:// resources and other schemes require project metadata to
			// resolve. Treating them as dynamic avoids false negatives.
			dynamic = true
		}
		return true
	})
	paths := make([]string, 0, len(seen))
	for dependencyPath := range seen {
		paths = append(paths, dependencyPath)
	}
	sort.Strings(paths)
	return Analysis{Paths: paths, Dynamic: dynamic}
}

func isResourceLoad(expression ast.Expression) bool {
	switch callee := expression.(type) {
	case *ast.Identifier:
		return callee.Name == "load" || callee.Name == "preload"
	case *ast.MemberExpression:
		object, ok := callee.Object.(*ast.Identifier)
		return ok && object.Name == "ResourceLoader" && callee.Property == "load"
	default:
		return false
	}
}

func stringValue(literal *ast.Literal) (string, bool) {
	raw := literal.Raw
	if literal.RawPrefix && len(raw) > 0 && raw[0] == 'r' {
		raw = raw[1:]
	}
	delimiterSize := 1
	if literal.Triple {
		delimiterSize = 3
	}
	if len(raw) < delimiterSize*2 {
		return "", false
	}
	value := raw[delimiterSize : len(raw)-delimiterSize]
	if literal.RawPrefix {
		return value, true
	}
	// strconv understands the escape sequences needed by ordinary resource
	// paths. Convert single-quoted GDScript spelling to a Go string first.
	if literal.Quote == '\'' {
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		value = strings.ReplaceAll(value, `\'`, `'`)
		raw = `"` + value + `"`
	}
	unquoted, err := strconv.Unquote(raw)
	return unquoted, err == nil
}

func normalizeResource(resource string) (string, bool) {
	if !strings.HasPrefix(resource, "res://") {
		return "", false
	}
	normalized := path.Clean(strings.TrimPrefix(resource, "res://"))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", false
	}
	return normalized, true
}

// Graph maps normalized project-relative files to their direct dependencies.
type Graph struct {
	edges   map[string][]string
	dynamic map[string]bool
}

// NewGraph creates an empty dependency graph.
func NewGraph() *Graph {
	return &Graph{edges: make(map[string][]string), dynamic: make(map[string]bool)}
}

// Set replaces one file's dependency fragment.
func (g *Graph) Set(filename string, analysis Analysis) {
	filename = normalizePath(filename)
	dependencies := append([]string(nil), analysis.Paths...)
	for index := range dependencies {
		dependencies[index] = normalizePath(dependencies[index])
	}
	sort.Strings(dependencies)
	g.edges[filename] = compact(dependencies)
	g.dynamic[filename] = analysis.Dynamic
}

// Dependencies returns a copy of a file's direct dependencies.
func (g *Graph) Dependencies(filename string) []string {
	return append([]string(nil), g.edges[normalizePath(filename)]...)
}

// DependsOn reports whether start transitively depends on target. Dynamic is
// true when an unresolved load was encountered anywhere in the traversed
// closure, requiring a conservative caller to consider the result affected.
func (g *Graph) DependsOn(start, target string) (depends bool, dynamic bool) {
	start, target = normalizePath(start), normalizePath(target)
	if start == target {
		return true, g.dynamic[start]
	}
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(current string) bool {
		if visited[current] {
			return false
		}
		visited[current] = true
		if g.dynamic[current] {
			dynamic = true
		}
		for _, next := range g.edges[current] {
			if next == target || visit(next) {
				return true
			}
		}
		return false
	}
	return visit(start), dynamic
}

func normalizePath(filename string) string {
	return path.Clean(strings.TrimPrefix(filepathSlash(filename), "./"))
}

func filepathSlash(filename string) string { return strings.ReplaceAll(filename, `\`, "/") }

func compact(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
