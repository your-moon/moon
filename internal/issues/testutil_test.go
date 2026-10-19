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

// The mirror, through its collections. Tests seed and assert with these, so a change to how the store keeps
// its data does not touch them.

func (e *env) store() mirrorStore { return e.app().mirror() }

// putFile and getFile address a repo's data file (index, prs, milestones, labels, lastpull, cursor).
func (e *env) putFile(short string, f repoFile, text string) {
	e.t.Helper()
	if _, err := e.store().Issues.Write(short, f, text); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) getFile(short string, f repoFile) string {
	e.t.Helper()
	s, err := e.store().Issues.Read(short, f)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) hasFile(short string, f repoFile) bool { return e.store().Issues.Has(short, f) }

func (e *env) putIssue(short, ident, text string) {
	e.t.Helper()
	if err := e.store().Issues.Put(short, ident, text); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) getIssue(short, ident string) string {
	e.t.Helper()
	s, err := e.store().Issues.Get(short, ident)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) hasIssue(short, ident string) bool { return e.store().Issues.Exists(short, ident) }

func (e *env) putPR(short string, n int, text string) {
	e.t.Helper()
	if _, err := e.store().Issues.PutPR(short, n, text); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) putClaim(short, ident, body string) {
	e.t.Helper()
	if err := e.store().Claims.Put(e.store().Claims.Key(short, ident), body); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) getClaim(short, ident string) string {
	e.t.Helper()
	b, err := e.store().Claims.Get(e.store().Claims.Key(short, ident))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) hasClaim(short, ident string) bool {
	_, err := e.store().Claims.Get(e.store().Claims.Key(short, ident))
	return err == nil
}

// mapLocal records that local id lid of short became GitHub number n; localmap lists what is recorded, one
// "short<TAB>lid<TAB>n" line per row.
func (e *env) mapLocal(short, lid string, n int) {
	e.t.Helper()
	os.MkdirAll(e.home, 0o755)
	if err := e.store().LocalIDs.Append(short, lid, n); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) localmap() string {
	var b strings.Builder
	for _, r := range e.store().LocalIDs.Rows() {
		fmt.Fprintf(&b, "%s\t%s\t%d\n", r.Short, r.Lid, r.N)
	}
	return b.String()
}

// outboxKeys lists the queued ops by key; outboxText is one op as stored.
func (e *env) outboxKeys() []string { return e.store().Outbox.List() }

func (e *env) outboxText(key string) string {
	e.t.Helper()
	b, err := e.store().Outbox.Get(key)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// putAutosyncLog is what the last background sync wrote.
func (e *env) putAutosyncLog(text string) {
	os.MkdirAll(e.home, 0o755)
	lf, err := e.store().Markers.Log()
	if err != nil {
		e.t.Fatal(err)
	}
	defer lf.Close()
	lf.WriteString(text)
}

// blockLocalmap makes the step after a create fail (the localmap cannot be written); the returned func lets it work.
func (e *env) blockLocalmap() func() {
	os.MkdirAll(filepath.Join(e.home, "localmap.tsv"), 0o755)
	return func() { e.rawRemove("localmap.tsv") }
}

// writeConfig replaces the config file (moon.toml next to the mirror).
func (e *env) writeConfig(text string) {
	if err := os.WriteFile(e.cfg, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// Raw paths under the mirror: only the on-disk format tests (format_test.go) and fault injection use these.

func (e *env) rawWrite(rel, content string) {
	p := filepath.Join(e.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) rawRead(rel string) string {
	b, err := os.ReadFile(filepath.Join(e.home, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) rawExists(rel string) bool {
	_, err := os.Stat(filepath.Join(e.home, rel))
	return err == nil
}

func (e *env) rawRemove(rel string) { os.Remove(filepath.Join(e.home, rel)) }

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
