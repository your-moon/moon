package issues

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serialPush makes push send one write at a time, for tests that assert the global order of gh calls.
func serialPush(t *testing.T) {
	old := pushWorkers
	pushWorkers = 1
	t.Cleanup(func() { pushWorkers = old })
	restPush(t)
}

// restPush turns batching off, for tests that fake the per-op REST calls.
func restPush(t *testing.T) {
	old := pushBatch
	pushBatch = 1
	t.Cleanup(func() { pushBatch = old })
}

type env struct {
	t     *testing.T
	home  string
	cfg   string
	mem   *memGH // the in-memory GitHub every command talks to (repos example/app and example/lib exist)
	now   time.Time
	extra map[string]string
	tick  bool
	tty   bool // pretend stdout is a terminal
	width int
	cwd   string        // working directory for the command; default: the config's folder
	git   GitRemoteFunc // fake `git remote get-url origin`; default: no remote
}

func newEnv(t *testing.T) *env {
	d := t.TempDir()
	cfg := filepath.Join(d, "moon.toml")
	os.WriteFile(cfg, []byte("mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n[repos.lib]\ngithub = \"example/lib\"\n"), 0o644)
	return &env{t: t, home: filepath.Join(d, "mirror"), cfg: cfg, mem: newMemGH(), now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		extra: map[string]string{"ISSUES_NO_AUTOSYNC": "1"}}
}

// run executes `moon issues args...` and returns stdout, stderr, exit code.
func (e *env) run(args ...string) (string, string, int) {
	return e.runIn("", args...)
}

func (e *env) runIn(stdin string, args ...string) (string, string, int) {
	if e.tick {
		e.now = e.now.Add(time.Millisecond)
	}
	var out, errb bytes.Buffer
	code := Main(append([]string{"--config", e.cfg}, args...), Env{
		Getenv: func(k string) string { return e.extra[k] },
		Stdin:  strings.NewReader(stdin), Out: &out, Err: &errb, Cwd: e.workdir(), GH: e.github(), GitRemote: e.gitFunc(),
		Now: func() time.Time { return e.now }, TTY: e.tty, Width: e.width,
	})
	return out.String(), errb.String(), code
}

func (e *env) github() GitHub { return e.mem }

// useMem is the env's in-memory GitHub, holding example/app and example/lib.
func (e *env) useMem() *memGH {
	e.mem.repo("example/app")
	e.mem.repo("example/lib")
	return e.mem
}

func (e *env) workdir() string {
	if e.cwd != "" {
		return e.cwd
	}
	return filepath.Dir(e.cfg)
}

func (e *env) gitFunc() GitRemoteFunc {
	if e.git != nil {
		return e.git
	}
	return func(string) (string, error) { return "", fmt.Errorf("fatal: not a git repository") }
}

func (e *env) app() *App {
	cfg, err := LoadConfig(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return &App{Home: e.home, GH: e.github(), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Stdin: strings.NewReader(""),
		Now: func() time.Time { return e.now }, Host: "testhost", Environ: func(k string) string { return e.extra[k] }, Repos: cfg.Repos}
}

func (e *env) write(rel, content string) {
	p := filepath.Join(e.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// writeConfig replaces the config file (moon.toml next to the mirror).
func (e *env) writeConfig(text string) {
	if err := os.WriteFile(e.cfg, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) read(rel string) string {
	b, err := os.ReadFile(filepath.Join(e.home, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(e.home, rel))
	return err == nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func eq(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s:\n got: %q\nwant: %q", name, got, want)
	}
}

func contains0(t *testing.T, name, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%s: %q does not contain %q", name, s, sub)
	}
}

var _ = fmt.Sprint
