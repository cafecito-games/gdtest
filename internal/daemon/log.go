package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger writes structured, bounded JSONL daemon diagnostics.
type Logger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	minimum  logLevel
	file     *os.File
}

type logLevel uint8

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
)

// OpenLogger opens a current-user-only log. Non-positive limits use 8 MiB.
func OpenLogger(path string, maxBytes int64) (*Logger, error) {
	return OpenLoggerWithLevel(path, maxBytes, "info")
}

// OpenLoggerWithLevel opens a bounded log that drops records below minimum.
func OpenLoggerWithLevel(path string, maxBytes int64, minimum string) (*Logger, error) {
	parsedMinimum, err := parseLogLevel(minimum)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure daemon log: %w", err)
	}
	return &Logger{path: path, maxBytes: maxBytes, minimum: parsedMinimum, file: file}, nil
}

// Write records fields with a UTC timestamp. Authentication values must not be passed here.
func (l *Logger) Write(fields map[string]any) error {
	if l == nil {
		return nil
	}
	name := "info"
	if value, exists := fields["level"]; exists {
		text, ok := value.(string)
		if !ok {
			return errors.New("daemon log level must be a string")
		}
		name = text
	}
	level, err := parseLogLevel(name)
	if err != nil {
		return err
	}
	if level < l.minimum {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	record := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		record[key] = value
	}
	record["level"] = name
	record["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode daemon log: %w", err)
	}
	line = append(line, '\n')
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect daemon log: %w", err)
	}
	if info.Size()+int64(len(line)) > l.maxBytes {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	if _, err := l.file.Write(line); err != nil {
		return fmt.Errorf("write daemon log: %w", err)
	}
	return nil
}

func parseLogLevel(value string) (logLevel, error) {
	switch value {
	case "debug":
		return levelDebug, nil
	case "info":
		return levelInfo, nil
	case "warn":
		return levelWarn, nil
	case "error":
		return levelError, nil
	default:
		return 0, fmt.Errorf("invalid daemon log level %q", value)
	}
}

func (l *Logger) rotate() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close daemon log for rotation: %w", err)
	}
	backup := l.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(l.path, backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate daemon log: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open rotated daemon log: %w", err)
	}
	l.file = file
	return nil
}

// Close flushes and closes the log.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
