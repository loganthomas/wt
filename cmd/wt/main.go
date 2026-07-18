package main

import (
	"os"

	"github.com/loganthomas/wt/internal/cli"
)

// Populated at release time by goreleaser via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Main(cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
