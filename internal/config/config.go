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
	Exclude           []string     `json:"exclude"`
	Godot             string       `json:"godot"`
	Jobs              int          `json:"jobs"`
	WarmWorkers       int          `json:"warm_workers"`
	WorkerIdleTimeout Duration     `json:"worker_idle_timeout"`
	WorkerMaxRuns     int          `json:"worker_max_runs"`
	OnlyMode          string       `json:"only_mode"`
	TestDoubles       []TestDouble `json:"test_doubles"`
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
		Exclude:           []string{".godot/**"},
		Godot:             "godot",
		Jobs:              0,
		WarmWorkers:       1,
		WorkerIdleTimeout: Duration(2 * time.Minute),
		WorkerMaxRuns:     100,
		OnlyMode:          "auto",
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
	return validateTestDoubles(c.TestDoubles)
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
		if strings.HasPrefix(double.Output, generatedDoubleRuntimePrefix) {
			return fmt.Errorf("test_doubles[%d].output: must not be under %s", index, generatedDoubleRuntimePrefix)
		}
		if first, exists := sources[double.Source]; exists {
			return fmt.Errorf("test_doubles[%d].source: %q duplicates test_doubles[%d].source", index, double.Source, first)
		}
		sources[double.Source] = index
		if first, exists := outputs[double.Output]; exists {
			return fmt.Errorf("test_doubles[%d].output: %q duplicates test_doubles[%d].output", index, double.Output, first)
		}
		outputs[double.Output] = index
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
	for index, double := range doubles {
		if first, exists := sources[double.Output]; exists {
			return fmt.Errorf("test_doubles[%d].output: %q is also test_doubles[%d].source", index, double.Output, first)
		}
	}
	return nil
}

func validateDoubleProjectPath(index int, field string, value string) error {
	location := fmt.Sprintf("test_doubles[%d].%s", index, field)
	switch {
	case value == "":
		return fmt.Errorf("%s: must not be empty", location)
	case strings.Contains(value, `\`):
		return fmt.Errorf("%s: %q must use forward slashes", location, value)
	case path.IsAbs(value) || (len(value) >= 2 && value[1] == ':'):
		return fmt.Errorf("%s: %q must be project-relative", location, value)
	case path.Clean(value) != value:
		return fmt.Errorf("%s: %q must be a clean relative path", location, value)
	case slices.Contains(strings.Split(value, "/"), ".."):
		return fmt.Errorf("%s: %q must not contain a parent directory segment", location, value)
	case !strings.HasSuffix(value, ".gd"):
		return fmt.Errorf("%s: %q must end in .gd", location, value)
	}
	return nil
}
