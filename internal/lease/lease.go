// Package lease implements crash-safe claims on pool slots
// (PLAN.md D15). A lease is an atomically created directory under
// the repo's state dir whose record names the claiming session:
// PID, process start time, hostname, branch, claim time.
// A lease goes stale only when its PID is dead or its start time
// no longer matches (PID reuse) — never by wall clock alone,
// so long-running legitimate work is never reaped (R3).
//
// A claim is a two-phase handoff. Acquire records wt's own PID:
// while wt provisions the slot — setup hooks can run for minutes —
// the slot is protected by wt's liveness, and a kill leaves a
// lease that is provably dead rather than one pinned to a shell
// that outlives the work. On success the caller Repins the lease
// to the session — the shell, script, or agent doing the work —
// because wt itself exits within milliseconds of finishing.
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
// returns *HeldError. The record names wt's own PID — the claim
// phase of the handoff described in the package comment — and is
// returned so the caller can later prove which lease is its own.
func Acquire(leasesDir, slot, branch string) (*Info, error) {
	unlock, err := lockLeases(leasesDir)
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
	mine, err := writeRecord(dir, branch, os.Getpid())
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return mine, nil
}

// Repin atomically transfers slot's lease to the calling session,
// provided it has not changed hands since expect was read
// (nil expect: the record was absent or unreadable at entry).
// It is the release-side half of the claim protocol: guards and
// resets that run after a successful Repin cannot race a
// concurrent Acquire, because the slot is now held live by the
// releasing session itself — and should that session die
// mid-release, its pin goes stale like any other lease.
// A live lease other than the expected one returns *HeldError;
// a stale or unreadable one is taken over regardless of expect,
// since its holder is either provably dead or unprovable-and-
// being-cleared on explicit user request. The new record names
// the session (wt's original parent) — this is the handoff half
// of both protocols: a finished claim pins its slot to the
// session doing the work, and a release pins the slot to the
// session clearing it. The record written is returned.
func Repin(leasesDir, slot, branch string, expect *Info) (*Info, error) {
	unlock, err := lockLeases(leasesDir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	dir := filepath.Join(leasesDir, slot)
	current, rerr := readRecord(dir)
	if rerr == nil && !current.same(expect) && !current.Stale() {
		return nil, &HeldError{Slot: slot, Info: current}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return writeRecord(dir, branch, sessionPID)
}

// Release frees slot, but only when the current lease is the
// expected one, provably dead, or absent — releasing a free slot
// is not an error, so cleanup paths can run it unconditionally.
// The guard matters when the caller's lease was taken over behind
// its back (an explicit `wt release` racing a claim): removing
// unconditionally would delete the new holder's live lease and
// hand the slot to a third claimer. A live lease other than
// expect, or an unreadable record (which proves nothing), returns
// *HeldError and leaves the lease in place.
func Release(leasesDir, slot string, expect *Info) error {
	unlock, err := lockLeases(leasesDir)
	if err != nil {
		return err
	}
	defer unlock()

	dir := filepath.Join(leasesDir, slot)
	current, rerr := readRecord(dir)
	switch {
	case errors.Is(rerr, fs.ErrNotExist):
		// Free, or a recordless leftover: no writer can be
		// mid-record while this flock is held, so removing is safe.
	case rerr != nil:
		return &HeldError{Slot: slot}
	case !current.same(expect) && !current.Stale():
		return &HeldError{Slot: slot, Info: current}
	}
	return os.RemoveAll(dir)
}

// Get reports the lease on slot: (nil, nil) when free, the record
// when held. A held slot whose record is missing or unreadable is
// an error carrying that fact; callers decide how loudly to say it.
// The record is read before the directory is checked: the reverse
// order misread a lease released between the two calls as a held
// slot with an unreadable record.
func Get(leasesDir, slot string) (*Info, error) {
	dir := filepath.Join(leasesDir, slot)
	info, err := readRecord(dir)
	if !errors.Is(err, fs.ErrNotExist) {
		return info, err
	}
	if _, serr := os.Stat(dir); errors.Is(serr, fs.ErrNotExist) {
		return nil, nil
	}
	return nil, err
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

// same reports whether two records name the same claim.
// ClaimedAt participates so a slot released and re-claimed by the
// very same session still reads as having changed hands.
func (i *Info) same(o *Info) bool {
	return i != nil && o != nil &&
		i.PID == o.PID && i.PIDStart == o.PIDStart &&
		i.Hostname == o.Hostname && i.ClaimedAt.Equal(o.ClaimedAt)
}

// sessionPID is wt's parent as it was at startup — the shell,
// script, or agent session doing the work. Captured before any
// work runs: if that session later dies, Getppid would report the
// reaper (init, or a PID-1 shell in a container) and the lease
// would wrongly track a process that never claimed anything.
// Recording the original parent means an orphaned claim's lease
// reads stale the moment its session is gone — and a container
// session that legitimately IS PID 1 stays live by its own
// start time, instead of being mistaken for a reparented orphan.
var sessionPID = os.Getppid()

func writeRecord(dir, branch string, pid int) (*Info, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	info := &Info{
		PID:       pid,
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
	// Temp file + rename: a crash mid-write leaves a recordless
	// directory — the state Acquire already reclaims — never a
	// torn or empty record, which nothing could ever prove dead.
	tmp, err := os.CreateTemp(dir, recordName+".tmp-*")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	// Synced before the rename: without it a power loss could
	// publish an empty or torn record at the final name — one that
	// parses as a holder no host can ever prove dead.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, recordName)); err != nil {
		return nil, err
	}
	return info, nil
}

// lockLeases enters the lease critical section: the leases
// directory is created as needed and the acquire flock taken.
// One spelling for all three protocol entry points, so the lock
// path and mode cannot drift between them.
func lockLeases(leasesDir string) (unlock func(), err error) {
	if err := os.MkdirAll(leasesDir, 0o755); err != nil {
		return nil, err
	}
	return lockExclusive(filepath.Join(leasesDir, ".acquire.lock"))
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
