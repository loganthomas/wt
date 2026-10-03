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
	"unicode/utf8"
)

// Align renders rows in aligned columns, shared by every tabular
// listing. Widths are computed by hand rather than with
// text/tabwriter: padding must only ever sit between cells,
// because trimming rendered lines would also strip a path's own
// trailing spaces, and stdout must stay exact for machine
// consumers (D13). Trailing empty cells drop their padding too,
// so no line ever ends in spaces.
func Align(rows [][]string) string {
	var width []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], utf8.RuneCountInString(cell))
		}
	}
	const gap = 2
	var out strings.Builder
	for _, row := range rows {
		last := len(row) - 1
		for last > 0 && row[last] == "" {
			last--
		}
		for i := range last {
			// fmt pads %s to a minimum rune count, matching the width math above.
			fmt.Fprintf(&out, "%-*s", width[i]+gap, row[i])
		}
		fmt.Fprintln(&out, row[last])
	}
	return out.String()
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
