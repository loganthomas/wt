package lease

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestAcquireFree(t *testing.T) {
	dir := t.TempDir()
	info, err := Acquire(dir, "pool-1", "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "feature/login" {
		t.Errorf("Branch = %q, want %q", info.Branch, "feature/login")
	}
	// The liveness anchor is the invoking session (wt itself exits
	// immediately), which from this test's perspective is its parent.
	if info.PID != os.Getppid() {
		t.Errorf("PID = %d, want parent pid %d", info.PID, os.Getppid())
	}
	if info.ClaimedAt.IsZero() {
		t.Error("ClaimedAt is zero")
	}

	// The record lands at the documented layout path
	// (PLAN.md, State layout): <leases>/<slot>/lease.toml.
	if _, err := os.Stat(filepath.Join(dir, "pool-1", "lease.toml")); err != nil {
		t.Errorf("lease record not at the documented path: %v", err)
	}

	got, err := Get(dir, "pool-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.PID != info.PID || got.Branch != info.Branch {
		t.Errorf("Get = %+v, want the acquired lease %+v", got, info)
	}
}

func TestGetFreeSlot(t *testing.T) {
	info, err := Get(t.TempDir(), "pool-1")
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Errorf("Get on free slot = %+v, want nil", info)
	}
}

func TestAcquireHeld(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir, "pool-1", "first"); err != nil {
		t.Fatal(err)
	}
	_, err := Acquire(dir, "pool-1", "second")
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second Acquire error = %v, want *HeldError", err)
	}
	if held.Slot != "pool-1" || held.Info == nil || held.Info.Branch != "first" {
		t.Errorf("HeldError = %+v, want slot pool-1 held for branch first", held)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir, "pool-1", "x"); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir, "pool-1"); err != nil {
		t.Fatal(err)
	}
	if info, err := Get(dir, "pool-1"); err != nil || info != nil {
		t.Errorf("Get after Release = %+v, %v; want free", info, err)
	}
	if err := Release(dir, "pool-1"); err != nil {
		t.Errorf("second Release: %v", err)
	}
	if _, err := Acquire(dir, "pool-1", "y"); err != nil {
		t.Errorf("Acquire after Release: %v", err)
	}
}

// deadPID returns a PID guaranteed dead: a child spawned,
// finished, and reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// plant writes a lease record as if some other process held slot.
func plant(t *testing.T, dir, slot string, info Info) {
	t.Helper()
	leaseDir := filepath.Join(dir, slot)
	if err := os.MkdirAll(leaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := toml.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaseDir, "lease.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAcquireStealsDeadPID(t *testing.T) {
	dir := t.TempDir()
	plant(t, dir, "pool-1", Info{
		PID:       deadPID(t),
		PIDStart:  "Mon Jan  2 15:04:05 2006",
		Hostname:  hostname(t),
		Branch:    "crashed",
		ClaimedAt: time.Now(),
	})
	info, err := Acquire(dir, "pool-1", "fresh")
	if err != nil {
		t.Fatalf("Acquire over dead lease: %v", err)
	}
	if info.Branch != "fresh" {
		t.Errorf("Branch = %q, want fresh", info.Branch)
	}
}

func TestAcquireStealsReusedPID(t *testing.T) {
	dir := t.TempDir()
	// A live PID whose recorded start time is someone else's:
	// the PID was reused, the original holder is gone (D15).
	plant(t, dir, "pool-1", Info{
		PID:       os.Getpid(),
		PIDStart:  "Mon Jan  2 15:04:05 2006",
		Hostname:  hostname(t),
		Branch:    "reused",
		ClaimedAt: time.Now(),
	})
	if _, err := Acquire(dir, "pool-1", "fresh"); err != nil {
		t.Fatalf("Acquire over reused-PID lease: %v", err)
	}
}

func TestLiveLeaseIsNeverStaleByAge(t *testing.T) {
	dir := t.TempDir()
	start, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// Ancient by wall clock, but the process is alive: never stale (D15).
	plant(t, dir, "pool-1", Info{
		PID:       os.Getpid(),
		PIDStart:  start,
		Hostname:  hostname(t),
		Branch:    "long-running",
		ClaimedAt: time.Now().Add(-90 * 24 * time.Hour),
	})
	_, err = Acquire(dir, "pool-1", "impatient")
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("Acquire over live lease error = %v, want *HeldError", err)
	}
}

