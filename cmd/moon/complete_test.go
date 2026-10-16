package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// moonBinary builds the real binary once, so the shell tests run the same program a user would.
func moonBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "moonbin")
		if buildErr == nil {
			out, err := exec.Command("go", "build", "-o", filepath.Join(binDir, "moon"), ".").CombinedOutput()
			if err != nil {
				buildErr = fmt.Errorf("%v\n%s", err, out)
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func writeMirror(t *testing.T, issues int) string {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "moon.toml"), []byte("mirror_dir = \"mirror\"\n[repos.erp]\ngithub = \"example/erp\"\n"), 0o644)
	var b strings.Builder
	for i := 1; i <= issues; i++ {
		fmt.Fprintf(&b, "%d\topen\tv1\tkind:bug\t2026-02-%02dT00:00:00Z\tinvented issue %d\n", i, 1+i%27, i)
	}
	os.MkdirAll(filepath.Join(d, "mirror", "erp"), 0o755)
	os.WriteFile(filepath.Join(d, "mirror", "erp", "index.tsv"), []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(d, "mirror", "erp", "milestones.tsv"), []byte("v1\topen\t0\t1\t\nv1 beta\topen\t0\t1\t\n"), 0o644)
	return d
}

func TestCompleteCommandEndToEnd(t *testing.T) {
	bin := moonBinary(t)
	d := writeMirror(t, 30)
	cmd := exec.Command(filepath.Join(bin, "moon"), "__complete", "issues", "show", "erp#2")
	cmd.Env = append(os.Environ(), "MOON_CONFIG="+filepath.Join(d, "moon.toml"), "ISSUES_NO_AUTOSYNC=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[len(lines)-1] != ":4" || !strings.HasPrefix(lines[0], "erp#2") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	// $MOON_CONFIG is the same lookup the CLI uses; a bad path stays silent
	cmd = exec.Command(filepath.Join(bin, "moon"), "__complete", "issues", "show", "")
	cmd.Env = append(os.Environ(), "MOON_CONFIG="+filepath.Join(d, "nope.toml"))
	out, err = cmd.CombinedOutput()
	if err != nil || string(out) != ":4\n" {
		t.Fatalf("no config must be silent: %v %q", err, out)
	}
}

func TestCompletionProcessStaysUnder100msOn1000Issues(t *testing.T) {
	bin := moonBinary(t)
	d := writeMirror(t, 1000)
	best := time.Hour
	for i := 0; i < 7; i++ {
		cmd := exec.Command(filepath.Join(bin, "moon"), "__complete", "issues", "claim", "erp#")
		cmd.Env = append(os.Environ(), "MOON_CONFIG="+filepath.Join(d, "moon.toml"))
		start := time.Now()
		out, err := cmd.Output()
		if el := time.Since(start); el < best {
			best = el
		}
		if err != nil || strings.Count(string(out), "\n") != 201 {
			t.Fatalf("%v: %d lines", err, strings.Count(string(out), "\n"))
		}
	}
	t.Logf("whole process, 1000 issues: %v", best)
	if best > 100*time.Millisecond {
		t.Fatalf("best of 7 took %v, want < 100ms", best)
	}
}

// bashComplete sources the real script and runs the completion function for one command line.
func bashComplete(t *testing.T, bin, cfg string, words []string, line string) []string {
	t.Helper()
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	script := fmt.Sprintf(`source <(moon completion bash)
COMP_WORDS=(%s); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); COMP_LINE='%s'; COMP_POINT=${#COMP_LINE}
_moon_complete
printf '%%s\n' "${COMPREPLY[@]}"`, strings.Join(quoted, " "), strings.ReplaceAll(line, "'", `'\''`))
	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "MOON_CONFIG="+cfg, "ISSUES_NO_AUTOSYNC=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestBashFunctionEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	bin := moonBinary(t)
	d := writeMirror(t, 12)
	cfg := filepath.Join(d, "moon.toml")
	join := func(l []string) string { return strings.Join(l, "|") }
	if got := join(bashComplete(t, bin, cfg, []string{"moon", "issues", "show", "erp#1"}, "moon issues show erp#1")); got != "erp#12|erp#11|erp#10|erp#1" {
		t.Errorf("refs: %s", got)
	}
	if got := join(bashComplete(t, bin, cfg, []string{"moon", "issues", "ls", "-s", ""}, "moon issues ls -s ")); got != "number|updated|created|title" {
		t.Errorf("sort values: %s", got)
	}
	// a milestone with a space: escaped when unquoted, quote kept when the user opened one
	if got := join(bashComplete(t, bin, cfg, []string{"moon", "issues", "ls", "-m", `v1\ b`}, `moon issues ls -m v1\ b`)); got != `v1\ beta` {
		t.Errorf("unquoted milestone: %s", got)
	}
	if got := join(bashComplete(t, bin, cfg, []string{"moon", "issues", "ls", "-m", `"v1 b`}, `moon issues ls -m "v1 b`)); got != `"v1 beta` {
		t.Errorf("double-quoted milestone: %s", got)
	}
	// bash splits words at ':' ; the label kind:bug must still complete to the rest of the word
	if got := join(bashComplete(t, bin, cfg, []string{"moon", "issues", "ls", "-L", "kind", ":"}, "moon issues ls -L kind:")); got != ":bug" {
		t.Errorf("colon label: %s", got)
	}
	// files come from compgen when the engine has nothing and allows them
	os.WriteFile(filepath.Join(d, "notes.md"), nil, 0o644)
	script := fmt.Sprintf("cd %s; source <(moon completion bash); COMP_WORDS=(moon issues new --body-file no); COMP_CWORD=4; COMP_LINE='moon issues new --body-file no'; COMP_POINT=${#COMP_LINE}; _moon_complete; printf '%%s\\n' \"${COMPREPLY[@]}\"", d)
	cmd := exec.Command("bash", "--norc", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "MOON_CONFIG="+cfg)
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "notes.md" {
		t.Errorf("files: %q", out)
	}
}

func TestCompletionCommand(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish"} {
		if c, o, _ := runArgs("completion", sh); c != 0 || !strings.Contains(o, "__complete") {
			t.Errorf("%s: %d %q", sh, c, o)
		}
	}
	if c, _, e := runArgs("completion", "tcsh"); c != 2 || !strings.Contains(e, "unknown shell") {
		t.Errorf("bad shell: %d %q", c, e)
	}
	if c, _, _ := runArgs("completion"); c != 2 {
		t.Errorf("no shell: %d", c)
	}
	if _, o, _ := runArgs("help"); strings.Contains(o, "__complete") {
		t.Errorf("__complete must stay hidden: %q", o)
	}
}
