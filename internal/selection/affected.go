package selection

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdtest/internal/discovery"
)

type affectedIndex struct {
	scripts map[string]discovery.ScriptMetadata
	known   map[string]struct{}
	globals map[string]struct{}
}

func selectAffected(
	manifest discovery.Manifest,
	scripts []discovery.ScriptMetadata,
	options AffectedOptions,
) (Result, error) {
	changed, err := normalizePaths(options.Paths, "affected path")
	if err != nil {
		return Result{}, &UsageError{Message: err.Error()}
	}
	if len(changed) == 0 {
		return Result{Manifest: emptyManifest(manifest), Message: "no tests were affected"}, nil
	}
	if options.ConservativeAll {
		selected := cloneManifest(manifest)
		reasons := reasonsForManifest(
			selected,
			ReasonGlobalInput,
			changed[0],
			nil,
			"selected conservatively because project metadata contains an unresolved global input",
		)
		sortReasons(reasons)
		return Result{Manifest: selected, Reasons: reasons}, nil
	}
	globalInputs, err := normalizePaths(options.GlobalInputs, "global input")
	if err != nil {
		return Result{}, &UsageError{Message: err.Error()}
	}
	index := newAffectedIndex(scripts, globalInputs)
	if global := index.globalChange(changed); global != "" {
		selected := cloneManifest(manifest)
		reasons := reasonsForManifest(
			selected,
			ReasonGlobalInput,
			global,
			nil,
			fmt.Sprintf("selected because %s is a project-wide input", global),
		)
		sortReasons(reasons)
		return Result{Manifest: selected, Reasons: reasons}, nil
	}

	result := emptyManifest(manifest)
	reasons := make([]Reason, 0)
	for _, file := range manifest.Files {
		reason, selected := index.reason(file.Path, changed)
		if !selected {
			continue
		}
		result.Files = append(result.Files, cloneFile(file))
		if len(file.Cases) == 0 {
			reasons = append(reasons, reason)
			continue
		}
		for _, testCase := range file.Cases {
			caseReason := reason
			caseReason.TestID = testCase.ID
			reasons = append(reasons, caseReason)
		}
	}
	sortReasons(reasons)
	message := ""
	if len(result.Files) == 0 {
		message = "no tests were affected"
	}
	return Result{Manifest: result, Reasons: reasons, Message: message}, nil
}

func newAffectedIndex(scripts []discovery.ScriptMetadata, globalInputs []string) affectedIndex {
	index := affectedIndex{
		scripts: make(map[string]discovery.ScriptMetadata, len(scripts)),
		known:   make(map[string]struct{}, len(scripts)),
		globals: make(map[string]struct{}, len(globalInputs)),
	}
	for _, script := range scripts {
		name := normalizePath(script.Path)
		index.scripts[name] = script
		index.known[name] = struct{}{}
		if script.ClassName != "" {
			index.globals[name] = struct{}{}
		}
		for _, dependency := range script.Dependencies {
			index.known[normalizePath(dependency)] = struct{}{}
		}
	}
	for _, input := range globalInputs {
		index.globals[input] = struct{}{}
	}
	return index
}

func (index affectedIndex) globalChange(changed []string) string {
	for _, name := range changed {
		if alwaysGlobal(name) {
			return name
		}
		for global := range index.globals {
			if pathMatchesChange(global, name, index.known) {
				return name
			}
		}
		if _, known := index.known[name]; !known && unknownRuntimeInput(name) {
			return name
		}
	}
	return ""
}

func unknownRuntimeInput(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".gd", ".tscn", ".tres", ".res", ".cfg", ".json":
		return true
	default:
		return false
	}
}

func alwaysGlobal(name string) bool {
	lower := strings.ToLower(name)
	base := path.Base(lower)
	if base == "project.godot" || base == "gdtest.json" || base == "addons.toml" || base == "addons.lock" {
		return true
	}
	if strings.HasPrefix(lower, "addons/gdtest/") || strings.HasSuffix(lower, ".gdextension") {
		return true
	}
	switch path.Ext(lower) {
	case ".so", ".dylib", ".dll":
		return true
	default:
		return false
	}
}