func TestForeignHostLeaseIsNeverStale(t *testing.T) {
	dir := t.TempDir()
	// Liveness cannot be verified across hosts; never steal.
	plant(t, dir, "pool-1", Info{
		PID:       deadPID(t),
		PIDStart:  "Mon Jan  2 15:04:05 2006",
		Hostname:  "some-other-host.invalid",
		Branch:    "remote",
		ClaimedAt: time.Now(),
	})
	_, err := Acquire(dir, "pool-1", "local")
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("Acquire over foreign-host lease error = %v, want *HeldError", err)
	}
}

func TestRecordlessLeaseIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	// A lease directory without a record is a claimer that died
	// between mkdir and record write: record writes happen under
	// the acquire lock, so no live writer can be mid-flight once
	// Acquire holds it. Reclaim rather than wedge the slot.
	if err := os.MkdirAll(filepath.Join(dir, "pool-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, "pool-1", "x"); err != nil {
		t.Fatalf("Acquire over a recordless lease: %v", err)
	}
}

func TestGetRecordlessLease(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pool-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Get is read-only and lockless; it reports the unreadable
	// record as an error and leaves reclaiming to Acquire.
	if _, err := Get(dir, "pool-1"); err == nil {
		t.Error("Get on a recordless lease dir: want an error, got nil")
	}
}

func TestCorruptRecordHeldConservatively(t *testing.T) {
	dir := t.TempDir()
	leaseDir := filepath.Join(dir, "pool-1")
	if err := os.MkdirAll(leaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := os.WriteFile(filepath.Join(leaseDir, "lease.toml"), []byte("not = [toml"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, aerr := Acquire(dir, "pool-1", "x")
	var held *HeldError
	if !errors.As(aerr, &held) {
		t.Fatalf("Acquire error = %v, want *HeldError", aerr)
	}
}

func TestProcessStart(t *testing.T) {
	first, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("processStart returned empty for a live process")
	}
	second, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("processStart not stable: %q then %q", first, second)
	}
	if _, err := processStart(deadPID(t)); err == nil {
		t.Error("processStart on a dead PID: want an error, got nil")
	}
}

// TestSoak interleaves claim/release cycles across goroutines with
// dead leases injected along the way (PLAN.md, pool soak):
// no slot is ever held twice, and no slot is lost.
func TestSoak(t *testing.T) {
	dir := t.TempDir()
	slots := []string{"pool-1", "pool-2", "pool-3"}
	var holders [3]atomic.Int32

	// Some slots start wedged by a crashed process.
	plant(t, dir, "pool-2", Info{
		PID:       deadPID(t),
		PIDStart:  "Mon Jan  2 15:04:05 2006",
		Hostname:  hostname(t),
		Branch:    "crashed",
		ClaimedAt: time.Now(),
	})

	const workers, rounds = 8, 25
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				i := (w + r) % len(slots)
				_, err := Acquire(dir, slots[i], "work")
				var held *HeldError
				if errors.As(err, &held) {
					continue
				}
				if err != nil {
					t.Errorf("Acquire(%s): %v", slots[i], err)
					continue
				}
				if n := holders[i].Add(1); n != 1 {
					t.Errorf("slot %s held by %d claimers at once", slots[i], n)
				}
				holders[i].Add(-1)
				if err := Release(dir, slots[i]); err != nil {
					t.Errorf("Release(%s): %v", slots[i], err)
				}
			}
		}()
	}
	wg.Wait()

	// No slot was lost: every one is acquirable at the end.
	for _, s := range slots {
		if _, err := Acquire(dir, s, "final"); err != nil {
			t.Errorf("slot %s lost after soak: %v", s, err)
		}
	}
}
