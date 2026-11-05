package doubles

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/parser"
	"github.com/cafecito-games/gdparser/token"
)

// ownershipMarker matches the exact first line of a generated double. The
// capture is the project-relative path of the script it doubles, which may
// contain spaces; generatedSourceOf validates it.
var ownershipMarker = regexp.MustCompile(`^# gdtest:generated-double v1 source=res://(.+)$`)

const resourcePrefix = "res://"

// ownershipLineLimit bounds how much of an excluded script is read to find its
// marker. A first line longer than this cannot be a marker, because the marker
// pattern is a fixed prefix followed by one resource path.
const ownershipLineLimit = 4096

// GeneratedScript is a script whose first line carries the generated-double
// ownership marker.
type GeneratedScript struct {
	Path   string
	Source string
}

// Index is the parsed view of every GDScript file under a project root.
//
// Index holds canonical paths and parsed trees, so it must be built from one
// project root and used only while that tree is unchanged.
type Index struct {
	root    string
	scripts map[string]*script
	failed  map[string]bool
	// classes maps each class_name declared by a script that is not marker-owned.
	// Generated doubles are excluded: their class_name is owned by configuration,
	// which enforces uniqueness across entries, so they are never resolvable
	// project classes.
	classes map[string]*script
	// excludedOwned maps the project-relative path of each excluded script whose
	// first line carries the ownership marker to the source it doubles. Excluded
	// scripts are never parsed, so they appear here and nowhere else.
	excludedOwned map[string]string
	diagnostics   []Diagnostic
}

// script records the facts about one parsed file that analysis needs.
type script struct {
	path            string
	source          []byte
	tree            *ast.File
	className       string
	extends         ast.Expression
	generatedSource string
	functions       []*ast.FunctionDeclaration
	enums           map[string]bool
	innerClasses    map[string]bool
	members         []member
}

// member is a top-level declaration whose name a generated double must not reuse.
type member struct {
	name string
	span token.Span
}

// BuildIndex parses every *.gd file under root, skipping .godot and .git
// directories. A file or directory matched by exclude, which uses the same glob
// semantics as test discovery, is not parsed and takes no part in class or
// inheritance resolution, but its first line is still checked for the
// ownership marker so that stale generated outputs under it are found.
// Failures to read or parse a non-excluded file become diagnostics; the index
// still contains every file that parsed. When root cannot be resolved, the
// returned index is nil.
func BuildIndex(root string, exclude []string) (*Index, []Diagnostic) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, []Diagnostic{{Path: root, Message: "resolve project root: " + err.Error()}}
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, []Diagnostic{{Path: root, Message: "resolve project root: " + err.Error()}}
	}
	index := &Index{
		root:          canonical,
		scripts:       make(map[string]*script),
		failed:        make(map[string]bool),
		classes:       make(map[string]*script),
		excludedOwned: make(map[string]string),
	}
	walkErr := filepath.WalkDir(canonical, func(current string, entry fs.DirEntry, walkErr error) error {
		relative := relativeSlash(canonical, current)
		if walkErr != nil {
			if current != canonical && directoryExcluded(relative, exclude) {
				return nil
			}
			index.diagnostics = append(index.diagnostics, Diagnostic{Path: relative, Message: "walk project: " + walkErr.Error()})
			return nil
		}
		if entry.IsDir() {
			if current != canonical && (entry.Name() == ".godot" || entry.Name() == ".git") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".gd" {
			return nil
		}
		if isExcluded(relative, exclude) {
			index.addExcluded(current, relative)
			return nil
		}
		index.addFile(current, relative, entry)
		return nil
	})
	if walkErr != nil {
		index.diagnostics = append(index.diagnostics, Diagnostic{Path: ".", Message: "walk project: " + walkErr.Error()})
	}
	SortDiagnostics(index.diagnostics)
	return index, index.diagnostics
}

// GeneratedScripts returns every script carrying the ownership marker, including
// scripts under excluded paths, sorted by path.
func (index *Index) GeneratedScripts() []GeneratedScript {
	var owned []GeneratedScript
	for _, script := range index.scripts {
		if script.generatedSource != "" {
			owned = append(owned, GeneratedScript{Path: script.path, Source: script.generatedSource})
		}
	}
	for relative, source := range index.excludedOwned {
		owned = append(owned, GeneratedScript{Path: relative, Source: source})
	}
	sort.Slice(owned, func(left, right int) bool { return owned[left].Path < owned[right].Path })
	return owned
}

