package migrate

import (
	"strconv"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
)

const (
	signalCaptureCode  = "unsupported.signal-capture"
	signalCaptureUnset = ""
	signalCaptureUse   = "signal capture has no gdtest v1 equivalent"
	probeHelperName    = "watch_signal"
)

type signalCapture struct {
	call      *ast.CallExpression
	statement *ast.ExpressionStatement
	member    *ast.MemberExpression
	function  *ast.FunctionDeclaration
	text      string
}

type signalReadKind int

const (
	signalReadUnsupported signalReadKind = iota
	signalReadCount
	signalReadEmissions
	signalReadArgumentsAt
	signalReadNestedArgument
)

type signalRead struct {
	call     *ast.CallExpression
	node     ast.Expression
	text     string
	property string
	kind     signalReadKind
	indexes  []ast.Expression
}

type signalGroup struct {
	captures []*signalCapture
	reads    []*signalRead
	foreign  string
}

// signalGroupKey scopes a signal expression to the function that names it, so
// identical text in different tests refers to independent local bindings.
type signalGroupKey struct {
	scope *ast.FunctionDeclaration
	text  string
}

// signalCaptures converts unambiguous Vest signal captures inside test functions
// to case-scoped probes. A signal's capture and every read of it convert together
// or not at all, so a file never ends up with a probe and a dangling Vest read.
func (t *transformer) signalCaptures(tree *ast.File) {
	parents := parentNodes(tree)
	var captures []*signalCapture
	var reads []*signalRead
	ast.Inspect(tree, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok {
			return true
		}
		identifier, ok := call.Callee.(*ast.Identifier)
		if !ok {
			return true
		}
		switch identifier.Name {
		case "capture_signal":
			captures = append(captures, t.classifyCapture(call, parents))
		case "get_signal_emissions":
			reads = append(reads, t.classifyRead(call, parents))
		}
		return true
	})

	groups := make(map[signalGroupKey]*signalGroup)
	for _, capture := range captures {
		if capture.member == nil {
			continue
		}
		key := signalGroupKey{scope: enclosingFunction(tree, capture.call.Span()), text: capture.text}
		group := groupFor(groups, key)
		group.captures = append(group.captures, capture)
	}
	for _, read := range reads {
		key := signalGroupKey{scope: enclosingFunction(tree, read.call.Span()), text: read.text}
		if group, exists := groups[key]; exists {
			group.reads = append(group.reads, read)
			continue
		}
		for otherKey, group := range groups {
			sameProperty := read.property != "" && group.captures[0].member.Property == read.property
			if otherKey.text != read.text && !sameProperty {
				continue
			}
			if otherKey.scope == key.scope {
				group.foreign = "the signal is also read through another expression in this test"
			} else {
				group.foreign = "the signal is read outside the test that captures it"
			}
		}
	}

	scopeNames := make(map[*ast.FunctionDeclaration]map[string]struct{})
	for _, group := range groups {
		reason := t.signalGroupBlocker(group)
		var names map[string]struct{}
		if reason == signalCaptureUnset {
			function := group.captures[0].function
			cached, exists := scopeNames[function]
			if !exists {
				cached = declaredNames(tree, function)
				scopeNames[function] = cached
			}
			names = cached
			if _, taken := names[probeHelperName]; taken {
				reason = "the generated call " + probeHelperName + " would resolve to a user declaration"
			}
		}
		if reason != signalCaptureUnset {
			for _, capture := range group.captures {
				t.manualOnce(capture.call.Span(), signalCaptureCode, "signal capture requires manual migration: "+reason)
			}
			for _, read := range group.reads {
				t.manualOnce(read.node.Span(), signalCaptureCode, "signal read requires manual migration: "+reason)
			}
			continue
		}
		t.convertSignalGroup(group, names)
	}
}

func groupFor(groups map[signalGroupKey]*signalGroup, key signalGroupKey) *signalGroup {
	group, exists := groups[key]
	if !exists {
		group = &signalGroup{}
		groups[key] = group
	}
	return group
}

