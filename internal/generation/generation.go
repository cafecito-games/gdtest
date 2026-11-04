// Package generation tracks content-proven project source generations.
package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
)

// ChangeKind describes the least disruptive safe response to a source change.
type ChangeKind string

const (
	// BodyOnly may be eligible for an in-place script reload.
	BodyOnly ChangeKind = "body_only"
	// Structural requires a replacement worker generation.
	Structural ChangeKind = "structural"
)

// FileState proves the content and structural identity of one relevant file.
type FileState struct {
	Path             string `json:"path"`
	ContentDigest    string `json:"content_digest"`
	StructuralDigest string `json:"structural_digest"`
	Parsed           bool   `json:"parsed"`
}

// Snapshot is a path-sorted semantic view of relevant project files.
type Snapshot struct {
	Files map[string]FileState `json:"files"`
}

// ContentFingerprint returns a deterministic digest of every relevant path
// and its content. It is suitable for cache identities across worktrees with
// identical project inputs.
func (s Snapshot) ContentFingerprint() string {
	paths := make([]string, 0, len(s.Files))
	for name := range s.Files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, name := range paths {
		state := s.Files[name]
		_, _ = fmt.Fprintf(digest, "%d:%s", len(name), name)
		_, _ = fmt.Fprintf(digest, "%d:%s", len(state.ContentDigest), state.ContentDigest)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Change records one changed, added, or removed path.
type Change struct {
	Path string     `json:"path"`
	Kind ChangeKind `json:"kind"`
	Old  *FileState `json:"old,omitempty"`
	New  *FileState `json:"new,omitempty"`
}

// Build reads explicit project-relative paths without following paths outside root.
func Build(ctx context.Context, root string, paths []string) (Snapshot, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return Snapshot{}, err
	}
	unique := make(map[string]struct{}, len(paths))
	for _, name := range paths {
		name, err = normalizePath(name)
		if err != nil {
			return Snapshot{}, err
		}
		unique[name] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for name := range unique {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	snapshot := Snapshot{Files: make(map[string]FileState, len(ordered))}
	for _, name := range ordered {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		filename := filepath.Join(root, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(filename)
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve relevant source %s: %w", name, err)
		}
		if !withinRoot(root, resolved) {
			return Snapshot{}, fmt.Errorf("relevant source %s resolves outside project root", name)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read relevant source %s: %w", name, err)
		}
		snapshot.Files[name] = inspect(name, data)
	}
	return snapshot, nil
}

// Compare returns path-sorted changes. Adds, removals, non-GDScript changes,
// and parse failures are structural by design.
func Compare(previous, current Snapshot) []Change {
	paths := make(map[string]struct{}, len(previous.Files)+len(current.Files))
	for name := range previous.Files {
		paths[name] = struct{}{}
	}
	for name := range current.Files {
		paths[name] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	changes := make([]Change, 0)
	for _, name := range ordered {
		oldState, hadOld := previous.Files[name]
		newState, hasNew := current.Files[name]
		if hadOld && hasNew && oldState.ContentDigest == newState.ContentDigest {
			continue
		}
		change := Change{Path: name, Kind: Structural}
		if hadOld {
			copy := oldState
			change.Old = &copy
		}
		if hasNew {
			copy := newState
			change.New = &copy
		}
		if hadOld && hasNew && oldState.Parsed && newState.Parsed &&
			oldState.StructuralDigest == newState.StructuralDigest {
			change.Kind = BodyOnly
		}
		changes = append(changes, change)
	}
	return changes
}

// Update is one monotonic tracker transition.
type Update struct {
	Previous   uint64   `json:"previous"`
	Generation uint64   `json:"generation"`
	Changes    []Change `json:"changes"`
	Structural bool     `json:"structural"`
}

// Tracker owns monotonically increasing source generations.
type Tracker struct {
	mu         sync.RWMutex
	generation uint64
	snapshot   Snapshot
	ready      bool
}

// Apply initializes or advances the tracker. The initial snapshot is generation 1.
func (t *Tracker) Apply(snapshot Snapshot) Update {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ready {
		t.ready = true
		t.generation = 1
		t.snapshot = cloneSnapshot(snapshot)
		return Update{Generation: 1}
	}
	changes := Compare(t.snapshot, snapshot)
	update := Update{Previous: t.generation, Generation: t.generation, Changes: changes}
	if len(changes) == 0 {
		return update
	}
	t.generation++
	update.Generation = t.generation
	for _, change := range changes {
		if change.Kind == Structural {
			update.Structural = true
			break
		}
	}
	t.snapshot = cloneSnapshot(snapshot)
	return update
}

// Generation returns the current generation, or zero before initialization.
func (t *Tracker) Generation() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.generation
}

// Snapshot returns a defensive copy of the current source proof.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return cloneSnapshot(t.snapshot)
}

// IsStale reports whether a result from requestedGeneration is obsolete.
func (t *Tracker) IsStale(requestedGeneration uint64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ready && requestedGeneration < t.generation
}

// Stamp labels a completed result without discarding useful obsolete output.
type Stamp struct {
	Requested uint64 `json:"requested"`
	Current   uint64 `json:"current"`
	Stale     bool   `json:"stale"`
}

// Stamp returns the authority state for a result requested in generation.
func (t *Tracker) Stamp(requestedGeneration uint64) Stamp {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Stamp{
		Requested: requestedGeneration,
		Current:   t.generation,
		Stale:     t.ready && requestedGeneration < t.generation,
	}
}

func inspect(name string, data []byte) FileState {
	state := FileState{Path: name, ContentDigest: digest(data)}
	if !strings.HasSuffix(strings.ToLower(name), ".gd") {
		state.StructuralDigest = state.ContentDigest
		return state
	}
	file, err := gdparser.ParseFile(name, data)
	if err != nil {
		state.StructuralDigest = state.ContentDigest
		return state
	}
	state.Parsed = true
	if strings.HasPrefix(strings.ToLower(filepath.ToSlash(name)), "addons/gdtest/") {
		// The shipped runtime participates in the worker protocol and may affect
		// registration outside any function signature. Never attempt hot reload.
		state.StructuralDigest = state.ContentDigest
		return state
	}
	state.StructuralDigest = structuralDigest(file, data)
	return state
}

type fileShape struct {
	Statements []statementShape `json:"statements"`
}

type statementShape struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name,omitempty"`
	Type        string           `json:"type,omitempty"`
	Value       string           `json:"value,omitempty"`
	Parameters  []parameterShape `json:"parameters,omitempty"`
	ReturnType  string           `json:"return_type,omitempty"`
	Static      bool             `json:"static,omitempty"`
	Abstract    bool             `json:"abstract,omitempty"`
	Constant    bool             `json:"constant,omitempty"`
	Inferred    bool             `json:"inferred,omitempty"`
	Annotations []string         `json:"annotations,omitempty"`
}

type parameterShape struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	HasDefault bool   `json:"has_default,omitempty"`
	Variadic   bool   `json:"variadic,omitempty"`
	Inferred   bool   `json:"inferred,omitempty"`
	Default    string `json:"default,omitempty"`
}

