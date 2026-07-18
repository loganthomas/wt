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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	fragmentsDir  = ".changes"
	changelogFile = "CHANGELOG.md"
	headerLine    = "# Changelog"
)

// fragmentTypes maps fragment type keys to changelog headings,
// ordered by user impact for rendering.
var fragmentTypes = []struct {
	Key     string
	Heading string
}{
	{"enh", "Enhancements"},
	{"bug", "Fixes"},
	{"dep", "Deprecations"},
	{"doc", "Documentation"},
	{"maint", "Infrastructure"},
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
	body, _, err := pendingSection(fragmentsDir)
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
func batchChangelog(root, version, date string) error {
	body, consumed, err := pendingSection(filepath.Join(root, fragmentsDir))
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

	for _, p := range consumed {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return reportLeftovers(filepath.Join(root, fragmentsDir), consumed)
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

// pendingSection renders the staged fragments grouped by type
// and returns the file paths it consumed.
func pendingSection(dir string) (string, []string, error) {
	var out strings.Builder
	var consumed []string
	for _, t := range fragmentTypes {
		paths, err := filepath.Glob(filepath.Join(dir, "*."+t.Key+".md"))
		if err != nil {
			return "", nil, err
		}
		if len(paths) == 0 {
			continue
		}
		sort.Strings(paths)
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "### %s\n\n", t.Heading)
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				return "", nil, err
			}
			fmt.Fprintf(&out, "- %s\n", strings.TrimSpace(string(raw)))
			consumed = append(consumed, p)
		}
	}
	return out.String(), consumed, nil
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

func reportLeftovers(dir string, consumed []string) error {
	taken := make(map[string]bool, len(consumed))
	for _, p := range consumed {
		taken[filepath.Base(p)] = true
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "README.md" || taken[e.Name()] {
			continue
		}
		fmt.Fprintf(os.Stderr, "not collected: %s\n", filepath.Join(dir, e.Name()))
	}
	return nil
}

func prompt(in *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	line, err := in.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func validType(typ string) bool {
	for _, t := range fragmentTypes {
		if t.Key == typ {
			return true
		}
	}
	return false
}

func typeKeys() string {
	keys := make([]string, len(fragmentTypes))
	for i, t := range fragmentTypes {
		keys[i] = t.Key
	}
	return strings.Join(keys, ", ")
}
