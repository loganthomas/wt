package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ktr0731/go-fuzzyfinder"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/nav"
)

// pickTree opens the fuzzy picker over all jump targets and
// returns the chosen one. Only ever called when interactive()
// holds; the TUI renders on /dev/tty, so a shim-captured stdout
// stays clean for the cd protocol.
// The TUI itself is verified by hand (see docs/shell.md);
// everything around it is covered by the non-TTY tests.
func pickTree(ctx context.Context, cands []nav.Candidate) (nav.Candidate, error) {
	if len(cands) == 0 {
		return nav.Candidate{}, errors.New("no trees to pick from — `wt new <branch>` creates one")
	}
	// Previews shell out to git, so each is rendered once and
	// cached: the picker re-asks on every keystroke.
	previews := make([]string, len(cands))
	idx, err := fuzzyfinder.Find(cands,
		func(i int) string { return cands[i].Display() },
		fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
			if i < 0 {
				return ""
			}
			if previews[i] == "" {
				previews[i] = preview(ctx, cands[i])
			}
			return previews[i]
		}),
	)
	if err != nil {
		if errors.Is(err, fuzzyfinder.ErrAbort) {
			return nav.Candidate{}, errors.New("cancelled")
		}
		return nav.Candidate{}, err
	}
	return cands[idx], nil
}

// preview renders one tree's pane: where it is, what state it's
// in, and what it last did. Errors degrade to omission — a
// broken tree should still be pickable, if only to jump in and
// repair it.
func preview(ctx context.Context, c nav.Candidate) string {
	g := gitx.New(c.Path)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", c.Display(), c.Path)
	if status, err := g.ShortStatus(ctx); err == nil {
		fmt.Fprintf(&b, "\n%s\n", status)
	}
	if last, err := g.LastCommit(ctx); err == nil {
		fmt.Fprintf(&b, "\n%s\n", last)
	}
	return b.String()
}
