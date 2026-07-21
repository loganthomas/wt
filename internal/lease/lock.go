//go:build unix

package lease

import (
	"os"
	"syscall"
)

// lockExclusive takes a blocking exclusive flock on path,
// creating the file as needed, and returns the release function.
// Blocking is right here: the guarded section is a few filesystem
// operations, so waiting beats surfacing spurious contention
// errors. The kernel releases the lock if its holder dies,
// so a crash can never wedge later claimers.
func lockExclusive(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	// Closing the descriptor releases the flock.
	return func() { _ = f.Close() }, nil
}
