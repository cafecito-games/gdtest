// Package config loads strict project configuration and applies safe defaults.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	Tests             []string `json:"tests"`
	Exclude           []string `json:"exclude"`
	Godot             string   `json:"godot"`
	Jobs              int      `json:"jobs"`
	WarmWorkers       int      `json:"warm_workers"`
	WorkerIdleTimeout Duration `json:"worker_idle_timeout"`
	WorkerMaxRuns     int      `json:"worker_max_runs"`
	OnlyMode          string   `json:"only_mode"`
}

// Defaults returns the conventional, conservative project policy.
func Defaults() Project {
	jobs := min(runtime.NumCPU(), 4)
	return Project{
		Tests:             []string{"**/*.test.gd"},
		Exclude:           []string{".godot/**"},
		Godot:             "godot",
		Jobs:              jobs,
		WarmWorkers:       1,
		WorkerIdleTimeout: Duration(2 * time.Minute),
		WorkerMaxRuns:     100,
		OnlyMode:          "auto",
	}
}

// Load reads gdtest.json from root, returning defaults when it does not exist.
func Load(root string) (Project, error) {
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Project{}, fmt.Errorf("read %s: %w", path, err)
	}
	config := Defaults()
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
		return nil
	default:
		return fmt.Errorf("only_mode %q must be auto, ignore, require, or forbid", c.OnlyMode)
	}
}
