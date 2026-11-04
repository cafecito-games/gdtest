// Package projectmeta extracts path-neutral runtime inputs from project.godot.
package projectmeta

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/configfile"
	"github.com/cafecito-games/gdparser/configfile/ast"
	"github.com/cafecito-games/gdparser/configfile/token"
	"github.com/cafecito-games/gdparser/textresource"
)

const projectFilename = "project.godot"

// Location identifies project metadata without embedding an absolute path.
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Diagnostic explains metadata that could not be resolved without risking an
// affected-test false negative.
type Diagnostic struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
}

// Autoload is one project-global script or scene.
type Autoload struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Singleton bool     `json:"singleton"`
	Location  Location `json:"location"`
}

// Metadata is a deterministic project-relative affected-selection fragment.
// ConservativeAll tells callers to select all tests because at least one
// global input could not be resolved to a project-relative path.
type Metadata struct {
	Autoloads       []Autoload   `json:"autoloads,omitempty"`
	GlobalInputs    []string     `json:"global_inputs,omitempty"`
	NativeInputs    []string     `json:"native_inputs,omitempty"`
	Diagnostics     []Diagnostic `json:"diagnostics,omitempty"`
	ConservativeAll bool         `json:"conservative_all,omitempty"`
}

// Load parses root/project.godot. Returned identities never include root.
func Load(root string) (Metadata, error) {
	data, err := os.ReadFile(filepath.Join(root, projectFilename))
	if err != nil {
		return Metadata{}, fmt.Errorf("read %s: %w", projectFilename, err)
	}
	metadata, err := Parse(data)
	if err != nil {
		return Metadata{}, err
	}
	metadata.loadGDExtensions(root)
	metadata.finish()
	return metadata, nil
}

// Parse extracts autoload, main-scene, and native-extension inputs.
func Parse(source []byte) (Metadata, error) {
	file, err := configfile.ParseFile(projectFilename, source)
	if err != nil {
		return Metadata{}, fmt.Errorf("parse %s: %w", projectFilename, err)
	}
	result := Metadata{}
	global := make(map[string]struct{})
	native := make(map[string]struct{})
	autoloadNames := make(map[string]struct{})
	for _, section := range file.Sections {
		for _, statement := range section.Statements {
			assignment, ok := statement.(*ast.Assignment)
			if !ok {
				continue
			}
			if section.Name == "autoload" {
				readAutoload(&result, global, autoloadNames, assignment)
			}
			if section.Name == "application" && assignment.Key == "run/main_scene" {
				readGlobalString(&result, global, assignment, "project.main-scene")
			}
			if section.Name == "native_extensions" && assignment.Key == "paths" {
				readNativeExtensionPaths(&result, global, native, assignment)
				continue
			}
			ast.Inspect(assignment.Value, func(node ast.Node) bool {
				literal, ok := node.(*ast.StringLiteral)
				if !ok || !nativeResource(literal.Value) {
					return true
				}
				name, valid := normalizeResource(literal.Value)
				if !valid {
					result.unresolved("project.native-input", "native runtime input is not a resolvable res:// path", literal.Span())
					return true
				}
				native[name] = struct{}{}
				global[name] = struct{}{}
				return true
			})
		}
	}
	result.GlobalInputs = sortedKeys(global)
	result.NativeInputs = sortedKeys(native)
	result.finish()
	return result, nil
}

func (metadata *Metadata) finish() {
	sort.SliceStable(metadata.Autoloads, func(i, j int) bool {
		if metadata.Autoloads[i].Name != metadata.Autoloads[j].Name {
			return metadata.Autoloads[i].Name < metadata.Autoloads[j].Name
		}
		return metadata.Autoloads[i].Path < metadata.Autoloads[j].Path
	})
	sort.Strings(metadata.GlobalInputs)
	metadata.GlobalInputs = compact(metadata.GlobalInputs)
	sort.Strings(metadata.NativeInputs)
	metadata.NativeInputs = compact(metadata.NativeInputs)
	sort.SliceStable(metadata.Diagnostics, func(i, j int) bool {
		left, right := metadata.Diagnostics[i], metadata.Diagnostics[j]
		if left.Location.File != right.Location.File {
			return left.Location.File < right.Location.File
		}
		if left.Location.Line != right.Location.Line {
			return left.Location.Line < right.Location.Line
		}
		if left.Location.Column != right.Location.Column {
			return left.Location.Column < right.Location.Column
		}
		return left.Code < right.Code
	})
}

