package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/pathglob"
)

// BenchmarkCase is one statically discovered benchmark method.
type BenchmarkCase = model.BenchmarkCasePlan

// BenchmarkFile is one source-ordered benchmark script.
type BenchmarkFile = model.BenchmarkFilePlan

// BenchmarkManifest is an ordered, path-neutral static benchmark manifest.
type BenchmarkManifest struct {
	Files              []BenchmarkFile `json:"files"`
	ContentFingerprint string          `json:"content_fingerprint"`
}

// BenchmarkOptions controls benchmark filesystem discovery.
type BenchmarkOptions struct {
	Patterns []string
	Exclude  []string
}

type benchmarkScript struct {
	path      string
	className string
	baseName  string
	basePath  string
	extends   bool
	functions []*ast.FunctionDeclaration
	content   string
}

var benchmarkHookArguments = map[string]int{
	"before_all": 0, "after_all": 0,
	"before_benchmark": 1, "after_benchmark": 1,
	"before_sample": 1, "after_sample": 1,
}

// DiscoverBenchmarks statically discovers conventional benchmark scripts.
func DiscoverBenchmarks(root string, options BenchmarkOptions) (BenchmarkManifest, error) {
	if len(options.Patterns) == 0 {
		options.Patterns = []string{"**/*.bench.gd"}
	}
	paths, err := benchmarkPaths(root, options.Exclude)
	if err != nil {
		return BenchmarkManifest{}, err
	}
	scripts := make(map[string]*benchmarkScript, len(paths))
	for _, relative := range paths {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			return BenchmarkManifest{}, fmt.Errorf("read %s: %w", relative, readErr)
		}
		tree, parseErr := gdparser.ParseFile(relative, data)
		if parseErr != nil {
			return BenchmarkManifest{}, fmt.Errorf("parse %s: %w", relative, parseErr)
		}
		script := &benchmarkScript{path: normalizeRelative(relative), content: fingerprint(data)}
		for _, statement := range tree.Statements {
			switch declaration := statement.(type) {
			case *ast.Directive:
				inspectBenchmarkDirective(script, declaration)
			case *ast.FunctionDeclaration:
				script.functions = append(script.functions, declaration)
			}
		}
		scripts[script.path] = script
	}
	resolveBenchmarkInheritance(scripts)
	manifest := BenchmarkManifest{Files: make([]BenchmarkFile, 0)}
	var content strings.Builder
	for _, relative := range paths {
		script := scripts[normalizeRelative(relative)]
		content.WriteString(script.path)
		content.WriteByte(0)
		content.WriteString(script.content)
		content.WriteByte('\n')
		if !script.extends {
			continue
		}
		file, inspectErr := inspectBenchmarkContracts(script)
		if inspectErr != nil {
			return BenchmarkManifest{}, inspectErr
		}
		if pathglob.MatchesAny(script.path, options.Patterns) {
			manifest.Files = append(manifest.Files, file)
		}
	}
	manifest.ContentFingerprint = fingerprint([]byte(content.String()))
	return manifest, nil
}

// SelectBenchmarks filters by exact path, directory, glob, or stable ID.
func SelectBenchmarks(manifest BenchmarkManifest, selectors []string, allowEmpty bool) (BenchmarkManifest, error) {
	files := make([]model.FilePlan, 0, len(manifest.Files))
	byID := make(map[string]BenchmarkCase)
	for _, file := range manifest.Files {
		mapped := model.FilePlan{Path: file.Path}
		for _, item := range file.Benchmarks {
			mapped.Cases = append(mapped.Cases, model.CasePlan{
				ID: item.ID, Name: item.Name, MethodName: item.MethodName, Line: item.Line,
			})
			byID[item.ID] = item
		}
		files = append(files, mapped)
	}
	selected, err := Select(Manifest{Files: files}, selectors, allowEmpty)
	if err != nil {
		return BenchmarkManifest{}, errors.New(strings.NewReplacer("tests", "benchmarks", "test", "benchmark").Replace(err.Error()))
	}
	result := BenchmarkManifest{ContentFingerprint: manifest.ContentFingerprint}
	for _, file := range selected.Files {
		out := BenchmarkFile{Path: file.Path}
		for _, item := range file.Cases {
			out.Benchmarks = append(out.Benchmarks, byID[item.ID])
		}
		result.Files = append(result.Files, out)
	}
	return result, nil
}

