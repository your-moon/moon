package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/issues"
)

func runWith(t *testing.T, term bool, env map[string]string) (int, string, int) {
	t.Helper()
	oldT, oldR := isTerminal, runProgram
	defer func() { isTerminal, runProgram = oldT, oldR }()
	isTerminal = func(any) bool { return term }
	started := 0
	runProgram = func(tea.Model) error { started++; return nil }
	mi := newMirror(t, base)
	var out, errb bytes.Buffer
	code := Run([]string{"--config", mi.cfg}, issues.Env{Getenv: func(k string) string { return env[k] }, Stdin: strings.NewReader(""), Out: &out, Err: &errb, Cwd: mi.dir})
	return code, errb.String(), started
}

func TestRunRefusesWithoutTerminal(t *testing.T) {
	code, msg, started := runWith(t, false, nil)
	if code != 2 || started != 0 || msg != needTerminal+"\n" {
		t.Fatalf("code %d started %d msg %q", code, started, msg)
	}
}

func TestRunRefusesWhenMoonNoTUI(t *testing.T) {
	code, msg, started := runWith(t, true, map[string]string{"MOON_NO_TUI": "1"})
	if code != 2 || started != 0 || !strings.Contains(msg, "(MOON_NO_TUI is set)") || !strings.Contains(msg, needTerminal) {
		t.Fatalf("code %d started %d msg %q", code, started, msg)
	}
}

func TestRunStartsOnATerminal(t *testing.T) {
	code, msg, started := runWith(t, true, nil)
	if code != 0 || started != 1 || msg != "" {
		t.Fatalf("code %d started %d msg %q", code, started, msg)
	}
}
