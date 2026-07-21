// Package lease implements crash-safe claims on pool slots
// (PLAN.md D15). A lease is an atomically created directory under
// the repo's state dir whose record names the claiming session:
// PID, process start time, hostname, branch, claim time.
// A lease goes stale only when its PID is dead or its start time
// no longer matches (PID reuse) — never by wall clock alone,
// so long-running legitimate work is never reaped (R3).
//
// The recorded PID is wt's parent — the shell, script, or agent
// session doing the work — because wt itself exits within
// milliseconds of claiming.
package lease

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// recordName is the record file inside a lease directory
// (PLAN.md, State layout: leases/pool-3/lease.toml).
const recordName = "lease.toml"

// Info is the record inside a lease directory.
type Info struct {
	PID       int       `toml:"pid"`
	PIDStart  string    `toml:"pid_start"`
	Hostname  string    `toml:"hostname"`
	Branch    string    `toml:"branch"`
	ClaimedAt time.Time `toml:"claimed_at"`
}

// HeldError reports a slot whose lease is live — or unverifiable,
// which wt treats the same way: it only ever steals a lease it can
// prove dead.
type HeldError struct {
	Slot string
	Info *Info // nil when the record is missing or unreadable
}

func (e *HeldError) Error() string {
	if e.Info == nil {
		return fmt.Sprintf("%s is claimed (lease record unreadable)", e.Slot)
	}
	return fmt.Sprintf("%s is claimed for %s (pid %d since %s)",
		e.Slot, e.Info.Branch, e.Info.PID, e.Info.ClaimedAt.Format(time.RFC3339))
}

// Acquire claims slot under leasesDir for branch.
// The lease directory is the persistent claim — it survives every
// process involved, which is the point — while a short flock
// serializes the check-steal-create critical section, so racing
// claimers can neither double-create nor steal a lease that was
// re-acquired between their staleness check and their theft.
// The flock cannot wedge: the kernel drops it with its holder.
// A provably dead lease is stolen; a live or unverifiable one
// returns *HeldError.
func Acquire(leasesDir, slot, branch string) (*Info, error) {
	if err := os.MkdirAll(leasesDir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := lockExclusive(filepath.Join(leasesDir, ".acquire.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()

	dir := filepath.Join(leasesDir, slot)
	err = os.Mkdir(dir, 0o755)
	if errors.Is(err, fs.ErrExist) {
		info, rerr := readRecord(dir)
		switch {
		case errors.Is(rerr, fs.ErrNotExist):
			// No record can be mid-write while this flock is held,
			// so a recordless lease is a claimer that died between
			// mkdir and write: reclaim it.
		case rerr != nil:
			// A corrupt record proves nothing about its holder;
			// never steal on a guess (wt release clears a truly
			// wedged slot by hand).
			return nil, &HeldError{Slot: slot}
		case !info.Stale():
			return nil, &HeldError{Slot: slot, Info: info}
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		err = os.Mkdir(dir, 0o755)
	}
	if err != nil {
		return nil, err
	}
	info, err := writeRecord(dir, branch)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return info, nil
}

// Release frees slot; releasing a free slot is not an error,
// so cleanup paths can run it unconditionally.
func Release(leasesDir, slot string) error {
	return os.RemoveAll(filepath.Join(leasesDir, slot))
}

// Get reports the lease on slot: (nil, nil) when free, the record
// when held. A held slot whose record is missing or unreadable is
// an error carrying that fact; callers decide how loudly to say it.
func Get(leasesDir, slot string) (*Info, error) {
	dir := filepath.Join(leasesDir, slot)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return readRecord(dir)
}

// Stale reports whether the lease's holder is provably gone:
// its PID is dead, or the PID is alive but belongs to a different
// process than the one recorded (start times differ — PID reuse).
// Anything unverifiable — a foreign host, an unreadable start
// time — reads as live: wt never steals on a guess.
func (i *Info) Stale() bool {
	if host, err := os.Hostname(); err != nil || i.Hostname != host {
		return false
	}
	if i.PID <= 0 {
		return false
	}
	if !alive(i.PID) {
		return true
	}
	if i.PIDStart == "" {
		return false
	}
	start, err := processStart(i.PID)
	if err != nil {
		return false
	}
	return start != i.PIDStart
}

func writeRecord(dir, branch string) (*Info, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	info := &Info{
		PID:       os.Getppid(),
		Hostname:  host,
		Branch:    branch,
		ClaimedAt: time.Now().UTC(),
	}
	// Best effort: an unreadable start time weakens the PID-reuse
	// guard for this lease but must not block claiming.
	info.PIDStart, _ = processStart(info.PID)
	raw, err := toml.Marshal(info)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, recordName), raw, 0o644); err != nil {
		return nil, err
	}
	return info, nil
}

func readRecord(dir string) (*Info, error) {
	raw, err := os.ReadFile(filepath.Join(dir, recordName))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := toml.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, recordName), err)
	}
	return &info, nil
}
