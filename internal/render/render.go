// Package render is the one place wt turns structured data into
// output: aligned columns for humans and JSON for machines
// (PLAN.md Phase 6). Commands build a single view value and hand
// it to both renderers, so the human and machine views of the
// same command can never drift apart (D13).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Align renders rows in aligned columns, shared by every tabular
// listing. Widths are computed by hand rather than with
// text/tabwriter: padding must only ever sit between cells,
// because trimming rendered lines would also strip a path's own
// trailing spaces, and stdout must stay exact for machine
// consumers (D13). Trailing empty cells drop their padding too,
// so no line ever ends in spaces.
// Widths are display cells, so colored cells align with plain ones.
func Align(rows [][]string) string {
	width := columnWidths(rows)
	var out strings.Builder
	for _, row := range rows {
		last := len(row) - 1
		for last > 0 && row[last] == "" {
			last--
		}
		for i := range last {
			out.WriteString(row[i])
			out.WriteString(strings.Repeat(" ", width[i]+gap-ansi.StringWidth(row[i])))
		}
		fmt.Fprintln(&out, row[last])
	}
	return out.String()
}

const gap = 2

// minFit keeps a squeezed column legible: below it, a truncated
// path says too little to be worth the lost characters.
const minFit = 16

// FitColumn middle-truncates column col so the aligned table fits
// within maxWidth display cells; maxWidth 0 means unlimited.
// The middle goes because a path's ends carry its meaning: the
// root it hangs from and the tree it names.
// When even a minFit-wide col can't make the table fit, nothing
// is truncated: the rows overflow either way, and losing
// characters would then buy nothing.
func FitColumn(rows [][]string, col, maxWidth int) [][]string {
	if maxWidth <= 0 {
		return rows
	}
	width := columnWidths(rows)
	if col >= len(width) {
		return rows
	}
	others := gap * (len(width) - 1)
	for i, w := range width {
		if i != col {
			others += w
		}
	}
	budget := maxWidth - others
	if budget < minFit {
		return rows
	}
	fitted := make([][]string, len(rows))
	for i, row := range rows {
		fitted[i] = row
		if col < len(row) && ansi.StringWidth(row[col]) > budget {
			fitted[i] = append([]string(nil), row...)
			fitted[i][col] = truncateMiddle(row[col], budget)
		}
	}
	return fitted
}

// truncateMiddle shortens s to width cells around an ellipsis,
// keeping a short head for orientation and the rest for the tail,
// where a path names its tree.
func truncateMiddle(s string, width int) string {
	head := (width - 1) / 4
	tail := width - 1 - head
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-tail, "")
}

func columnWidths(rows [][]string) []int {
	var width []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], ansi.StringWidth(cell))
		}
	}
	return width
}

// JSON writes v as two-space-indented JSON with a final newline.
// HTML escaping is off: this output goes to terminals and scripts,
// never into web pages, and `<n>` in a fix command must stay
// readable.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
