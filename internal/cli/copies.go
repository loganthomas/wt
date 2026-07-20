// The two halves of the copy-list contract live together here:
// wt new plants the configured files, wt done sweeps only what
// still looks planted. Keeping them side by side is what keeps
// "what wt planted is what wt may sweep" one invariant.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/loganthomas/wt/internal/gitx"
)

// copyFiles ports the configured untracked files from the main
// checkout into the fresh tree. Copies, never symlinks: symlinked
// configs break tools that resolve paths through them (D5).
// A missing source is a note, not an error — .env may simply not
// exist on this machine.
func copyFiles(srcRoot, dstRoot string, names []string, chatter io.Writer) error {
	for _, name := range names {
		err := copyFile(filepath.Join(srcRoot, name), filepath.Join(dstRoot, name))
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(chatter, "copy: %s not found in the main checkout, skipped\n", name)
			continue
		}
		if err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
		fmt.Fprintf(chatter, "copy: %s\n", name)
	}
	return nil
}

// copyFile copies one file, creating parent directories and
// carrying the source permissions over — copy sources are often
// secrets (.env) deliberately locked down.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// splitCopies partitions the configured copy files found untracked
// in the tree: pristine ones still match the main checkout byte for
// byte and are wt's own plantings, free to sweep on removal; edited
// ones are the user's data and must block it. A tracked file belongs
// to git even if copy-listed and lands in neither list.
// Paths come back slash-separated to match git's status output.
func splitCopies(
	ctx context.Context, srcRoot, treeRoot string, names []string,
) (pristine, edited []string, err error) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	tracked, err := gitx.New(treeRoot).Tracked(ctx, names...)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		if tracked[filepath.ToSlash(name)] {
			continue
		}
		treeData, ok, err := readCopy(treeRoot, name)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		srcData, ok, err := readCopy(srcRoot, name)
		if err != nil {
			return nil, nil, err
		}
		if ok && bytes.Equal(treeData, srcData) {
			pristine = append(pristine, filepath.ToSlash(name))
		} else {
			edited = append(edited, filepath.ToSlash(name))
		}
	}
	return pristine, edited, nil
}

// readCopy reads a copy-list file under root;
// ok is false when the file does not exist there.
func readCopy(root, name string) (data []byte, ok bool, err error) {
	data, err = os.ReadFile(filepath.Join(root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("copy %s: %w", name, err)
	}
	return data, true, nil
}
