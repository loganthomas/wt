//go:build unix

package lease

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether pid exists. Signal 0 delivers nothing and
// only checks existence; EPERM means the process exists but is
// someone else's, which still counts as alive.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processStart returns pid's start time as ps prints it
// (e.g. "Mon Jul 20 09:15:02 2026"). The value is only ever
// compared for equality against a recorded copy, so its format
// does not matter — only that a reused PID prints a different one.
// ps is POSIX and present on both v1 platforms (macOS, Linux CI);
// shelling out beats per-OS sysctl/procfs code for a call made a
// handful of times per command.
func processStart(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	start := strings.TrimSpace(string(out))
	if start == "" {
		return "", fmt.Errorf("ps -p %d: no such process", pid)
	}
	return start, nil
}