func enclosingFunction(tree *ast.File, span token.Span) *ast.FunctionDeclaration {
	for _, statement := range tree.Statements {
		if function, ok := statement.(*ast.FunctionDeclaration); ok && spanContains(function.Span(), span) {
			return function
		}
	}
	return nil
}

func (t *transformer) classifyCapture(call *ast.CallExpression, parents map[ast.Node]ast.Node) *signalCapture {
	capture := &signalCapture{call: call}
	if len(call.Arguments) == 0 {
		return capture
	}
	member, ok := call.Arguments[0].(*ast.MemberExpression)
	if !ok {
		return capture
	}
	if _, ok := member.Object.(*ast.Identifier); !ok {
		return capture
	}
	capture.member = member
	capture.text = t.text(member.Span())
	if statement, ok := parents[call].(*ast.ExpressionStatement); ok {
		capture.statement = statement
		if function, ok := parents[statement].(*ast.FunctionDeclaration); ok {
			capture.function = function
		}
	}
	return capture
}

func (t *transformer) classifyRead(call *ast.CallExpression, parents map[ast.Node]ast.Node) *signalRead {
	read := &signalRead{call: call, node: call}
	if len(call.Arguments) != 1 {
		return read
	}
	read.text = t.text(call.Arguments[0].Span())
	if member, ok := call.Arguments[0].(*ast.MemberExpression); ok {
		read.property = member.Property
	}
	if t.isEmptinessCheck(call, parents) {
		read.kind = signalReadEmissions
		return read
	}
	switch parent := parents[call].(type) {
	case *ast.MemberExpression:
		if parent.Object != ast.Expression(call) || parent.Property != "size" {
			return read
		}
		outer, ok := parents[parent].(*ast.CallExpression)
		if !ok || outer.Callee != ast.Expression(parent) || len(outer.Arguments) != 0 {
			return read
		}
		read.kind = signalReadCount
		read.node = outer
	case *ast.SubscriptExpression:
		if parent.Object != ast.Expression(call) {
			return read
		}
		read.kind = signalReadArgumentsAt
		read.node = parent
		read.indexes = []ast.Expression{parent.Index}
		if outer, ok := parents[parent].(*ast.SubscriptExpression); ok && outer.Object == ast.Expression(parent) {
			read.kind = signalReadNestedArgument
			read.node = outer
			read.indexes = []ast.Expression{parent.Index, outer.Index}
		}
	}
	return read
}

// isEmptinessCheck reports whether the read is the subject of expect_empty,
// expect_not_empty, or an equality with an empty array, all of which the probe's
// emissions() list answers exactly.
func (t *transformer) isEmptinessCheck(call *ast.CallExpression, parents map[ast.Node]ast.Node) bool {
	parent, ok := parents[call].(*ast.CallExpression)
	if !ok || len(parent.Arguments) == 0 || parent.Arguments[0] != ast.Expression(call) {
		return false
	}
	name, ok := parent.Callee.(*ast.Identifier)
	if !ok {
		return false
	}
	switch name.Name {
	case "expect_empty", "expect_not_empty":
		return len(parent.Arguments) >= 1
	case "expect_equal":
		return len(parent.Arguments) == 2 && t.text(parent.Arguments[1].Span()) == "[]"
	default:
		return false
	}
}

// signalGroupBlocker returns why a signal group cannot be converted, or the empty
// string when every capture and read has a proven equivalent.
func (t *transformer) signalGroupBlocker(group *signalGroup) string {
	if len(group.captures) != 1 {
		return "the same signal is captured more than once in this test"
	}
	if group.foreign != "" {
		return group.foreign
	}
	capture := group.captures[0]
	if len(capture.call.Arguments) != 2 {
		return "capture_signal requires a literal argument count to migrate"
	}
	if literal, ok := capture.call.Arguments[1].(*ast.Literal); !ok || literal.Kind != ast.IntegerLiteral {
		return "capture_signal requires a literal argument count to migrate"
	}
	if capture.statement == nil || capture.function == nil || !strings.HasPrefix(capture.function.Name, "test_") {
		return "capture_signal must be a top-level statement of a test_ function"
	}
	if t.overlapsRewrite(capture.statement.Span()) {
		return "the signal capture overlaps another migration rewrite"
	}
	owner := capture.member.Object.(*ast.Identifier).Name
	if reassigns(capture.function, owner) {
		return "the signal owner is reassigned inside the test function"
	}
	for _, read := range group.reads {
		if t.overlapsRewrite(read.node.Span()) {
			return "the signal read overlaps another migration rewrite"
		}
		if read.kind == signalReadUnsupported {
			return "get_signal_emissions is used in an unsupported expression"
		}
		if !spanContains(capture.function.Span(), read.node.Span()) {
			return "signal reads must be in the same test_ function as the capture"
		}
		if read.node.Span().Start.Offset < capture.statement.Span().End.Offset {
			return "signal reads must follow the capture"
		}
	}
	return signalCaptureUnset
}

