//go:build unix

package lease

import (
	"errors"
	"fmt"
	"os"
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
// does not matter, only that a reused PID prints a different one
// and the same process always prints the same one: TZ and locale
// are pinned, because ps renders lstart in local time and a claim
// written under one TZ must not read as a different process under
// another (that misread would steal a live lease). lstart is a
// BSD/GNU extension, not POSIX, but present on both v1 platforms
// (macOS, Linux CI); where ps lacks it the guard degrades to
// PID-liveness alone, which fails safe: never steals.
// Shelling out beats per-OS sysctl/procfs code for a call made a
// handful of times per command.
func processStart(pid int) (string, error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	start := strings.TrimSpace(string(out))
	if start == "" {
		return "", fmt.Errorf("ps -p %d: no such process", pid)
	}
	return start, nil
}
