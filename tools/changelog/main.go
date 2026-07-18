// Command changelog manages wt's news-fragment release notes.
//
// Fragments are single-sentence Markdown files under .changes/,
// one per PR, named <pr>.<type>.md.
// They are batched into CHANGELOG.md at release time
// and the release workflow publishes the extracted section.
// See CONTRIBUTING.md for the workflow.
package main

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	fragmentsDir  = ".changes"
	changelogFile = "CHANGELOG.md"
	headerLine    = "# Changelog"
)

// fragmentType maps a fragment type key to its changelog heading.
type fragmentType struct {
	Key     string
	Heading string
}

// fragmentTypes lists the known types, ordered by user impact for rendering.
var fragmentTypes = []fragmentType{
	{"enh", "Enhancements"},
	{"bug", "Fixes"},
	{"dep", "Deprecations"},
	{"doc", "Documentation"},
	{"maint", "Infrastructure"},
}

// fragment is one staged news entry, parsed from a <pr>.<type>.md filename.
type fragment struct {
	path string
	stem string
	pr   int // 0 when the stem is not a PR number
	typ  string
}

func main() {
	cmd, args := "pending", os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "new":
		err = runNew(args)
	case "pending":
		err = runPending()
	case "batch":
		err = runBatch(args)
	case "extract":
		err = runExtract(args)
	default:
		err = fmt.Errorf("unknown command %q (want new, pending, batch, or extract)", cmd)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "changelog: %v\n", err)
		os.Exit(1)
	}
}

// runNew creates a fragment from flags,
// prompting for anything not supplied
// so both agents (flags) and humans (prompts) can use it.
func runNew(args []string) error {
	flags := flag.NewFlagSet("new", flag.ExitOnError)
	pr := flags.Int("pr", 0, "pull request number")
	typ := flags.String("type", "", "fragment type: "+typeKeys())
	if err := flags.Parse(args); err != nil {
		return err
	}
	message := strings.Join(flags.Args(), " ")

	in := bufio.NewReader(os.Stdin)
	if *pr == 0 {
		answer, err := prompt(in, "PR number")
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(answer)
		if err != nil {
			return fmt.Errorf("invalid PR number %q", answer)
		}
		*pr = n
	}
	if *typ == "" {
		answer, err := prompt(in, "Type ("+typeKeys()+")")
		if err != nil {
			return err
		}
		*typ = answer
	}
	if strings.TrimSpace(message) == "" {
		answer, err := prompt(in, "Describe the change for END USERS, one sentence")
		if err != nil {
			return err
		}
		message = answer
	}

	path, err := writeFragment(fragmentsDir, *pr, *typ, message)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created %s; commit it with your PR\n", path)
	return nil
}

func runPending() error {
	frags, strays, err := stagedFragments(fragmentsDir)
	if err != nil {
		return err
	}
	for _, stray := range strays {
		fmt.Fprintf(os.Stderr, "warning: %s will not be collected (want <pr>.<type>.md)\n", stray)
	}
	body, err := renderSection(frags)
	if err != nil {
		return err
	}
	if body == "" {
		fmt.Fprintln(os.Stderr, "no pending fragments")
		return nil
	}
	fmt.Print(body)
	return nil
}

func runBatch(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: changelog batch <version>")
	}
	date := time.Now().UTC().Format("2006-01-02")
	return batchChangelog(".", args[0], date)
}

func runExtract(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: changelog extract <version>")
	}
	section, err := extractSection(".", args[0])
	if err != nil {
		return err
	}
	fmt.Print(section)
	return nil
}

// batchChangelog folds the pending fragments into a dated version
// section at the top of CHANGELOG.md and deletes the consumed files,
// so each fragment is released exactly once.
// Stray files are an error, not a warning:
// a release must never silently drop a staged note.
func batchChangelog(root, version, date string) error {
	frags, strays, err := stagedFragments(filepath.Join(root, fragmentsDir))
	if err != nil {
		return err
	}
	if len(strays) > 0 {
		return fmt.Errorf("stray files would be dropped from the notes: %s (want <pr>.<type>.md)",
			strings.Join(strays, ", "))
	}
	body, err := renderSection(frags)
	if err != nil {
		return err
	}
	if body == "" {
		body = "No notable changes.\n"
	}

	path := filepath.Join(root, changelogFile)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if strings.Contains(string(old), "\n## "+version+" ") {
		return fmt.Errorf("%s already has a %s section", changelogFile, version)
	}

	rest := strings.TrimLeft(strings.TrimPrefix(string(old), headerLine), "\n")
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n\n## %s - %s\n\n%s", headerLine, version, date, body)
	if rest != "" {
		out.WriteString("\n")
		out.WriteString(rest)
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return err
	}

	for _, f := range frags {
		if err := os.Remove(f.path); err != nil {
			return err
		}
	}
	return nil
}