func structuralDigest(file *ast.File, source []byte) string {
	shape := fileShape{Statements: make([]statementShape, 0, len(file.Statements))}
	for _, statement := range file.Statements {
		switch value := statement.(type) {
		case *ast.Directive:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "directive", Name: value.Name,
				Value: sourceText(source, value.Value) + "\x00" + sourceText(source, value.Extends),
			})
		case *ast.Annotation:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "annotation", Name: value.Name, Value: sourceText(source, value),
			})
		case *ast.SignalDeclaration:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "signal", Name: value.Name, Parameters: parameterShapes(source, value.Parameters),
				Annotations: annotationShapes(source, value.Annotations),
			})
		case *ast.FunctionDeclaration:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "function", Name: value.Name, Parameters: parameterShapes(source, value.Parameters),
				ReturnType: value.ReturnType, Static: value.Static, Abstract: value.Abstract,
				Annotations: annotationShapes(source, value.Annotations),
			})
		case *ast.VariableDeclaration:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "variable", Name: value.Name, Type: value.Type,
				Value: sourceText(source, value.Value), Static: value.Static,
				Constant: value.Constant, Inferred: value.Inferred,
				Annotations: annotationShapes(source, value.Annotations),
			})
		case *ast.ClassDeclaration:
			// Inner class bodies can change inheritance, signals, or registration
			// behavior. Treat their complete syntax span as structural conservatively.
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "class", Name: value.Name, Type: value.Extends,
				Value:       sourceText(source, value),
				Annotations: annotationShapes(source, value.Annotations),
			})
		case *ast.EnumDeclaration:
			shape.Statements = append(shape.Statements, statementShape{
				Kind: "enum", Name: value.Name, Value: sourceText(source, value),
				Annotations: annotationShapes(source, value.Annotations),
			})
		case *ast.Comment:
			// Comments do not affect the runtime shape.
		default:
			// Unknown top-level syntax is not proven reload-safe.
			shape.Statements = append(shape.Statements, statementShape{
				Kind: fmt.Sprintf("%T", statement), Value: sourceText(source, statement),
			})
		}
	}
	data, err := json.Marshal(shape)
	if err != nil {
		panic(err)
	}
	return digest(data)
}

func parameterShapes(source []byte, parameters []ast.Parameter) []parameterShape {
	result := make([]parameterShape, 0, len(parameters))
	for _, parameter := range parameters {
		result = append(result, parameterShape{
			Name: parameter.Name, Type: parameter.Type,
			HasDefault: parameter.Default != nil, Variadic: parameter.Variadic,
			Inferred: parameter.Inferred, Default: sourceText(source, parameter.Default),
		})
	}
	return result
}

func annotationShapes(source []byte, annotations []*ast.Annotation) []string {
	result := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		result = append(result, sourceText(source, annotation))
	}
	return result
}

func sourceText(source []byte, node ast.Node) string {
	if node == nil {
		return ""
	}
	span := node.Span()
	if span.Start.Offset < 0 || span.End.Offset < span.Start.Offset || span.End.Offset > len(source) {
		// Couple an invalid AST span to all source content so a change can never be
		// misclassified as body-only.
		return fmt.Sprintf("%T:invalid-span:%s", node, digest(source))
	}
	return string(source[span.Start.Offset:span.End.Offset])
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	result := Snapshot{Files: make(map[string]FileState, len(snapshot.Files))}
	for name, state := range snapshot.Files {
		result.Files[name] = state
	}
	return result
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolute project root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("project root is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func normalizePath(name string) (string, error) {
	name = filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if name == "" || name == "." || filepath.IsAbs(filepath.FromSlash(name)) ||
		name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("relevant path %q must stay within the project", name)
	}
	return name, nil
}

func withinRoot(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
