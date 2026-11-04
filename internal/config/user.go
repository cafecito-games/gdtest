package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// UserFilename is the user-level resource-policy configuration filename.
const UserFilename = "config.json"

// User is the user-scoped daemon and default-executable policy. Projects may
// request fewer resources but cannot raise these global bounds.
type User struct {
	MaxWorkers     int      `json:"max_workers"`
	MemoryBudgetMB int      `json:"memory_budget_mb"`
	WorkerMaxAge   Duration `json:"worker_max_age"`
	LogLevel       string   `json:"log_level"`
	Godot          string   `json:"godot"`
}

// UserDefaults returns conservative built-in user policy.
func UserDefaults() User {
	return User{
		MaxWorkers: 4, WorkerMaxAge: Duration(30 * time.Minute),
		LogLevel: "info", Godot: "godot",
	}
}

// UserPath returns the conventional per-user configuration path.
func UserPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user configuration directory: %w", err)
	}
	return filepath.Join(root, "gdtest", UserFilename), nil
}

// LoadUser loads strict user-level configuration, or built-in defaults when
// no file exists.
func LoadUser() (User, error) {
	filename, err := UserPath()
	if err != nil {
		return User{}, err
	}
	return loadUserFile(filename)
}

func loadUserFile(filename string) (User, error) {
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return UserDefaults(), nil
	}
	if err != nil {
		return User{}, fmt.Errorf("read %s: %w", filename, err)
	}
	result := UserDefaults()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return User{}, fmt.Errorf("parse %s: %w", filename, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return User{}, fmt.Errorf("parse %s: unexpected content after configuration", filename)
	}
	if err := result.Validate(); err != nil {
		return User{}, fmt.Errorf("validate %s: %w", filename, err)
	}
	return result, nil
}

// Validate rejects unsafe or nonsensical global policy.
func (c User) Validate() error {
	if c.MaxWorkers < 1 {
		return errors.New("max_workers must be positive")
	}
	if c.MemoryBudgetMB < 0 {
		return errors.New("memory_budget_mb must be non-negative")
	}
	if time.Duration(c.WorkerMaxAge) <= 0 {
		return errors.New("worker_max_age must be positive")
	}
	if c.Godot == "" {
		return errors.New("godot must not be empty")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("log_level %q must be debug, info, warn, or error", c.LogLevel)
	}
}
