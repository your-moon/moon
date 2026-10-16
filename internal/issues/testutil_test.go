package issues

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH is the GitHub interface for tests. Nothing here talks to a network.
type fakeGH struct {
	mu      sync.Mutex
	calls   [][]string
	handler func(args []string, stdin string) (string, error)
}

func (f *fakeGH) Run(args []string, stdin string, _ time.Duration) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{}, args...))
	h := f.handler
	f.mu.Unlock()
	if h == nil {
		return "", &GhError{Msg: "fake: no handler"}
	}
	return h(args, stdin)
}

func (f *fakeGH) count(prefix ...string) int {
	n := 0
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			n++
		}
	}
	return n
}

type env struct {
	t     *testing.T
	home  string
	cfg   string
	gh    *fakeGH
	now   time.Time
	extra map[string]string
	tick  bool
}

func newEnv(t *testing.T) *env {
	d := t.TempDir()
	cfg := filepath.Join(d, "moon.toml")
	os.WriteFile(cfg, []byte("mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n[repos.lib]\ngithub = \"example/lib\"\n"), 0o644)
	return &env{t: t, home: filepath.Join(d, "mirror"), cfg: cfg, gh: &fakeGH{}, now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
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
		Stdin:  strings.NewReader(stdin), Out: &out, Err: &errb, Cwd: filepath.Dir(e.cfg), GH: e.gh,
		Now: func() time.Time { return e.now },
	})
	return out.String(), errb.String(), code
}

func (e *env) app() *App {
	cfg, err := LoadConfig(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return &App{Home: e.home, GH: e.gh, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Stdin: strings.NewReader(""),
		Now: func() time.Time { return e.now }, Host: "testhost", Environ: func(k string) string { return e.extra[k] }, Repos: cfg.Repos}
}

func (e *env) write(rel, content string) {
	p := filepath.Join(e.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
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
