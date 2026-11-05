// Package discovery performs deterministic static GDScript test discovery.
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/pathglob"
)

// Manifest is an ordered, project-relative static test manifest.
type Manifest struct {
	Files                 []model.FilePlan `json:"files"`
	Scripts               []ScriptMetadata `json:"scripts,omitempty"`
	StructuralFingerprint string           `json:"structural_fingerprint"`
	ContentFingerprint    string           `json:"content_fingerprint"`
}

// Options controls filesystem discovery.
type Options struct {
	Patterns []string
	Exclude  []string
}

// ParseFunc parses one project-relative GDScript source. Cached discovery may
// provide a content-addressed implementation while direct callers use gdparser.
type ParseFunc func(relative string, source []byte) (*ast.File, error)

// Discover walks root and builds a deterministic static index. All GDScript
// files participate in inheritance and dependency resolution, while only
// matching GDTest descendants contribute executable plans.
func Discover(root string, options Options) (Manifest, error) {
	return DiscoverWithParser(root, options, gdparser.ParseFile)
}

// DiscoverWithParser performs discovery with an injected syntax-tree parser.
func DiscoverWithParser(root string, options Options, parse ParseFunc) (Manifest, error) {
	if parse == nil {
		return Manifest{}, errors.New("discovery parser is nil")
	}
	if len(options.Patterns) == 0 {
		options.Patterns = []string{"**/*.test.gd"}
	}
	var paths []string
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if entry.IsDir() && pathglob.MatchesAny(relative+"/placeholder", options.Exclude) {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && strings.HasSuffix(relative, ".gd") && !pathglob.MatchesAny(relative, options.Exclude) {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("walk project: %w", err)
	}
	sort.Strings(paths)
	scripts := make(map[string]*parsedScript, len(paths))
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return Manifest{}, fmt.Errorf("read %s: %w", relative, err)
		}
		tree, err := parse(relative, data)
		if err != nil {
			return Manifest{}, fmt.Errorf("parse %s: %w", relative, err)
		}
		script, err := inspectScript(relative, data, tree, false)
		if err != nil {
			return Manifest{}, err
		}
		scripts[script.metadata.Path] = &script
	}
	resolveInheritance(scripts)
	for _, relative := range paths {
		script := scripts[normalizeRelative(relative)]
		if !script.metadata.ExtendsGDTest {
			continue
		}
		if _, err := inspectScript(script.metadata.Path, script.source, script.tree, true); err != nil {
			return Manifest{}, err
		}
	}

	manifest := Manifest{
		Files:   make([]model.FilePlan, 0),
		Scripts: make([]ScriptMetadata, 0, len(paths)),
	}
	var structural, content strings.Builder
	for _, relative := range paths {
		relative = normalizeRelative(relative)
		script := scripts[relative]
		manifest.Scripts = append(manifest.Scripts, script.metadata)
		structural.WriteString(relative)
		structural.WriteByte(0)
		structural.WriteString(script.metadata.StructuralFingerprint)
		if script.metadata.ExtendsGDTest {
			structural.WriteString(":gdtest")
		}
		structural.WriteByte('\n')
		content.WriteString(relative)
		content.WriteByte(0)
		content.WriteString(script.metadata.ContentFingerprint)
		content.WriteByte('\n')
		if pathglob.MatchesAny(relative, options.Patterns) && script.metadata.ExtendsGDTest {
			manifest.Files = append(manifest.Files, script.plan)
		}
	}
	manifest.StructuralFingerprint = fingerprint([]byte(structural.String()))
	manifest.ContentFingerprint = fingerprint([]byte(content.String()))
	return manifest, nil
}

// Parse discovers direct method tests from one source file in source order.
func Parse(relative string, source []byte) (model.FilePlan, error) {
	tree, err := gdparser.ParseFile(relative, source)
	if err != nil {
		return model.FilePlan{}, fmt.Errorf("parse %s: %w", relative, err)
	}
	script, err := inspectScript(relative, source, tree, true)
	if err != nil {
		return model.FilePlan{}, err
	}
	return script.plan, nil
}

func displayName(name string) string {
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return words
	}
	runes := []rune(words)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// Select filters a manifest by exact file path, directory, glob, or stable ID.
func Select(manifest Manifest, selectors []string, allowEmpty bool) (Manifest, error) {
	if len(selectors) == 0 {
		return manifest, nil
	}
	normalized := make([]string, len(selectors))
	for index, selector := range selectors {
		clean, err := normalizeDiscoverySelector(selector)
		if err != nil {
			return Manifest{}, err
		}
		normalized[index] = clean
	}
	selected := Manifest{
		Scripts:               manifest.Scripts,
		StructuralFingerprint: manifest.StructuralFingerprint,
		ContentFingerprint:    manifest.ContentFingerprint,
	}
	known := make([]bool, len(selectors))
	for _, file := range manifest.Files {
		out := model.FilePlan{Path: file.Path}
		fileSelected := false
		for index, selector := range normalized {
			pathMatch := selector == file.Path || strings.HasPrefix(file.Path, strings.TrimSuffix(selector, "/")+"/")
			globMatched := pathglob.Match(selector, file.Path)
			if pathMatch || globMatched {
				known[index] = true
				fileSelected = true
			}
		}
		for _, testCase := range file.Cases {
			if fileSelected {
				out.Cases = append(out.Cases, testCase)
				continue
			}
			for index, selector := range normalized {
				if selector == testCase.ID {
					known[index] = true
					out.Cases = append(out.Cases, testCase)
					break
				}
			}
		}
		if fileSelected || len(out.Cases) > 0 {
			selected.Files = append(selected.Files, out)
		}
	}
	var unknown []string
	for index, selector := range selectors {
		if !known[index] {
			unknown = append(unknown, selector)
		}
	}
	if len(unknown) > 0 && !allowEmpty {
		return Manifest{}, fmt.Errorf("unknown selector(s): %s", strings.Join(unknown, ", "))
	}
	if len(selected.Files) == 0 && !allowEmpty {
		return Manifest{}, errors.New("selectors matched no tests")
	}
	return selected, nil
}

func normalizeDiscoverySelector(selector string) (string, error) {
	original := selector
	selector = strings.ReplaceAll(selector, `\`, "/")
	if selector == "" {
		return "", errors.New("selector must not be empty")
	}
	if strings.HasPrefix(selector, "/") ||
		(len(selector) >= 3 && selector[1] == ':' && selector[2] == '/') {
		return "", fmt.Errorf("selector %q must be project-relative", original)
	}
	selector = strings.TrimPrefix(selector, "res://")
	selector = strings.TrimPrefix(selector, "./")
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(selector)))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("selector %q escapes the project root", original)
	}
	if strings.ContainsAny(clean, "*?[") {
		for _, part := range strings.Split(clean, "/") {
			if part == "**" {
				continue
			}
			if _, err := path.Match(part, ""); err != nil {
				return "", fmt.Errorf("invalid selector %q: %w", original, err)
			}
		}
	}
	return clean, nil
}
