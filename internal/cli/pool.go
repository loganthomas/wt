package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/pool"
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
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	p, err := poolOf(w)
	if err != nil {
		return err
	}
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, p.cfg.Pool.Size)
	for _, slot := range pool.Names(p.cfg.Pool.Size) {
		_, registered := findTree(trees, filepath.Join(p.treesDir(), slot))
		held, err := lease.Get(p.state.LeasesDir(), slot)
		rows = append(rows, slotRow(slot, registered, held, err))
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), alignRows(rows))
	return err
}

// slotRow renders one slot's occupancy: slot, state, branch, detail.
func slotRow(slot string, registered bool, held *lease.Info, err error) []string {
	switch {
	case err != nil:
		return []string{
			slot, "claimed", "?",
			fmt.Sprintf("lease record unreadable — `wt release %s` clears it", slot),
		}
	case held == nil && !registered:
		return []string{slot, "unprovisioned", "-", "provisions on first claim"}
	case held == nil:
		return []string{slot, "free", "-", ""}
	case held.Stale():
		return []string{
			slot, "stale", held.Branch,
			fmt.Sprintf("dead pid %d — reclaimed on next claim", held.PID),
		}
	default:
		return []string{
			slot, "claimed", held.Branch,
			fmt.Sprintf("pid %d, claimed %s", held.PID, humanAge(held.ClaimedAt)),
		}
	}
}

// humanAge says how long ago t was, coarsely: pool occupancy is
// read at a glance, not billed by the second.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	p, err := poolOf(w)
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
// provisioning on demand — never the reverse, where warm trees
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
	if _, ok := pool.SlotPath(p.treesDir(), t.Path); !ok {
		return fmt.Errorf("refusing to remove %s: not a pool slot under %s", t.Path, p.treesDir())
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
