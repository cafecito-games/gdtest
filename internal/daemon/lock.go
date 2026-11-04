package daemon

import (
	"errors"
	"os"
)

// ErrAlreadyRunning reports that another process owns the daemon namespace.
var ErrAlreadyRunning = errors.New("compatible daemon is already running")

// Lock is an exclusive process-lifetime namespace lock.
type Lock struct{ file *os.File }

// AcquireLock takes the namespace lock without waiting.
func AcquireLock(path string) (*Lock, error) { return acquireLock(path) }

// Close releases the namespace lock.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := releaseLock(l.file)
	l.file = nil
	return err
}
