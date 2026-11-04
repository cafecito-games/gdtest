// Package discovery performs deterministic static GDScript test discovery.
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdtest/internal/model"
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

// Discover walks root and builds a deterministic static index. All GDScript
// files participate in inheritance and dependency resolution, while only
// matching GDTest descendants contribute executable plans.
func Discover(root string, options Options) (Manifest, error) {
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
		if entry.IsDir() && matchesAny(relative+"/placeholder", options.Exclude) {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && strings.HasSuffix(relative, ".gd") && !matchesAny(relative, options.Exclude) {
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
		tree, err := gdparser.ParseFile(relative, data)
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
		if matchesAny(relative, options.Patterns) && script.metadata.ExtendsGDTest {
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

func matchesAny(filename string, patterns []string) bool {
	for _, pattern := range patterns {
		if globMatch(pattern, filename) {
			return true
		}
	}
	return false
}

func globMatch(pattern, filename string) bool {
	if !strings.Contains(pattern, "**") {
		matched, err := path.Match(pattern, filename)
		return err == nil && matched
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(pattern); {
		if strings.HasPrefix(pattern[index:], "**/") {
			expression.WriteString("(?:.*/)?")
			index += 3
			continue
		}
		if strings.HasPrefix(pattern[index:], "**") {
			expression.WriteString(".*")
			index += 2
			continue
		}
		switch pattern[index] {
		case '*':
			expression.WriteString("[^/]*")
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[index : index+1]))
		}
		index++
	}
	expression.WriteByte('$')
	matched, err := regexp.MatchString(expression.String(), filename)
	return err == nil && matched
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
	selected := Manifest{
		Scripts:               manifest.Scripts,
		StructuralFingerprint: manifest.StructuralFingerprint,
		ContentFingerprint:    manifest.ContentFingerprint,
	}
	known := make([]bool, len(selectors))
	for _, file := range manifest.Files {
		out := model.FilePlan{Path: file.Path}
		fileSelected := false
		for index, selector := range selectors {
			pathMatch := selector == file.Path || strings.HasPrefix(file.Path, strings.TrimSuffix(selector, "/")+"/")
			globMatched, _ := path.Match(selector, file.Path)
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
			for index, selector := range selectors {
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
