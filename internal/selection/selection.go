// Package selection applies user selectors and conservative affected-test
// analysis to path-neutral discovery manifests.
package selection

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/model"
)

// ReasonKind identifies why one affected test was retained.
type ReasonKind string

const (
	ReasonDirectDependency     ReasonKind = "direct_dependency"
	ReasonTransitiveDependency ReasonKind = "transitive_dependency"
	ReasonDynamicDependency    ReasonKind = "dynamic_dependency"
	ReasonGlobalInput          ReasonKind = "global_input"
)

// Reason is a deterministic explanation for affected-test selection.
type Reason struct {
	TestID  string     `json:"test_id"`
	File    string     `json:"file"`
	Kind    ReasonKind `json:"kind"`
	Changed string     `json:"changed"`
	Chain   []string   `json:"chain,omitempty"`
	Message string     `json:"message"`
}

// AffectedOptions enables conservative dependency selection. A non-nil value
// enables affected mode even when Paths is empty. GlobalInputs should contain
// project-metadata inputs such as resolved autoload scripts.
type AffectedOptions struct {
	Paths           []string
	GlobalInputs    []string
	ConservativeAll bool
}

// Options composes positional selectors, an explicit case-insensitive name
// substring filter, and optional affected-test selection. Enabled filters
// intersect; positional selectors themselves are a union.
type Options struct {
	Selectors  []string
	Name       string
	AllowEmpty bool
	Affected   *AffectedOptions
}

// Result contains the selected manifest and stable affected explanations.
type Result struct {
	Manifest discovery.Manifest `json:"manifest"`
	Reasons  []Reason           `json:"reasons,omitempty"`
	Message  string             `json:"message,omitempty"`
}

// UsageError reports an invalid or unknown user selector.
type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }

// CaseInfo is the selector-visible identity of either a static or dynamically
// discovered case.
type CaseInfo struct {
	ID         string
	Name       string
	MethodName string
}

// Group preserves one file-level scheduling unit and its source-ordered cases.
type Group[T any] struct {
	Path  string
	Cases []T
}

// MatchOptions controls path/ID and explicit name matching independently of
// dependency analysis.
type MatchOptions struct {
	Selectors  []string
	Name       string
	AllowEmpty bool
}

// FilterGroups applies identical selector semantics to any static/dynamic case
// representation while preserving file and case order.
func FilterGroups[T any](
	groups []Group[T],
	options MatchOptions,
	describe func(T) CaseInfo,
) ([]Group[T], error) {
	if describe == nil {
		return nil, fmt.Errorf("selection case descriptor is required")
	}
	normalized := make([]normalizedSelector, len(options.Selectors))
	for index, selector := range options.Selectors {
		value, err := normalizeSelector(selector)
		if err != nil {
			return nil, &UsageError{Message: err.Error()}
		}
		normalized[index] = value
	}
	known := make([]bool, len(normalized))
	query := strings.ToLower(options.Name)
	result := make([]Group[T], 0, len(groups))
	for _, group := range groups {
		filePath := normalizePath(group.Path)
		selectAll := len(normalized) == 0
		for index, selector := range normalized {
			matches, err := selector.matchesFile(filePath)
			if err != nil {
				return nil, &UsageError{Message: fmt.Sprintf("invalid selector %q: %v", options.Selectors[index], err)}
			}
			if matches {
				known[index] = true
				selectAll = true
			}
		}
		selectedCases := make([]T, 0, len(group.Cases))
		for _, testCase := range group.Cases {
			info := describe(testCase)
			selectedByID := false
			for index, selector := range normalized {
				if selector.stableID != "" && selector.stableID == normalizeStableID(info.ID) {
					known[index] = true
					selectedByID = true
				}
			}
			if !selectAll && !selectedByID {
				continue
			}
			if query != "" && !caseNameMatches(info, query) {
				continue
			}
			selectedCases = append(selectedCases, testCase)
		}
		if len(selectedCases) > 0 || (selectAll && len(group.Cases) == 0 && query == "") {
			result = append(result, Group[T]{Path: group.Path, Cases: selectedCases})
		}
	}
	unknown := make([]string, 0)
	for index, matched := range known {
		if !matched {
			unknown = append(unknown, options.Selectors[index])
		}
	}
	if len(unknown) > 0 && !options.AllowEmpty {
		return nil, &UsageError{Message: "unknown selector(s): " + strings.Join(unknown, ", ")}
	}
	if len(result) == 0 && query != "" && !options.AllowEmpty {
		return nil, &UsageError{Message: fmt.Sprintf("name filter %q matched no tests", options.Name)}
	}
	return result, nil
}

func caseNameMatches(info CaseInfo, query string) bool {
	return strings.Contains(strings.ToLower(info.Name), query) ||
		strings.Contains(strings.ToLower(info.MethodName), query) ||
		strings.Contains(strings.ToLower(info.ID), query)
}

