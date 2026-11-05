// Package config loads strict project configuration and applies safe defaults.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cafecito-games/gdtest/internal/model"
)

// Filename is the optional project configuration file.
const Filename = "gdtest.json"

// Duration is a JSON duration string.
type Duration time.Duration

// UnmarshalJSON parses a quoted time.Duration.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("duration must be a string")
	}
	value, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", text, err)
	}
	*d = Duration(value)
	return nil
}

// MarshalJSON emits a quoted time.Duration.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Project is the strict project-level configuration.
type Project struct {
	Tests             []string     `json:"tests"`
	Benchmarks        []string     `json:"benchmarks"`
	Exclude           []string     `json:"exclude"`
	Godot             string       `json:"godot"`
	Jobs              int          `json:"jobs"`
	WarmWorkers       int          `json:"warm_workers"`
	WorkerIdleTimeout Duration     `json:"worker_idle_timeout"`
	WorkerMaxRuns     int          `json:"worker_max_runs"`
	OnlyMode          string       `json:"only_mode"`
	LeakCheck         LeakCheck    `json:"leak_check"`
	TestDoubles       []TestDouble `json:"test_doubles"`
}

// LeakCheck configures post-scope node leak detection and exemptions.
type LeakCheck struct {
	Policy string   `json:"policy"`
	Ignore []string `json:"ignore,omitempty"`
}

// ToModel returns a backend-neutral copy of the leak configuration.
func (l LeakCheck) ToModel() model.LeakCheck {
	var ignore []string
	if l.Ignore != nil {
		ignore = append([]string(nil), l.Ignore...)
	}
	return model.LeakCheck{Policy: l.Policy, Ignore: ignore}
}

