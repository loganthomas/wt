package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBinaryStep(t *testing.T) {
	// A cask install reaches PATH through a symlink into the
	// Caskroom; the step must see through it to hand off to brew.
	dir := t.TempDir()
	caskBin := filepath.Join(dir, "Caskroom", "wt", "0.1.0", "wt")
	if err := os.MkdirAll(filepath.Dir(caskBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caskBin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "wt")
	if err := os.Symlink(caskBin, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		exe  string
		want []string
	}{
		{"cask via symlink", link, []string{"brew uninstall wt", "brew untap loganthomas/tap"}},
		{"cask direct", caskBin, []string{"brew uninstall wt", "brew untap loganthomas/tap"}},
		{"go install", "/Users/me/go/bin/wt", []string{"rm /Users/me/go/bin/wt"}},
		{"unknown location", "", []string{`rm "$(command -v wt)"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, binaryStep(tt.exe).commands); diff != "" {
				t.Errorf("commands (-want +got):\n%s", diff)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/Users/me/.config/wt", "/Users/me/.config/wt"},
		{"/Users/me/Application Support/wt", "'/Users/me/Application Support/wt'"},
		{"/tmp/it's", `'/tmp/it'\''s'`},
		{"~/wt", "'~/wt'"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
