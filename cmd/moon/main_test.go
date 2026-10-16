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

func TestIssuesTUIArgs(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
		ok   bool
	}{
		{[]string{"tui"}, "", true},
		{[]string{"tui", "--agent", "x"}, "--agent x", true},
		{[]string{"--config", "f", "tui"}, "--config f", true},
		{[]string{"--dir=d", "tui", "--agent=a"}, "--dir=d --agent=a", true},
		{[]string{"ls"}, "", false},
		{[]string{"--config", "f", "ls"}, "", false},
		{nil, "", false},
	} {
		got, ok := issuesTUIArgs(c.in)
		if ok != c.ok || strings.Join(got, " ") != c.want {
			t.Errorf("%v: %q %v", c.in, got, ok)
		}
	}
	if _, o, _ := runArgs("help"); !strings.Contains(o, "tui ") {
		t.Errorf("help does not list tui: %q", o)
	}
}

func TestHelpAndTypos(t *testing.T) {
	// help needs no moon.toml (Cwd is /nonexistent in runArgs)
	for _, a := range [][]string{{"issues", "ls", "--help"}, {"issues", "ls", "-h"}, {"issues", "help", "ls"}, {"help", "issues", "ls"}, {"issues", "milestones", "show", "--help"}} {
		if c, o, e := runArgs(a...); c != 0 || e != "" || !strings.Contains(o, "examples:") {
			t.Errorf("%v: %d %q %q", a, c, o, e)
		}
	}
	if c, _, e := runArgs("isues"); c != 2 || !strings.Contains(e, "did you mean issues?") {
		t.Errorf("isues: %d %q", c, e)
	}
	if c, _, e := runArgs("help", "isues"); c != 2 || !strings.Contains(e, "did you mean issues?") {
		t.Errorf("help isues: %d %q", c, e)
	}
	if c, _, e := runArgs("issues", "lst"); c != 2 || !strings.Contains(e, "did you mean ls?") {
		t.Errorf("issues lst: %d %q", c, e)
	}
}
