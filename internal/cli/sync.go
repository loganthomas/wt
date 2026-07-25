package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/pool"
)

func newSyncCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch the base, fast-forward it, and report tree staleness",
		Long: "Fetch the base branch's remote, fast-forward the local base\n" +
			"(refusing anything that is not a pure fast-forward), and report\n" +
			"how far each tree trails the base. Branches carrying your own\n" +
			"commits are never touched.\n\n" +
			"--all additionally re-parks every idle pool slot onto the new\n" +
			"base tip, running the gated refresh, so the next claim is warm.\n" +
			"Claimed slots are left alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "also re-park idle pool slots onto the new base")
	return cmd
}

func runSync(cmd *cobra.Command, all bool) error {
	ctx := cmd.Context()
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	g := gitx.New(w.repo.Root)
	base := w.cfg.Base
	chatter := cmd.ErrOrStderr()

	// No upstream means no remote to be stale against: sync has
	// nothing to do, which is a clean exit, not a failure.
	remote, err := g.UpstreamRemote(ctx, base)
	if err != nil {
		fmt.Fprintf(chatter, "base %s tracks no remote — nothing to sync\n", base)
		return nil
	}
	up, err := g.Upstream(ctx, base)
	if err != nil {
		fmt.Fprintf(chatter, "base %s tracks no remote — nothing to sync\n", base)
		return nil
	}

	if err := g.Fetch(ctx, remote); err != nil {
		return fmt.Errorf("fetching %s: %w", remote, err)
	}
	st, err := w.stateDir()
	if err != nil {
		return err
	}
	if err := st.WriteLastFetch(time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(chatter, "fetched %s\n", remote)

	trees, err := g.Worktrees(ctx)
	if err != nil {
		return err
	}
	ffErr := fastForwardBase(ctx, g, trees, base, up, chatter)
	if ffErr != nil && exitCodeFor(ffErr) != exitPrecondition {
		return ffErr
	}

	// A base that could not fast-forward (ffErr is a precondition) is
	// left untouched, so there is no new tip to re-park onto; skip the
	// slot work but still print the report, then surface the exit-3 so
	// a scheduled or scripted sync can tell "synced" from "stuck".
	// The re-park runs before the report so the behind-counts reflect
	// the final state: an idle slot brought forward reads as current,
	// not as trailing the base it was just parked on.
	if ffErr == nil && all {
		if err := syncPool(ctx, w, base, chatter); err != nil {
			return err
		}
	}

	// Re-listed after every mutation: the base's own checkout moved in
	// the fast-forward, and a report built from the stale heads would
	// show trees trailing a base they now sit on.
	trees, err = g.Worktrees(ctx)
	if err != nil {
		return err
	}
	reportBehind(ctx, g, trees, base, chatter)
	return ffErr
}

// syncPool re-parks the repo's idle slots, or explains that --all
// had nothing to act on because the repo runs no pool.
func syncPool(ctx context.Context, w *wtRepo, base string, chatter io.Writer) error {
	if w.cfg.Pool == nil {
		fmt.Fprintln(chatter, "--all re-parks pool slots; this repo has no pool")
		return nil
	}
	p, err := poolOf(w)
	if err != nil {
		return err
	}
	return reparkIdleSlots(ctx, p, base, chatter)
}

// fastForwardBase advances the local base to its upstream, ff-only.
// It works wherever the base lives: in the worktree that has it
// checked out (a plain merge there), or, when it is checked out
// nowhere, by a local fetch into the ref. A base that cannot
// fast-forward (diverged, or a busy working tree) is never rewritten:
// it returns a precondition error so the sync exits 3 rather than
// reporting a success it did not achieve.
func fastForwardBase(
	ctx context.Context, g *gitx.Git, trees []gitx.Worktree, base, up string, chatter io.Writer,
) error {
	behind, err := g.CommitCount(ctx, base+".."+up)
	if err != nil {
		return err
	}
	if behind == 0 {
		fmt.Fprintf(chatter, "%s already current with %s\n", base, up)
		return nil
	}
	if ffErr := ffInPlace(ctx, g, trees, base, up); ffErr != nil {
		return preconditionf(
			"%s could not fast-forward to %s (%v) — resolve it by hand", base, up, ffErr)
	}
	fmt.Fprintf(chatter, "%s fast-forwarded %s to %s\n", base, commits(behind), up)
	return nil
}

// ffInPlace performs the actual fast-forward at whichever end holds
// the base: a merge in the checked-out worktree, or a local ref
// update when nothing has it out.
func ffInPlace(
	ctx context.Context, g *gitx.Git, trees []gitx.Worktree, base, up string,
) error {
	if t, held := treeHoldingBranch(trees, base); held {
		return gitx.New(t.Path).MergeFFOnly(ctx, up)
	}
	return g.FetchLocalFF(ctx, up, base)
}

// reportBehind lists each tree's distance behind the base, aligned.
// Bare entries have no commits to compare and are skipped.
func reportBehind(
	ctx context.Context, g *gitx.Git, trees []gitx.Worktree, base string, chatter io.Writer,
) {
	rows := make([][]string, 0, len(trees))
	for _, t := range trees {
		if t.Bare {
			continue
		}
		n, err := g.CommitCount(ctx, t.Head+".."+base)
		behind := "up to date"
		switch {
		case err != nil:
			behind = "unknown"
		case n > 0:
			behind = commits(n) + " behind"
		}
		rows = append(rows, []string{filepath.Base(t.Path), branchLabel(t), behind})
	}
	fmt.Fprint(chatter, alignRows(rows))
}

// reparkIdleSlots resets every idle slot onto the new base and runs
// the gated refresh, so a later claim lands warm and current (D7).
// Only free, detached slots are touched: a claimed slot (its branch
// checked out, or its lease live) carries work and is skipped, and
// the orphan guard inside the reset still protects any stranded
// commits. Each slot is re-parked under its own lease so a
// concurrent claim can never race in mid-reset.
func reparkIdleSlots(ctx context.Context, p *poolRepo, base string, chatter io.Writer) error {
	leases := p.state.LeasesDir()
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	for _, slot := range pool.Names(p.cfg.Pool.Size) {
		dest := filepath.Join(p.treesDir(), slot)
		t, registered := findTree(trees, dest)
		if !registered || !t.Detached {
			continue
		}
		if held, err := lease.Get(leases, slot); err != nil || (held != nil && !held.Stale()) {
			continue
		}
		mine, err := lease.Acquire(leases, slot, lease.Reparking)
		if err != nil {
			if isHeld(err) {
				continue
			}
			return err
		}
		if err := p.reparkOne(ctx, dest, slot, base, chatter); err != nil {
			_ = lease.Release(leases, slot, mine)
			if exitCodeFor(err) == exitPrecondition {
				fmt.Fprintf(chatter, "skipping %s: %v\n", slot, err)
				continue
			}
			return err
		}
		if err := lease.Release(leases, slot, mine); err != nil {
			return err
		}
		fmt.Fprintf(chatter, "re-parked %s onto %s\n", slot, base)
	}
	return nil
}

// reparkOne resets one leased slot back onto base and refreshes it
// behind the lockfile gate. It re-reads the tree under the lease:
// a claim that came and went since the listing may have changed it.
func (p *poolRepo) reparkOne(
	ctx context.Context, dest, slot, base string, chatter io.Writer,
) error {
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	t, registered := findTree(trees, dest)
	if !registered || !t.Detached {
		return nil
	}
	if err := p.resetSlot(ctx, t, base, chatter); err != nil {
		return err
	}
	return refreshTree(ctx, p.cfg, p.state, dest, slot, chatter)
}
