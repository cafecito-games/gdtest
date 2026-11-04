//go:build windows

package daemon

import (
	"fmt"
	"os"
)

// Windows daemon transport is loopback TCP in the final platform adapter. This
// exclusive-create lock keeps development cross-builds safe without a native dependency.
func acquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if os.IsExist(err) {
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("create daemon lock: %w", err)
	}
	return &Lock{file: file}, nil
}

func releaseLock(file *os.File) error {
	name := file.Name()
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(name)
}