// extractSection returns one version's body from CHANGELOG.md,
// without its heading; the release workflow publishes it verbatim.
func extractSection(root, version string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		return "", err
	}
	var section []string
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			if found {
				break
			}
			found = strings.HasPrefix(line, "## "+version+" ")
			continue
		}
		if found {
			section = append(section, line)
		}
	}
	if !found {
		return "", fmt.Errorf("no %s section in %s; run batch first", version, changelogFile)
	}
	return strings.TrimSpace(strings.Join(section, "\n")) + "\n", nil
}

// stagedFragments parses the fragments directory in one pass,
// separating well-formed fragments from strays
// so callers can refuse or report files that would silently drop.
func stagedFragments(dir string) ([]fragment, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var frags []fragment
	var strays []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" {
			continue
		}
		parts := strings.Split(name, ".")
		typ := ""
		if len(parts) >= 3 && parts[len(parts)-1] == "md" {
			typ = parts[len(parts)-2]
		}
		if !validType(typ) {
			strays = append(strays, filepath.Join(dir, name))
			continue
		}
		stem := strings.Join(parts[:len(parts)-2], ".")
		pr, _ := strconv.Atoi(stem)
		frags = append(frags, fragment{path: filepath.Join(dir, name), stem: stem, pr: pr, typ: typ})
	}
	slices.SortFunc(frags, compareFragments)
	return frags, strays, nil
}

// compareFragments orders fragments by PR number
// so release notes read chronologically;
// non-numeric stems sort last, by name.
func compareFragments(a, b fragment) int {
	if (a.pr == 0) != (b.pr == 0) {
		if a.pr == 0 {
			return 1
		}
		return -1
	}
	if c := cmp.Compare(a.pr, b.pr); c != 0 {
		return c
	}
	return strings.Compare(a.stem, b.stem)
}

// renderSection renders fragments grouped by type in fragmentTypes order.
func renderSection(frags []fragment) (string, error) {
	var out strings.Builder
	for _, t := range fragmentTypes {
		wroteHeading := false
		for _, f := range frags {
			if f.typ != t.Key {
				continue
			}
			if !wroteHeading {
				if out.Len() > 0 {
					out.WriteString("\n")
				}
				fmt.Fprintf(&out, "### %s\n\n", t.Heading)
				wroteHeading = true
			}
			raw, err := os.ReadFile(f.path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "- %s\n", strings.TrimSpace(string(raw)))
		}
	}
	return out.String(), nil
}

func writeFragment(dir string, pr int, typ, message string) (string, error) {
	if pr <= 0 {
		return "", errors.New("a positive PR number is required")
	}
	if !validType(typ) {
		return "", fmt.Errorf("unknown type %q (want %s)", typ, typeKeys())
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("a fragment message is required")
	}
	// A multi-line message would break the one-bullet-per-fragment
	// rendering, and an embedded "## " line would truncate extract.
	if strings.ContainsAny(message, "\r\n") {
		return "", errors.New("a fragment message must be a single line")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.%s.md", pr, typ))
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; edit it instead", path)
	}
	content := fmt.Sprintf("%s (#%d)\n", message, pr)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func prompt(in *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	line, err := in.ReadString('\n')
	line = strings.TrimSpace(line)
	// Piped input often ends without a trailing newline;
	// an answer delivered alongside io.EOF still counts.
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return line, nil
}

func validType(typ string) bool {
	return slices.ContainsFunc(fragmentTypes, func(t fragmentType) bool { return t.Key == typ })
}

func typeKeys() string {
	keys := make([]string, len(fragmentTypes))
	for i, t := range fragmentTypes {
		keys[i] = t.Key
	}
	return strings.Join(keys, ", ")
}