// Apply selects tests without changing manifest ordering or content identity.
func Apply(manifest discovery.Manifest, options Options) (Result, error) {
	groups := make([]Group[model.CasePlan], 0, len(manifest.Files))
	for _, file := range manifest.Files {
		groups = append(groups, Group[model.CasePlan]{Path: file.Path, Cases: file.Cases})
	}
	filtered, err := FilterGroups(groups, MatchOptions{
		Selectors: options.Selectors, Name: options.Name, AllowEmpty: options.AllowEmpty,
	}, func(testCase model.CasePlan) CaseInfo {
		return CaseInfo{ID: testCase.ID, Name: testCase.Name, MethodName: testCase.MethodName}
	})
	if err != nil {
		return Result{}, err
	}
	selected := emptyManifest(manifest)
	for _, group := range filtered {
		selected.Files = append(selected.Files, model.FilePlan{Path: group.Path, Cases: group.Cases})
	}
	if options.Affected != nil {
		selectedResult, affectedErr := selectAffected(selected, manifest.Scripts, *options.Affected)
		if affectedErr != nil {
			return Result{}, affectedErr
		}
		return selectedResult, nil
	}
	return Result{Manifest: selected}, nil
}

type normalizedSelector struct {
	path     string
	stableID string
	glob     bool
}

func normalizeSelector(selector string) (normalizedSelector, error) {
	selector = strings.ReplaceAll(selector, `\`, "/")
	if selector == "" {
		return normalizedSelector{}, fmt.Errorf("selector must not be empty")
	}
	if strings.HasPrefix(selector, "/") || hasWindowsVolume(selector) {
		return normalizedSelector{}, fmt.Errorf("selector %q must be project-relative", selector)
	}
	if before, after, ok := strings.Cut(selector, "::"); ok {
		clean, err := cleanRelative(before)
		if err != nil {
			return normalizedSelector{}, err
		}
		if after == "" || strings.ContainsAny(clean, "*?[") {
			return normalizedSelector{}, fmt.Errorf("invalid stable test selector %q", selector)
		}
		return normalizedSelector{stableID: clean + "::" + after}, nil
	}
	clean, err := cleanRelative(selector)
	if err != nil {
		return normalizedSelector{}, err
	}
	isGlob := strings.ContainsAny(clean, "*?[")
	if isGlob {
		for _, part := range strings.Split(clean, "/") {
			if part == "**" {
				continue
			}
			if _, matchErr := path.Match(part, ""); matchErr != nil {
				return normalizedSelector{}, fmt.Errorf("invalid selector %q: %v", selector, matchErr)
			}
		}
	}
	return normalizedSelector{path: clean, glob: isGlob}, nil
}

func (s normalizedSelector) matchesFile(filename string) (bool, error) {
	if s.stableID != "" {
		return false, nil
	}
	if s.glob {
		return matchGlob(s.path, filename)
	}
	return s.path == "." || filename == s.path || strings.HasPrefix(filename, strings.TrimSuffix(s.path, "/")+"/"), nil
}

func matchGlob(pattern, filename string) (bool, error) {
	patternParts := strings.Split(pattern, "/")
	filenameParts := strings.Split(filename, "/")
	var match func(int, int) (bool, error)
	match = func(patternIndex, filenameIndex int) (bool, error) {
		if patternIndex == len(patternParts) {
			return filenameIndex == len(filenameParts), nil
		}
		if patternParts[patternIndex] == "**" {
			for next := filenameIndex; next <= len(filenameParts); next++ {
				matched, err := match(patternIndex+1, next)
				if err != nil || matched {
					return matched, err
				}
			}
			return false, nil
		}
		if filenameIndex == len(filenameParts) {
			return false, nil
		}
		matched, err := path.Match(patternParts[patternIndex], filenameParts[filenameIndex])
		if err != nil || !matched {
			return false, err
		}
		return match(patternIndex+1, filenameIndex+1)
	}
	return match(0, 0)
}

func cleanRelative(value string) (string, error) {
	value = strings.TrimPrefix(value, "res://")
	value = strings.TrimPrefix(value, "./")
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("selector %q escapes the project root", value)
	}
	return clean, nil
}

func hasWindowsVolume(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && value[2] == '/'
}

func normalizePath(value string) string {
	clean, _ := cleanRelative(strings.ReplaceAll(value, `\`, "/"))
	return clean
}

func normalizeStableID(value string) string {
	before, after, ok := strings.Cut(strings.ReplaceAll(value, `\`, "/"), "::")
	if !ok {
		return value
	}
	return normalizePath(before) + "::" + after
}

func emptyManifest(manifest discovery.Manifest) discovery.Manifest {
	return discovery.Manifest{
		Scripts:               manifest.Scripts,
		StructuralFingerprint: manifest.StructuralFingerprint,
		ContentFingerprint:    manifest.ContentFingerprint,
	}
}

func cloneManifest(manifest discovery.Manifest) discovery.Manifest {
	result := emptyManifest(manifest)
	result.Files = make([]model.FilePlan, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		result.Files = append(result.Files, cloneFile(file))
	}
	return result
}

func cloneFile(file model.FilePlan) model.FilePlan {
	return model.FilePlan{Path: file.Path, Cases: append([]model.CasePlan(nil), file.Cases...)}
}

func sortReasons(reasons []Reason) {
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].File != reasons[j].File {
			return reasons[i].File < reasons[j].File
		}
		if reasons[i].TestID != reasons[j].TestID {
			return reasons[i].TestID < reasons[j].TestID
		}
		if reasons[i].Changed != reasons[j].Changed {
			return reasons[i].Changed < reasons[j].Changed
		}
		return reasons[i].Kind < reasons[j].Kind
	})
}