func (metadata *Metadata) loadGDExtensions(root string) {
	global := make(map[string]struct{}, len(metadata.GlobalInputs))
	native := make(map[string]struct{}, len(metadata.NativeInputs))
	for _, name := range metadata.GlobalInputs {
		global[name] = struct{}{}
	}
	for _, name := range metadata.NativeInputs {
		native[name] = struct{}{}
	}
	for _, extension := range append([]string(nil), metadata.NativeInputs...) {
		if !strings.EqualFold(path.Ext(extension), ".gdextension") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(extension)))
		if err != nil {
			metadata.unresolvedAt("project.gdextension.read", fmt.Sprintf("read declared GDExtension %q: %v", extension, err), Location{File: extension, Line: 1, Column: 1})
			continue
		}
		document, err := textresource.ParseFile(extension, data)
		if err != nil {
			location := Location{File: extension, Line: 1, Column: 1}
			var parseError *textresource.Error
			if errors.As(err, &parseError) {
				location.Line = parseError.Position.Line
				location.Column = parseError.Position.Column
			}
			metadata.unresolvedAt("project.gdextension.parse", err.Error(), location)
			continue
		}
		readGDExtension(metadata, document, extension, global, native)
	}
	metadata.GlobalInputs = sortedKeys(global)
	metadata.NativeInputs = sortedKeys(native)
}

func readGDExtension(
	metadata *Metadata,
	document *textresource.Document,
	extension string,
	global map[string]struct{},
	native map[string]struct{},
) {
	for _, item := range document.Items {
		section, ok := item.(*textresource.Section)
		if !ok || (section.Type != "libraries" && section.Type != "dependencies") {
			continue
		}
		for _, bodyItem := range section.Body {
			assignment, ok := bodyItem.(*textresource.Assignment)
			if !ok {
				continue
			}
			if section.Type == "libraries" {
				literal, stringValue := assignment.Value.(*textresource.StringValue)
				if !stringValue || literal.Value == "" {
					span := assignment.Value.Span()
					metadata.unresolvedAt("project.gdextension.library.invalid-value", "GDExtension library entry must be a non-empty String path", resourceLocation(extension, span.Start.Line, span.Start.Column))
					continue
				}
				addExtensionInput(metadata, extension, literal.Value, literal.Span().Start.Line, literal.Span().Start.Column, global, native)
				continue
			}
			textresource.Inspect(assignment.Value, func(node textresource.Node) bool {
				literal, stringValue := node.(*textresource.StringValue)
				if !stringValue || literal.Value == "" {
					return true
				}
				addExtensionInput(metadata, extension, literal.Value, literal.Span().Start.Line, literal.Span().Start.Column, global, native)
				return true
			})
		}
	}
}

func addExtensionInput(
	metadata *Metadata,
	extension string,
	value string,
	line int,
	column int,
	global map[string]struct{},
	native map[string]struct{},
) {
	name, valid := normalizeExtensionResource(extension, value)
	if !valid {
		metadata.unresolvedAt("project.gdextension.unresolved-path", fmt.Sprintf("native runtime input %q is not project-relative", value), resourceLocation(extension, line, column))
		return
	}
	native[name] = struct{}{}
	global[name] = struct{}{}
}

func readNativeExtensionPaths(
	result *Metadata,
	global map[string]struct{},
	native map[string]struct{},
	assignment *ast.Assignment,
) {
	literals, valid := stringList(assignment.Value)
	if !valid {
		result.unresolved("project.native-extensions.invalid-value", "native_extensions/paths must be an Array of String resource paths", assignment.Value.Span())
		return
	}
	for _, literal := range literals {
		name, normalized := normalizeResource(literal.Value)
		if !normalized || !strings.EqualFold(path.Ext(name), ".gdextension") {
			result.unresolved("project.native-extensions.unresolved-path", "native extension is not a resolvable res:// .gdextension path", literal.Span())
			continue
		}
		native[name] = struct{}{}
		global[name] = struct{}{}
	}
}

