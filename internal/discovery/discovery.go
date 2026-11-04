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
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdtest/internal/model"
)

// Manifest is an ordered, project-relative static test manifest.
type Manifest struct {
	Files []model.FilePlan `json:"files"`
}

// Options controls filesystem discovery.
type Options struct {
	Patterns []string
	Exclude  []string
}

// Discover walks root and parses matching tests in stable path order.
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
		if entry.Type().IsRegular() && matchesAny(relative, options.Patterns) && !matchesAny(relative, options.Exclude) {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("walk project: %w", err)
	}
	sort.Strings(paths)
	manifest := Manifest{Files: make([]model.FilePlan, 0, len(paths))}
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return Manifest{}, fmt.Errorf("read %s: %w", relative, err)
		}
		file, err := Parse(relative, data)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Files = append(manifest.Files, file)
	}
	return manifest, nil
}

// Parse discovers direct method tests from one source file in source order.
func Parse(relative string, source []byte) (model.FilePlan, error) {
	tree, err := gdparser.ParseFile(relative, source)
	if err != nil {
		return model.FilePlan{}, fmt.Errorf("parse %s: %w", relative, err)
	}
	file := model.FilePlan{Path: path.Clean(filepath.ToSlash(relative))}
	for _, statement := range tree.Statements {
		function, ok := statement.(*ast.FunctionDeclaration)
		if !ok || !strings.HasPrefix(function.Name, "test_") || strings.HasSuffix(function.Name, "__cases") {
			continue
		}
		logical := strings.TrimSuffix(function.Name, "__only")
		if logical == "test_" {
			continue
		}
		if len(function.Parameters) > 0 && !hasProvider(tree, logical) {
			return model.FilePlan{}, fmt.Errorf(
				"discover %s:%d: %s has required parameters but no %s__cases provider",
				file.Path,
				function.NameSpan.Start.Line,
				function.Name,
				logical,
			)
		}
		file.Cases = append(file.Cases, model.CasePlan{
			ID:         file.Path + "::" + logical,
			Name:       displayName(strings.TrimPrefix(logical, "test_")),
			MethodName: function.Name,
			Line:       function.NameSpan.Start.Line,
			IsOnly:     strings.HasSuffix(function.Name, "__only"),
		})
	}
	return file, nil
}

func hasProvider(tree *ast.File, method string) bool {
	want := method + "__cases"
	for _, statement := range tree.Statements {
		function, ok := statement.(*ast.FunctionDeclaration)
		if ok && function.Name == want {
			return true
		}
	}
	return false
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
	selected := Manifest{}
	known := make([]bool, len(selectors))
	for _, file := range manifest.Files {
		out := model.FilePlan{Path: file.Path}
		for _, testCase := range file.Cases {
			for index, selector := range selectors {
				pathMatch := selector == file.Path || strings.HasPrefix(file.Path, strings.TrimSuffix(selector, "/")+"/")
				globMatch, _ := path.Match(selector, file.Path)
				if pathMatch || globMatch || selector == testCase.ID {
					known[index] = true
					out.Cases = append(out.Cases, testCase)
					break
				}
			}
		}
		if len(out.Cases) > 0 {
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
