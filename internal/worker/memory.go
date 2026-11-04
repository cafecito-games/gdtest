package worker

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ResidentMemoryBytes returns the resident-set size of a live worker process.
// gdtest release targets provide the POSIX ps interface on macOS and Linux.
func ResidentMemoryBytes(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid worker PID %d", pid)
	}
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("read resident memory for worker %d: %w", pid, err)
	}
	kibibytes, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse resident memory for worker %d: %w", pid, err)
	}
	return kibibytes * 1024, nil
}
