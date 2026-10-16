package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/your-moon/moon/internal/issues"
)

func runArgs(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, issues.Env{Getenv: func(string) string { return "" }, Out: &out, Err: &errb, Cwd: "/nonexistent"})
	return code, out.String(), errb.String()
}

func TestRun(t *testing.T) {
	old := version
	version = "9.9.9"
	defer func() { version = old }()

	if c, o, _ := runArgs("version"); c != 0 || o != "moon 9.9.9\n" {
		t.Errorf("version: %d %q", c, o)
	}
	for _, a := range [][]string{nil, {"help"}} {
		if c, o, _ := runArgs(a...); c != 0 || !strings.HasPrefix(o, "usage: moon") {
			t.Errorf("%v: %d %q", a, c, o)
		}
	}
	if c, _, e := runArgs("bogus"); c != 2 || !strings.Contains(e, "unknown command") {
		t.Errorf("unknown: %d %q", c, e)
	}
	// issues routes to issues.Main: its help is not the top-level usage; with no config it must fail.
	if c, o, _ := runArgs("issues", "help"); c != 0 || o == "" || strings.HasPrefix(o, "usage: moon <command>") {
		t.Errorf("issues help: %d %q", c, o)
	}
	if c, _, _ := runArgs("issues", "ls"); c == 0 {
		t.Errorf("issues ls without config must fail, got 0")
	}
}
