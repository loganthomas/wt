package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/freshness"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/render"
	"github.com/loganthomas/wt/internal/state"
)

func newPoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Inspect and size the slot pool",
	}
	cmd.AddCommand(newPoolLsCmd(), newPoolResizeCmd())
	return cmd
}

func newPoolLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List slots: free, claimed, and by whom",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPoolLs(cmd)
		},
	}
}

func runPoolLs(cmd *cobra.Command) error {
	ctx := cmd.Context()
	p, err := openPool(ctx)
	if err != nil {
		return err
	}
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, p.cfg.Pool.Size)
	for _, view := range slotViews(p.wtRepo, p.state, trees) {
		rows = append(rows, slotRow(view))
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), render.Align(rows))
	return err
}

// slotViews builds every configured slot's occupancy view, in
// slot order. It takes the bare repo seams rather than a
// poolRepo, so wt status can feed it without re-deriving what it
// already holds.
func slotViews(w *wtRepo, st state.Dir, trees []gitx.Worktree) []slotView {
	views := make([]slotView, 0, w.cfg.Pool.Size)
	for _, slot := range pool.Names(w.cfg.Pool.Size) {
		_, registered := findTree(trees, filepath.Join(w.treesDir(), slot))
		held, err := lease.Get(st.LeasesDir(), slot)
		views = append(views, newSlotView(slot, registered, held, err))
	}
	return views
}

