package doubles

import (
	"fmt"
	"sort"

	"github.com/cafecito-games/gdparser/token"
)

// Diagnostic is a failure that stops generation. Path is project-relative, and
// Line and Column are one-based source positions, or zero when the failure has
// no source location.
type Diagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
}

// String formats the diagnostic as path:line:column: message.
func (diagnostic Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%d: %s", diagnostic.Path, diagnostic.Line, diagnostic.Column, diagnostic.Message)
}

// SortDiagnostics orders diagnostics by path, line, column, and message so that
// output does not depend on traversal order.
func SortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(left, right int) bool {
		a, b := diagnostics[left], diagnostics[right]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Message < b.Message
	})
}

func spanDiagnostic(path string, span token.Span, message string) Diagnostic {
	return Diagnostic{Path: path, Line: span.Start.Line, Column: span.Start.Column, Message: message}
}