func (index *Index) addFile(current string, relative string, entry fs.DirEntry) {
	if entry.Type()&fs.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			index.diagnostics = append(index.diagnostics, Diagnostic{Path: relative, Message: "resolve symlink: " + err.Error()})
			return
		}
		if !within(index.root, resolved) {
			index.diagnostics = append(index.diagnostics, Diagnostic{Path: relative, Message: "symlink resolves outside the project root"})
			return
		}
	}
	source, err := os.ReadFile(current)
	if err != nil {
		index.diagnostics = append(index.diagnostics, Diagnostic{Path: relative, Message: "read script: " + err.Error()})
		return
	}
	tree, err := gdparser.ParseFile(relative, source)
	if err != nil {
		index.failed[relative] = true
		index.diagnostics = append(index.diagnostics, parseDiagnostic(relative, err))
		return
	}
	scanned := &script{
		path:            relative,
		source:          source,
		tree:            tree,
		generatedSource: generatedSourceOf(source),
		enums:           make(map[string]bool),
		innerClasses:    make(map[string]bool),
	}
	index.scanStatements(scanned)
	index.scripts[relative] = scanned
	if scanned.className != "" && scanned.generatedSource == "" {
		if _, exists := index.classes[scanned.className]; !exists {
			index.classes[scanned.className] = scanned
		}
	}
}

// addExcluded records an excluded script as owned when its first line carries
// the marker. The script is never parsed, and no read or link problem is
// reported, so an excluded broken file cannot block generation. A symbolic link
// that resolves outside the project is not read.
func (index *Index) addExcluded(current string, relative string) {
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !within(index.root, resolved) {
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	firstLine, err := readFirstLine(resolved)
	if err != nil {
		return
	}
	if source := generatedSourceOf(firstLine); source != "" {
		index.excludedOwned[relative] = source
	}
}

// readFirstLine returns the first line of filename, including its newline, and
// stops reading at ownershipLineLimit bytes. A longer first line returns nothing.
func readFirstLine(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReaderSize(file, ownershipLineLimit).ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return nil, nil
	case errors.Is(err, io.EOF), err == nil:
		return line, nil
	default:
		return nil, err
	}
}

func (index *Index) scanStatements(scanned *script) {
	for _, statement := range scanned.tree.Statements {
		switch declaration := statement.(type) {
		case *ast.Directive:
			index.scanDirective(scanned, declaration)
		case *ast.FunctionDeclaration:
			scanned.functions = append(scanned.functions, declaration)
			scanned.members = append(scanned.members, member{name: declaration.Name, span: declaration.NameSpan})
		case *ast.VariableDeclaration:
			scanned.members = append(scanned.members, member{name: declaration.Name, span: declaration.NameSpan})
		case *ast.SignalDeclaration:
			scanned.members = append(scanned.members, member{name: declaration.Name, span: declaration.NameSpan})
		case *ast.EnumDeclaration:
			if declaration.Name != "" {
				scanned.enums[declaration.Name] = true
				scanned.members = append(scanned.members, member{name: declaration.Name, span: declaration.NameSpan})
			}
			for _, enumMember := range declaration.Members {
				scanned.members = append(scanned.members, member{name: enumMember.Name, span: enumMember.NameSpan})
			}
		case *ast.ClassDeclaration:
			scanned.innerClasses[declaration.Name] = true
			scanned.members = append(scanned.members, member{name: declaration.Name, span: declaration.NameSpan})
		}
	}
}

func (index *Index) scanDirective(scanned *script, directive *ast.Directive) {
	switch directive.Name {
	case "class_name":
		identifier, ok := directive.Value.(*ast.Identifier)
		if !ok {
			return
		}
		if scanned.className != "" {
			index.diagnostics = append(index.diagnostics, spanDiagnostic(scanned.path, identifier.Span(),
				"script declares more than one class_name"))
			return
		}
		if existing, exists := index.classes[identifier.Name]; exists && existing != scanned && scanned.generatedSource == "" {
			index.diagnostics = append(index.diagnostics, spanDiagnostic(scanned.path, identifier.Span(),
				"class_name "+identifier.Name+" is also declared by "+existing.path))
		}
		scanned.className = identifier.Name
		if directive.Extends != nil {
			setExtends(index, scanned, directive.Extends)
		}
	case "extends":
		if directive.Value != nil {
			setExtends(index, scanned, directive.Value)
		}
	}
}

func setExtends(index *Index, scanned *script, expression ast.Expression) {
	if scanned.extends != nil {
		index.diagnostics = append(index.diagnostics, spanDiagnostic(scanned.path, expression.Span(),
			"script declares more than one base class"))
		return
	}
	scanned.extends = expression
}

func parseDiagnostic(relative string, err error) Diagnostic {
	var parseErr *parser.Error
	if errors.As(err, &parseErr) {
		return spanDiagnostic(relative, parseErr.Token.Span, parseErr.Message)
	}
	return Diagnostic{Path: relative, Message: err.Error()}
}

func generatedSourceOf(source []byte) string {
	firstLine := source
	if end := bytes.IndexByte(source, '\n'); end >= 0 {
		firstLine = source[:end]
	}
	match := ownershipMarker.FindSubmatch(firstLine)
	if match == nil {
		return ""
	}
	captured := string(match[1])
	if captured != strings.TrimSpace(captured) || !strings.HasSuffix(captured, ".gd") {
		return ""
	}
	return captured
}

// literalString returns the contents of a string literal without its quotes.
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

func relativeSlash(root string, current string) string {
	relative, err := filepath.Rel(root, current)
	if err != nil {
		return filepath.ToSlash(current)
	}
	return filepath.ToSlash(relative)
}

// within reports whether path is root or lies beneath it. Both must be canonical.
func within(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