func benchmarkPaths(root string, exclude []string) ([]string, error) {
	paths := make([]string, 0)
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
		if entry.IsDir() && pathglob.MatchesAny(relative+"/placeholder", exclude) {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && strings.HasSuffix(relative, ".gd") && !pathglob.MatchesAny(relative, exclude) {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk project for benchmarks: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func inspectBenchmarkDirective(script *benchmarkScript, directive *ast.Directive) {
	if directive.Name == "class_name" {
		if identifier, ok := directive.Value.(*ast.Identifier); ok {
			script.className = identifier.Name
		}
		setBenchmarkBase(script, directive.Extends)
		return
	}
	if directive.Name == "extends" {
		setBenchmarkBase(script, directive.Value)
	}
}

func setBenchmarkBase(script *benchmarkScript, expression ast.Expression) {
	if expression == nil {
		return
	}
	if literal, ok := expression.(*ast.Literal); ok {
		value := literalString(literal)
		if strings.HasPrefix(value, "res://") {
			script.basePath = normalizeRelative(strings.TrimPrefix(value, "res://"))
		}
		return
	}
	script.baseName = expressionName(expression)
}

func resolveBenchmarkInheritance(scripts map[string]*benchmarkScript) {
	classes := make(map[string]string)
	ambiguous := make(map[string]bool)
	for filename, script := range scripts {
		if script.className == "" {
			continue
		}
		if _, exists := classes[script.className]; exists {
			ambiguous[script.className] = true
		} else {
			classes[script.className] = filename
		}
	}
	state := make(map[string]uint8)
	var resolve func(string) bool
	resolve = func(filename string) bool {
		script := scripts[filename]
		if script == nil {
			return false
		}
		if state[filename] == 2 {
			return script.extends
		}
		if state[filename] == 1 {
			return false
		}
		state[filename] = 1
		base := ""
		switch {
		case script.baseName == "GDTestBenchmark":
			script.extends = true
		case script.basePath != "":
			base = script.basePath
		case script.baseName != "" && !ambiguous[script.baseName]:
			base = classes[script.baseName]
		}
		if base != "" {
			script.extends = resolve(base)
		}
		state[filename] = 2
		return script.extends
	}
	for filename := range scripts {
		resolve(filename)
	}
}

func inspectBenchmarkContracts(script *benchmarkScript) (BenchmarkFile, error) {
	file := BenchmarkFile{Path: script.path}
	for _, function := range script.functions {
		arguments, hook := benchmarkHookArguments[function.Name]
		if hook {
			if err := validateBenchmarkHook(script.path, function, arguments); err != nil {
				return BenchmarkFile{}, err
			}
		}
		if !strings.HasPrefix(function.Name, "benchmark_") {
			continue
		}
		location := function.NameSpan.Start
		if function.Name == "benchmark_" {
			return BenchmarkFile{}, fmt.Errorf("discover %s:%d:%d: benchmark method needs a name after benchmark_", script.path, location.Line, location.Column)
		}
		if len(function.Parameters) != 0 {
			return BenchmarkFile{}, fmt.Errorf("discover %s:%d:%d: benchmark method %s must take no arguments", script.path, location.Line, location.Column, function.Name)
		}
		if function.ReturnType != "void" {
			return BenchmarkFile{}, fmt.Errorf("discover %s:%d:%d: benchmark method %s must return void, got %s", script.path, location.Line, location.Column, function.Name, function.ReturnType)
		}
		file.Benchmarks = append(file.Benchmarks, BenchmarkCase{
			ID:         script.path + "::" + function.Name,
			Name:       displayName(strings.TrimPrefix(function.Name, "benchmark_")),
			MethodName: function.Name, Line: location.Line,
		})
	}
	return file, nil
}

func validateBenchmarkHook(filename string, function *ast.FunctionDeclaration, arguments int) error {
	location := function.NameSpan.Start
	if len(function.Parameters) != arguments {
		return fmt.Errorf("discover %s:%d:%d: hook %s must take %d argument(s), got %d", filename, location.Line, location.Column, function.Name, arguments, len(function.Parameters))
	}
	if arguments == 1 && function.Parameters[0].Type != "GDTestBenchmarkInfo" {
		return fmt.Errorf("discover %s:%d:%d: hook %s argument must be GDTestBenchmarkInfo", filename, location.Line, location.Column, function.Name)
	}
	if function.ReturnType != "void" {
		return fmt.Errorf("discover %s:%d:%d: hook %s must return void, got %s", filename, location.Line, location.Column, function.Name, function.ReturnType)
	}
	return nil
}