func (index affectedIndex) reason(filename string, changed []string) (Reason, bool) {
	filename = normalizePath(filename)
	for _, change := range changed {
		if pathMatchesChange(filename, change, index.known) {
			return Reason{
				File: filename, Kind: ReasonDirectDependency, Changed: change,
				Chain: []string{filename}, Message: fmt.Sprintf("selected because %s changed", change),
			}, true
		}
	}

	type node struct {
		path  string
		chain []string
	}
	queue := []node{{path: filename, chain: []string{filename}}}
	visited := make(map[string]struct{})
	dynamicPath := ""
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, seen := visited[current.path]; seen {
			continue
		}
		visited[current.path] = struct{}{}
		metadata, exists := index.scripts[current.path]
		if !exists {
			continue
		}
		if metadata.HasDynamicDependency && dynamicPath == "" {
			dynamicPath = current.path
		}
		dependencies := append([]string(nil), metadata.Dependencies...)
		for dependencyIndex := range dependencies {
			dependencies[dependencyIndex] = normalizePath(dependencies[dependencyIndex])
		}
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			chain := append(append([]string(nil), current.chain...), dependency)
			for _, change := range changed {
				if pathMatchesChange(dependency, change, index.known) {
					kind := ReasonTransitiveDependency
					if len(chain) == 2 {
						kind = ReasonDirectDependency
					}
					return Reason{
						File: filename, Kind: kind, Changed: change, Chain: chain,
						Message: fmt.Sprintf(
							"selected because %s depends on %s through %s",
							filename,
							change,
							strings.Join(chain, " -> "),
						),
					}, true
				}
			}
			queue = append(queue, node{path: dependency, chain: chain})
		}
	}
	if dynamicPath != "" {
		return Reason{
			File: filename, Kind: ReasonDynamicDependency, Changed: changed[0],
			Chain: []string{filename, dynamicPath},
			Message: fmt.Sprintf(
				"selected conservatively because %s has an unresolved dynamic load",
				dynamicPath,
			),
		}, true
	}
	return Reason{}, false
}

func normalizePaths(values []string, label string) ([]string, error) {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%s must not be empty", label)
		}
		value = strings.ReplaceAll(value, `\`, "/")
		if strings.HasPrefix(value, "/") || hasWindowsVolume(value) {
			return nil, fmt.Errorf("%s %q must be project-relative", label, value)
		}
		clean, err := cleanRelative(value)
		if err != nil {
			return nil, err
		}
		unique[clean] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func pathMatchesChange(candidate, changed string, known map[string]struct{}) bool {
	if candidate == changed {
		return true
	}
	if strings.HasSuffix(changed, "/") {
		return strings.HasPrefix(candidate, strings.TrimSuffix(changed, "/")+"/")
	}
	for knownPath := range known {
		if strings.HasPrefix(knownPath, changed+"/") {
			return strings.HasPrefix(candidate, changed+"/")
		}
	}
	return false
}

func reasonsForManifest(
	manifest discovery.Manifest,
	kind ReasonKind,
	changed string,
	chain []string,
	message string,
) []Reason {
	reasons := make([]Reason, 0)
	for _, file := range manifest.Files {
		if len(file.Cases) == 0 {
			reasons = append(reasons, Reason{
				File: file.Path, Kind: kind, Changed: changed,
				Chain: append([]string(nil), chain...), Message: message,
			})
			continue
		}
		for _, testCase := range file.Cases {
			reasons = append(reasons, Reason{
				TestID: testCase.ID, File: file.Path, Kind: kind, Changed: changed,
				Chain: append([]string(nil), chain...), Message: message,
			})
		}
	}
	return reasons
}
