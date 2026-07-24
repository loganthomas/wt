// The warm-up mechanism shared by both modes: a fresh tree or slot
// is built exactly once, and a claim re-runs hooks.refresh only
// when the gate says the tree went cold (D5). Kept out of slots.go
// so default-mode `wt new` does not have to reach into pool-mode
// orchestration for the definition of "fresh".
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/state"
)

// finishFresh completes a just-created tree or slot, one warm-up
// only: the setup hook when configured (presumed to leave the
// tree fully built, so the refresh hash it implicitly satisfied
// is recorded rather than immediately re-run), otherwise the
// refresh hook through its usual gate. Shared by wt new and slot
// provisioning so the two can never disagree on what "fresh"
// means.
func finishFresh(
	ctx context.Context, cfg config.Config, st state.Dir, dest, name string, chatter io.Writer,
) error {
	// Fresh also means no inherited state: a namesake tree removed
	// out of band can leave a refresh hash behind that would
	// satisfy the gate and skip the very warm-up this function
	// exists to run.
	if err := st.RemoveTree(name); err != nil {
		return err
	}
	setup := cfg.Hooks.Setup
	if setup == "" {
		return refreshTree(ctx, cfg, st, dest, name, chatter)
	}
	fmt.Fprintf(chatter, "running setup hook: %s\n", setup)
	if err := runHook(ctx, dest, setup, chatter); err != nil {
		return fmt.Errorf("setup hook failed: %w", err)
	}
	if files := cfg.Hooks.RefreshIfChanged; len(files) > 0 {
		hash, err := pool.Hash(dest, files)
		if err != nil {
			return err
		}
		return st.WriteRefreshHash(name, hash)
	}
	return nil
}

// refreshTree runs hooks.refresh behind the lockfile gate (D5):
// with refresh_if_changed configured, only a hash change triggers
// it, and only success re-records the hash; without the gate it
// runs on every claim and new. Shared by pool claims and
// default-mode trees so the two can never drift.
func refreshTree(
	ctx context.Context, cfg config.Config, st state.Dir, dest, name string, chatter io.Writer,
) error {
	refresh := cfg.Hooks.Refresh
	if refresh == "" {
		return nil
	}
	files := cfg.Hooks.RefreshIfChanged
	var current string
	if len(files) > 0 {
		var err error
		current, err = pool.Hash(dest, files)
		if err != nil {
			return err
		}
		if current == st.RefreshHash(name) {
			return nil
		}
	}
	fmt.Fprintf(chatter, "running refresh hook: %s\n", refresh)
	if err := runHook(ctx, dest, refresh, chatter); err != nil {
		return fmt.Errorf("refresh hook failed: %w", err)
	}
	if len(files) > 0 {
		return st.WriteRefreshHash(name, current)
	}
	return nil
}
