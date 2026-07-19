package cli_test

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
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
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"exitcode": cmdExitcode,
		},
		Setup: func(env *testscript.Env) error {
			// Isolate git from the developer's real config and hooks
			// so scripts behave identically everywhere, CI included.
			// The ceiling keeps repo discovery inside the work dir even
			// when the system temp dir sits under some git checkout.
			env.Setenv("GIT_CEILING_DIRECTORIES", env.WorkDir)
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_AUTHOR_NAME", "wt-test")
			env.Setenv("GIT_AUTHOR_EMAIL", "wt-test@example.invalid")
			env.Setenv("GIT_COMMITTER_NAME", "wt-test")
			env.Setenv("GIT_COMMITTER_EMAIL", "wt-test@example.invalid")
			return nil
		},
	})
}

// cmdExitcode is the shared contract assertion (PLAN.md D13):
//
//	exitcode <want> <command> [args...]
//
// runs the command and fails unless its exit code is exactly want —
// `! exec` can only distinguish zero from non-zero, which would let
// a usage error (2) impersonate a precondition failure (3).
// Stdout and stderr stay checkable with the regular stdout/stderr
// assertions afterwards.
func cmdExitcode(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("exitcode does not support negation; assert the exact code instead")
	}
	if len(args) < 2 {
		ts.Fatalf("usage: exitcode <want> <command> [args...]")
	}
	want, err := strconv.Atoi(args[0])
	ts.Check(err)
	got := 0
	if err := ts.Exec(args[1], args[2:]...); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			ts.Fatalf("exec %s: %v", args[1], err)
		}
		got = exit.ExitCode()
	}
	if got != want {
		ts.Fatalf("exit code = %d, want %d", got, want)
	}
}