func stringList(expression ast.Expression) ([]*ast.StringLiteral, bool) {
	var items []ast.ArrayItem
	switch value := expression.(type) {
	case *ast.ArrayLiteral:
		items = value.Items
	case *ast.ConstructorCall:
		if value.Name != "PackedStringArray" {
			return nil, false
		}
		result := make([]*ast.StringLiteral, 0, len(value.Items))
		for _, item := range value.Items {
			argument, ok := item.(*ast.ConstructorArgument)
			if !ok {
				continue
			}
			literal, ok := argument.Value.(*ast.StringLiteral)
			if !ok || argument.Key != nil {
				return nil, false
			}
			result = append(result, literal)
		}
		return result, true
	default:
		return nil, false
	}
	result := make([]*ast.StringLiteral, 0, len(items))
	for _, item := range items {
		element, ok := item.(*ast.ArrayElement)
		if !ok {
			continue
		}
		literal, ok := element.Value.(*ast.StringLiteral)
		if !ok {
			return nil, false
		}
		result = append(result, literal)
	}
	return result, true
}

func readAutoload(
	result *Metadata,
	global map[string]struct{},
	names map[string]struct{},
	assignment *ast.Assignment,
) {
	literal, ok := assignment.Value.(*ast.StringLiteral)
	if !ok {
		result.unresolved("project.autoload.invalid-value", fmt.Sprintf("autoload %q must be a String path", assignment.Key), assignment.Value.Span())
		return
	}
	value := literal.Value
	singleton := strings.HasPrefix(value, "*")
	value = strings.TrimPrefix(value, "*")
	name, valid := normalizeResource(value)
	if !valid {
		result.unresolved("project.autoload.unresolved-path", fmt.Sprintf("autoload %q is not a resolvable res:// path", assignment.Key), literal.Span())
		return
	}
	if _, duplicate := names[assignment.Key]; duplicate {
		result.unresolved("project.autoload.duplicate", fmt.Sprintf("autoload %q is declared more than once", assignment.Key), assignment.Span())
	}
	names[assignment.Key] = struct{}{}
	result.Autoloads = append(result.Autoloads, Autoload{
		Name: assignment.Key, Path: name, Singleton: singleton,
		Location: sourceLocation(literal.Span()),
	})
	global[name] = struct{}{}
}

func readGlobalString(result *Metadata, global map[string]struct{}, assignment *ast.Assignment, code string) {
	literal, ok := assignment.Value.(*ast.StringLiteral)
	if !ok {
		result.unresolved(code, fmt.Sprintf("%s must be a String resource path", assignment.Key), assignment.Value.Span())
		return
	}
	if literal.Value == "" {
		return
	}
	name, valid := normalizeResource(literal.Value)
	if !valid {
		result.unresolved(code, fmt.Sprintf("%s is not a resolvable res:// path", assignment.Key), literal.Span())
		return
	}
	global[name] = struct{}{}
}

func (metadata *Metadata) unresolved(code, message string, span token.Span) {
	metadata.unresolvedAt(code, message, sourceLocation(span))
}

func (metadata *Metadata) unresolvedAt(code, message string, location Location) {
	metadata.ConservativeAll = true
	metadata.Diagnostics = append(metadata.Diagnostics, Diagnostic{
		Code: code, Message: message, Location: location,
	})
}

func sourceLocation(span token.Span) Location {
	return Location{File: projectFilename, Line: span.Start.Line, Column: span.Start.Column}
}

func resourceLocation(filename string, line, column int) Location {
	return Location{File: filename, Line: line, Column: column}
}

func normalizeResource(value string) (string, bool) {
	if !strings.HasPrefix(value, "res://") {
		return "", false
	}
	return normalizeProjectPath(strings.TrimPrefix(value, "res://"))
}

func normalizeExtensionResource(extension, value string) (string, bool) {
	if strings.HasPrefix(value, "res://") {
		return normalizeResource(value)
	}
	if strings.Contains(value, "://") || path.IsAbs(value) {
		return "", false
	}
	return normalizeProjectPath(path.Join(path.Dir(extension), value))
}

func normalizeProjectPath(value string) (string, bool) {
	if value == "" || strings.ContainsRune(value, '\\') {
		return "", false
	}
	name := path.Clean(value)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", false
	}
	return name, true
}

func nativeResource(value string) bool {
	lower := strings.ToLower(value)
	switch path.Ext(lower) {
	case ".gdextension", ".so", ".dylib", ".dll", ".framework", ".xcframework", ".wasm":
		return true
	default:
		return false
	}
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func compact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
