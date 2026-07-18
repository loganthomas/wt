package cli_test

import (
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/loganthomas/wt/internal/cli"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"wt": func() { os.Exit(cli.Main(cli.BuildInfo{})) },
	})
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			// Isolate git from the developer's real config and hooks
			// so scripts behave identically everywhere, CI included.
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_AUTHOR_NAME", "wt-test")
			env.Setenv("GIT_AUTHOR_EMAIL", "wt-test@example.invalid")
			env.Setenv("GIT_COMMITTER_NAME", "wt-test")
			env.Setenv("GIT_COMMITTER_EMAIL", "wt-test@example.invalid")
			return nil
		},
	})
}