// UnmarshalJSON strictly decodes leak_check, defaulting policy to "fail" only
// when the policy field is omitted while rejecting null or non-object values.
func (l *LeakCheck) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("leak_check must be an object")
	}
	var raw struct {
		Policy json.RawMessage `json:"policy"`
		Ignore json.RawMessage `json:"ignore"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected content after leak_check")
	}
	policy := "fail"
	if len(raw.Policy) > 0 {
		policyBytes := bytes.TrimSpace(raw.Policy)
		if bytes.Equal(policyBytes, []byte("null")) {
			return errors.New("leak_check.policy must be a string")
		}
		if err := json.Unmarshal(policyBytes, &policy); err != nil {
			return errors.New("leak_check.policy must be a string")
		}
	}
	var ignore []string
	if len(raw.Ignore) > 0 {
		ignoreBytes := bytes.TrimSpace(raw.Ignore)
		if bytes.Equal(ignoreBytes, []byte("null")) {
			return errors.New("leak_check.ignore must be an array")
		}
		var rawEntries []*string
		if err := json.Unmarshal(ignoreBytes, &rawEntries); err != nil {
			return errors.New("leak_check.ignore must be an array of strings")
		}
		ignore = make([]string, len(rawEntries))
		for index, entry := range rawEntries {
			if entry == nil {
				return fmt.Errorf("leak_check.ignore[%d]: must be a string", index)
			}
			ignore[index] = *entry
		}
	}
	*l = LeakCheck{Policy: policy, Ignore: ignore}
	return nil
}

// TestDouble requests a generated typed test double of one project script.
//
// Source is the project-relative path of the script to double, and Output is
// the project-relative path of the generated script. ClassName names the
// generated class and may be empty, in which case the generated script has no
// class_name.
type TestDouble struct {
	Source    string `json:"source"`
	Output    string `json:"output"`
	ClassName string `json:"class_name"`
}

// Defaults returns the conventional, conservative project policy.
func Defaults() Project {
	return Project{
		Tests:             []string{"**/*.test.gd"},
		Benchmarks:        []string{"**/*.bench.gd"},
		Exclude:           []string{".godot/**"},
		Godot:             "godot",
		Jobs:              0,
		WarmWorkers:       1,
		WorkerIdleTimeout: Duration(2 * time.Minute),
		WorkerMaxRuns:     100,
		OnlyMode:          "auto",
		LeakCheck:         LeakCheck{Policy: "fail"},
	}
}

// Load reads gdtest.json from root, returning defaults when it does not exist.
func Load(root string) (Project, error) {
	return loadProject(root, Defaults())
}

// LoadWithUser reads project configuration over user-level defaults.
func LoadWithUser(root string, user User) (Project, error) {
	defaults := Defaults()
	defaults.Godot = user.Godot
	return loadProject(root, defaults)
}

func loadProject(root string, defaults Project) (Project, error) {
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return Project{}, fmt.Errorf("read %s: %w", path, err)
	}
	config := defaults
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Project{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Project{}, fmt.Errorf("parse %s: unexpected content after configuration", path)
	}
	if err := config.Validate(); err != nil {
		return Project{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return config, nil
}

var classNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// generatedDoubleRuntimePrefix is the directory of the shipped runtime, which
// generated doubles must never overwrite.
const generatedDoubleRuntimePrefix = "addons/gdtest/"

// Validate rejects unsafe or nonsensical project policy.
func (c Project) Validate() error {
	if len(c.Tests) == 0 {
		return errors.New("tests must contain at least one pattern")
	}
	if len(c.Benchmarks) == 0 {
		return errors.New("benchmarks must contain at least one pattern")
	}
	if c.Godot == "" {
		return errors.New("godot must not be empty")
	}
	if c.Jobs < 0 || c.WarmWorkers < 0 || c.WorkerMaxRuns < 1 {
		return errors.New("jobs and warm_workers must be non-negative; worker_max_runs must be positive")
	}
	if time.Duration(c.WorkerIdleTimeout) <= 0 {
		return errors.New("worker_idle_timeout must be positive")
	}
	switch c.OnlyMode {
	case "auto", "ignore", "require", "forbid":
	default:
		return fmt.Errorf("only_mode %q must be auto, ignore, require, or forbid", c.OnlyMode)
	}
	if err := validateLeakCheck(c.LeakCheck); err != nil {
		return err
	}
	return validateTestDoubles(c.TestDoubles)
}

func validateLeakCheck(leakCheck LeakCheck) error {
	switch leakCheck.Policy {
	case "fail", "warn", "off":
	default:
		return fmt.Errorf("leak_check.policy %q must be fail, warn, or off", leakCheck.Policy)
	}
	return leakCheck.ToModel().Validate()
}

func validateTestDoubles(doubles []TestDouble) error {
	sources := make(map[string]int, len(doubles))
	outputs := make(map[string]int, len(doubles))
	classNames := make(map[string]int, len(doubles))
	for index, double := range doubles {
		if err := validateDoubleProjectPath(index, "source", double.Source); err != nil {
			return err
		}
		if err := validateDoubleProjectPath(index, "output", double.Output); err != nil {
			return err
		}
		if strings.HasPrefix(strings.ToLower(double.Output), generatedDoubleRuntimePrefix) {
			return fmt.Errorf("test_doubles[%d].output: must not be under %s", index, generatedDoubleRuntimePrefix)
		}
		if first, exists := sources[double.Source]; exists {
			return fmt.Errorf("test_doubles[%d].source: %q duplicates test_doubles[%d].source", index, double.Source, first)
		}
		sources[double.Source] = index
		// Outputs are compared case-insensitively because two paths differing only
		// in case name one file on case-insensitive filesystems such as macOS.
		folded := strings.ToLower(double.Output)
		if first, exists := outputs[folded]; exists {
			return fmt.Errorf("test_doubles[%d].output: %q duplicates test_doubles[%d].output", index, double.Output, first)
		}
		outputs[folded] = index
		if double.ClassName == "" {
			continue
		}
		if !classNamePattern.MatchString(double.ClassName) {
			return fmt.Errorf("test_doubles[%d].class_name: %q is not a valid GDScript identifier", index, double.ClassName)
		}
		if first, exists := classNames[double.ClassName]; exists {
			return fmt.Errorf("test_doubles[%d].class_name: %q duplicates test_doubles[%d].class_name", index, double.ClassName, first)
		}
		classNames[double.ClassName] = index
	}
	foldedSources := make(map[string]int, len(doubles))
	for index, double := range doubles {
		if _, exists := foldedSources[strings.ToLower(double.Source)]; !exists {
			foldedSources[strings.ToLower(double.Source)] = index
		}
	}
	for index, double := range doubles {
		if first, exists := foldedSources[strings.ToLower(double.Output)]; exists {
			return fmt.Errorf("test_doubles[%d].output: %q is also test_doubles[%d].source", index, double.Output, first)
		}
	}
	return nil
}

// unsafeDoublePathSegments are directories that the ownership index never reads,
// so an output placed beneath one could never be found again.
var unsafeDoublePathSegments = []string{".godot", ".git"}

func validateDoubleProjectPath(index int, field string, value string) error {
	location := fmt.Sprintf("test_doubles[%d].%s", index, field)
	switch {
	case value == "":
		return fmt.Errorf("%s: must not be empty", location)
	case strings.ContainsFunc(value, isEmbeddingUnsafeRune):
		return fmt.Errorf("%s: %q must not contain a double quote or control character", location, value)
	case strings.TrimSpace(value) != value:
		return fmt.Errorf("%s: %q must not begin or end with whitespace", location, value)
	case strings.Contains(value, `\`):
		return fmt.Errorf("%s: %q must use forward slashes", location, value)
	case path.IsAbs(value) || (len(value) >= 2 && value[1] == ':'):
		return fmt.Errorf("%s: %q must be project-relative", location, value)
	case path.Clean(value) != value:
		return fmt.Errorf("%s: %q must be a clean relative path", location, value)
	case slices.Contains(strings.Split(value, "/"), ".."):
		return fmt.Errorf("%s: %q must not contain a parent directory segment", location, value)
	case slices.ContainsFunc(strings.Split(value, "/"), func(segment string) bool {
		return slices.Contains(unsafeDoublePathSegments, strings.ToLower(segment))
	}):
		return fmt.Errorf("%s: %q must not contain a .godot or .git segment", location, value)
	case !strings.HasSuffix(value, ".gd"):
		return fmt.Errorf("%s: %q must end in .gd", location, value)
	}
	return nil
}

// isEmbeddingUnsafeRune reports whether r would break a GDScript string literal
// or the ownership marker line that embeds a double path.
func isEmbeddingUnsafeRune(r rune) bool {
	return r == '"' || r < 0x20 || r == 0x7f
}
