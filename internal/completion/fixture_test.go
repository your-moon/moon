package completion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/your-moon/moon/internal/issues"
)

var fixedNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type fx struct {
	num                   int
	state, ms, labels     string
	updated, title, claim string // claim: agent name or ""
}

type fixture struct {
	t   *testing.T
	dir string
	cfg string
}

// newFixture writes an invented mirror: aliases erp and web with the given issues under erp.
func newFixture(t *testing.T, erp []fx) *fixture {
	d := t.TempDir()
	f := &fixture{t: t, dir: d, cfg: filepath.Join(d, "moon.toml")}
	os.WriteFile(f.cfg, []byte("mirror_dir = \"mirror\"\n[repos.erp]\ngithub = \"example/erp\"\n[repos.web]\ngithub = \"example/web\"\n"), 0o644)
	f.write("web/index.tsv", "3\topen\t\tdocs\t2026-02-01T00:00:00Z\tweb: fix typo\n")
	f.write("web/milestones.tsv", "launch\topen\t1\t2\t\n")
	f.write("erp/milestones.tsv", "v1\topen\t3\t4\t\nv1 beta\topen\t0\t2\t\nold\tclosed\t5\t0\t\n")
	var idx strings.Builder
	for _, it := range erp {
		fmt.Fprintf(&idx, "%d\t%s\t%s\t%s\t%s\t%s\n", it.num, it.state, it.ms, it.labels, it.updated, it.title)
		if it.claim != "" {
			f.write(fmt.Sprintf("claims/erp-%d.json", it.num), fmt.Sprintf(`{"agent": %q, "pid": 0, "host": "box", "ts": %d, "ttl": 7200}`, it.claim, time.Now().Unix()-10))
		}
	}
	f.write("erp/index.tsv", idx.String())
	return f
}

func (f *fixture) write(rel, s string) {
	p := filepath.Join(f.dir, "mirror", rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) opts() Options {
	return Options{
		Top: []Command{{"tui", "dashboard"}, {"issues", "issue mirror"}, {"version", "print the version"}, {"completion", "scripts"}, {"help", "usage"}},
		Open: func(cfg, dir string) Source {
			if cfg == "" {
				cfg = f.cfg
			}
			return OpenSession(cfg, dir, issues.Env{Getenv: func(string) string { return "" }, Cwd: f.dir})
		},
	}
}

func (f *fixture) complete(words ...string) Result { return Complete(f.opts(), words) }

func values(r Result) []string {
	var out []string
	for _, c := range r.Cands {
		out = append(out, c.Value)
	}
	return out
}

func list(r Result) string { return strings.Join(values(r), " ") }

var standard = []fx{
	{1, "closed", "v1", "kind:bug", "2026-01-01T00:00:00Z", "login: crash on empty password", ""},
	{2, "open", "v1", "kind:bug,area::auth", "2026-02-20T00:00:00Z", "auth: token refresh loops", "alice"},
	{3, "open", "v1 beta", "feature", "2026-02-10T00:00:00Z", "export: add CSV download", ""},
	{50, "open", "", "docs", "2026-02-25T00:00:00Z", "docs: explain config lookup", ""},
	{51, "open", "v1", "kind:bug", "2026-02-27T00:00:00Z", "widget: renders blank", ""},
	{52, "closed", "", "", "2026-01-30T00:00:00Z", "cleanup: remove dead flags", ""},
	{510, "open", "", "feature", "2026-02-28T00:00:00Z", "search: add filters", "bob"},
}

func issuesEnv(cwd string) issues.Env {
	return issues.Env{Getenv: func(string) string { return "" }, Cwd: cwd}
}
