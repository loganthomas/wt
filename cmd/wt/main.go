package main

import (
	"os"

	"github.com/loganthomas/wt/internal/cli"
)

// Populated at release time by goreleaser via ldflags;
// cli.BuildInfo supplies the development-build fallbacks.
var (
	version string
	commit  string
	date    string
)

func main() {
	os.Exit(cli.Main(cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