// overlapsRewrite reports whether a span intersects an edit already planned by an
// earlier pass, which would make the paired conversion impossible to apply whole.
func (t *transformer) overlapsRewrite(span token.Span) bool {
	for _, existing := range t.edits {
		if span.Start.Offset < existing.end && existing.start < span.End.Offset {
			return true
		}
	}
	return false
}

func reassigns(function *ast.FunctionDeclaration, name string) bool {
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Assignment:
			if identifier, ok := value.Target.(*ast.Identifier); ok && identifier.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// declaredNames returns every name a generated probe must not shadow: file-level
// declarations plus every identifier and declaration inside the function.
func declaredNames(tree *ast.File, function *ast.FunctionDeclaration) map[string]struct{} {
	names := make(map[string]struct{})
	collect := func(root ast.Node) {
		ast.Inspect(root, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Identifier:
				names[value.Name] = struct{}{}
			case *ast.VariableDeclaration:
				names[value.Name] = struct{}{}
			case *ast.FunctionDeclaration:
				names[value.Name] = struct{}{}
				for _, parameter := range value.Parameters {
					names[parameter.Name] = struct{}{}
				}
			}
			return true
		})
	}
	for _, statement := range tree.Statements {
		if declaration, ok := statement.(*ast.FunctionDeclaration); ok {
			names[declaration.Name] = struct{}{}
			continue
		}
		collect(statement)
	}
	collect(function)
	return names
}

func (t *transformer) convertSignalGroup(group *signalGroup, names map[string]struct{}) {
	capture := group.captures[0]
	probe := uniqueName(capture.member.Property+"_probe", names)
	names[probe] = struct{}{}
	expression := probeHelperName + "(" + capture.text + ")"
	if len(group.reads) > 0 {
		expression = "var " + probe + " := " + expression
	}
	t.markSignalHandled(capture.call.Span())
	t.rewrite(capture.statement.Span(), expression, "rewrite.signal-capture", "replace capture_signal with a case-scoped signal probe")
	for _, read := range group.reads {
		t.markSignalHandled(read.call.Span())
		t.rewrite(read.node.Span(), t.signalReadReplacement(probe, read), "rewrite.signal-read", "read recorded emissions from the signal probe")
	}
}

func (t *transformer) signalReadReplacement(probe string, read *signalRead) string {
	switch read.kind {
	case signalReadCount:
		return probe + ".emission_count()"
	case signalReadEmissions:
		return probe + ".emissions()"
	case signalReadArgumentsAt:
		return probe + ".arguments_at(" + t.text(read.indexes[0].Span()) + ")"
	default:
		return probe + ".arguments_at(" + t.text(read.indexes[0].Span()) + ")[" + t.text(read.indexes[1].Span()) + "]"
	}
}

func uniqueName(base string, taken map[string]struct{}) string {
	name := base
	for suffix := 2; ; suffix++ {
		if _, exists := taken[name]; !exists {
			return name
		}
		name = base + "_" + strconv.Itoa(suffix)
	}
}

func (t *transformer) markSignalHandled(span token.Span) {
	if t.signalHandled == nil {
		t.signalHandled = make(map[int]struct{})
	}
	t.signalHandled[span.Start.Offset] = struct{}{}
}

func (t *transformer) isSignalHandled(span token.Span) bool {
	_, handled := t.signalHandled[span.Start.Offset]
	return handled
}