// slotView is one slot's occupancy, the single source for wt pool
// ls rows and wt status, human and JSON alike, so the views
// cannot drift (D13). Note carries the human detail; its wording
// is informational, not part of the machine contract.
type slotView struct {
	Slot      string     `json:"slot"`
	State     string     `json:"state"` // claimed | free | stale | unprovisioned
	Branch    string     `json:"branch,omitempty"`
	PID       int        `json:"pid,omitempty"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	Note      string     `json:"note,omitempty"`
}

// newSlotView classifies one slot's occupancy. An unreadable
// lease record reads as claimed with branch "?": wt never treats
// what it cannot prove as free (D15).
func newSlotView(slot string, registered bool, held *lease.Info, err error) slotView {
	switch {
	case err != nil:
		return slotView{Slot: slot, State: "claimed", Branch: "?", Note: unreadableLeaseAdvice(slot)}
	case held == nil && !registered:
		return slotView{Slot: slot, State: "unprovisioned", Note: "provisions on first claim"}
	case held == nil:
		return slotView{Slot: slot, State: "free"}
	case held.Stale():
		return slotView{
			Slot: slot, State: "stale", Branch: held.Branch, PID: held.PID,
			Note: fmt.Sprintf("dead pid %d — reclaimed on next claim", held.PID),
		}
	default:
		at := held.ClaimedAt
		return slotView{
			Slot: slot, State: "claimed", Branch: held.Branch, PID: held.PID, ClaimedAt: &at,
			Note: fmt.Sprintf("pid %d, claimed %s",
				held.PID, freshness.Age(time.Since(held.ClaimedAt))),
		}
	}
}

// slotRow renders one slot view as a table row:
// slot, state, branch, detail.
func slotRow(v slotView) []string {
	return []string{v.Slot, v.State, cmp.Or(v.Branch, "-"), v.Note}
}

// unreadableLeaseAdvice is the one spelling of the escape hatch
// for a lease record wt cannot read and so never clears on a
// guess (D15): shared by pool ls and wt clean.
func unreadableLeaseAdvice(slot string) string {
	return fmt.Sprintf("lease record unreadable — `wt release %s` clears it", slot)
}

func newPoolResizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resize <size>",
		Short: "Grow or shrink the pool",
		Long: "Grow provisions and warms the new slots (setup hook included).\n" +
			"Shrink removes the top slots, refusing while any of them is claimed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPoolResize(cmd, args[0])
		},
	}
}

func runPoolResize(cmd *cobra.Command, arg string) error {
	size, err := strconv.Atoi(arg)
	if err != nil || size < 1 {
		return usageError{fmt.Errorf("pool size must be a whole number of at least 1, got %q", arg)}
	}
	ctx := cmd.Context()
	p, err := openPool(ctx)
	if err != nil {
		return err
	}
	chatter := cmd.ErrOrStderr()
	current := p.cfg.Pool.Size
	switch {
	case size == current:
		fmt.Fprintf(chatter, "the pool already has %d slots\n", size)
		return nil
	case size > current:
		return p.grow(ctx, current, size, chatter)
	default:
		return p.shrink(ctx, current, size, chatter)
	}
}

// grow writes the new size first: a crash mid-provision leaves an
// oversized config with missing slots, which claims heal by
// provisioning on demand, never the reverse, where warm trees
// sit outside the configured pool.
func (p *poolRepo) grow(ctx context.Context, from, to int, chatter io.Writer) error {
	if err := checkBase(ctx, p.g, p.cfg.Base); err != nil {
		return err
	}
	if err := p.savePoolSize(to); err != nil {
		return err
	}
	return resizeHeld(p.provisionPool(ctx, from, to, chatter))
}

// resizeHeld maps a lease refusal during pool provisioning to
// exit 3 with the honest way forward: a readable holder means a
// claim raced and a rerun will succeed once it settles; an
// unreadable record never resolves itself and only `wt release`
// clears it. Shared by resize and init, the two bulk provisioners.
func resizeHeld(err error) error {
	var held *lease.HeldError
	if !errors.As(err, &held) {
		return err
	}
	if held.Info == nil {
		return preconditionf("%v — `wt release %s` clears it", held, held.Slot)
	}
	return preconditionf("%v — a concurrent claim holds it; rerun once it settles", held)
}

// shrink removes the top slots down to size. Claimed victims
// refuse the whole shrink up front; each survivor is then removed
// under its own lease so no claim can race in. The config shrinks
// last: a crash leaves extra slots configured and intact, never
// warm trees orphaned outside the pool.
func (p *poolRepo) shrink(ctx context.Context, from, to int, chatter io.Writer) error {
	leases := p.state.LeasesDir()
	for i := to + 1; i <= from; i++ {
		slot := pool.SlotName(i)
		held, err := lease.Get(leases, slot)
		if err != nil {
			return preconditionf(
				"%s's lease record is unreadable — `wt release %s` clears it", slot, slot)
		}
		if held != nil && !held.Stale() {
			// Internal leases name no tree, so the wt done advice
			// would be nonsense for them.
			if lease.IsInternal(held.Branch) {
				return preconditionf(
					"%s is held by another wt operation %s — let it finish; "+
						"if it crashed, `wt release %s` clears it",
					slot, held.Branch, slot)
			}
			return preconditionf("%s is claimed for %s — `wt done %s` first",
				slot, held.Branch, held.Branch)
		}
	}
	for i := from; i > to; i-- {
		slot := pool.SlotName(i)
		mine, err := lease.Acquire(leases, slot, lease.Removing)
		if err != nil {
			return resizeHeld(err)
		}
		// Listed under the lease: a claim that came and went since
		// the precheck may have provisioned the slot, and a stale
		// listing would skip the removal while the config shrank.
		trees, err := p.g.Worktrees(ctx)
		if err != nil {
			_ = lease.Release(leases, slot, mine)
			return err
		}
		if err := p.removeSlot(ctx, trees, slot); err != nil {
			_ = lease.Release(leases, slot, mine)
			return err
		}
		if err := lease.Release(leases, slot, mine); err != nil {
			return err
		}
		fmt.Fprintf(chatter, "removed %s\n", slot)
	}
	return p.savePoolSize(to)
}

// removeSlot deletes one slot's tree and state, with the same
// guards as any other destructive path (R2, D14).
func (p *poolRepo) removeSlot(ctx context.Context, trees []gitx.Worktree, slot string) error {
	dest := filepath.Join(p.treesDir(), slot)
	t, registered := findTree(trees, dest)
	if !registered {
		return p.state.RemoveTree(slot)
	}
	if err := checkRemovable(t); err != nil {
		return err
	}
	if _, err := p.requireSlot(t.Path, "remove"); err != nil {
		return err
	}
	if _, err := finishGuards(ctx, p.repo.Root, t, p.cfg.Copy); err != nil {
		return err
	}
	if err := p.g.WorktreeRemoveForce(ctx, dest); err != nil {
		return err
	}
	return p.state.RemoveTree(slot)
}

// savePoolSize rewrites only the repo file's pool size, keeping
// the in-memory view honest for the rest of the command.
func (p *poolRepo) savePoolSize(size int) error {
	path := p.repo.ConfigPath()
	cfg, err := config.LoadRepo(path)
	if err != nil {
		return err
	}
	if cfg.Pool == nil {
		cfg.Pool = &config.Pool{}
	}
	cfg.Pool.Size = size
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	p.cfg.Pool.Size = size
	return nil
}
