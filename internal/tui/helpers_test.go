package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/your-moon/moon/internal/issues"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

var fixedNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type fx struct {
	num                    int
	state, ms, labels      string
	updated, created, name string
	body                   string
}

// invented data only
var base = []fx{
	{1, "closed", "v1", "bug", "2026-02-01T00:00:00Z", "2026-01-05T00:00:00Z", "login: crash on empty password", ""},
	{2, "open", "v1", "bug,area::auth", "2026-02-20T00:00:00Z", "2026-01-10T00:00:00Z", "auth: token refresh loops", "The refresh call retries forever when the clock is skewed.\n\n## Comments\n\n### sam · 2026-02-21T08:00:00Z · id 9\n\nI can reproduce this."},
	{3, "open", "v2", "feature", "2026-02-10T00:00:00Z", "2026-02-01T00:00:00Z", "export: add CSV download", ""},
	{4, "open", "", "docs", "2026-02-25T00:00:00Z", "2026-02-03T00:00:00Z", "docs: explain config lookup", ""},
	{5, "open", "v2", "bug", "2026-02-27T00:00:00Z", "2026-02-05T00:00:00Z", "widget: renders blank for ünïcode and 日本語 titles", ""},
	{6, "closed", "", "", "2026-01-30T00:00:00Z", "2026-01-20T00:00:00Z", "cleanup: remove dead flags", ""},
	{7, "open", "v1", "feature,area::auth", "2026-02-15T00:00:00Z", "2026-02-15T00:00:00Z", "auth: add passkey support", ""},
}

type mirror struct {
	t    *testing.T
	dir  string
	cfg  string
	sess *issues.Session
}

func (mi *mirror) write(rel, content string) {
	p := filepath.Join(mi.dir, "mirror", rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		mi.t.Fatal(err)
	}
}

func (mi *mirror) addIssues(alias string, items []fx) {
	var idx strings.Builder
	for _, it := range items {
		fmt.Fprintf(&idx, "%d\t%s\t%s\t%s\t%s\t%s\n", it.num, it.state, it.ms, it.labels, it.updated, it.name)
		var ls []string
		for _, l := range strings.Split(it.labels, ",") {
			if l != "" {
				ls = append(ls, fmt.Sprintf("%q", l))
			}
		}
		mi.write(fmt.Sprintf("%s/%d.md", alias, it.num), fmt.Sprintf("---\nnumber: %d\ntitle: %q\nstate: %q\nstate_reason: null\nlabels: [%s]\nmilestone: %s\nassignees: []\nauthor: \"sam\"\ncreated_at: %q\nupdated_at: %q\nurl: \"https://example.invalid/%d\"\n---\n\n%s\n",
			it.num, it.name, it.state, strings.Join(ls, ", "), msJSON(it.ms), it.created, it.updated, it.num, it.body))
	}
	mi.write(alias+"/index.tsv", idx.String())
	mi.write(alias+"/lastpull", fmt.Sprint(fixedNow.Unix()-5*60)+"\n")
}

func msJSON(ms string) string {
	if ms == "" {
		return "null"
	}
	return fmt.Sprintf("%q", ms)
}

// newMirror writes a mirror with repos app (and lib when two) and opens a session at fixedNow.
func newMirror(t *testing.T, items []fx, aliases ...string) *mirror {
	d := t.TempDir()
	if len(aliases) == 0 {
		aliases = []string{"app"}
	}
	cfg := "mirror_dir = \"mirror\"\n"
	for _, a := range aliases {
		cfg += fmt.Sprintf("[repos.%s]\ngithub = \"example/%s\"\n", a, a)
	}
	mi := &mirror{t: t, dir: d, cfg: filepath.Join(d, "moon.toml")}
	os.WriteFile(mi.cfg, []byte(cfg), 0o644)
	if items != nil {
		for _, a := range aliases {
			mi.addIssues(a, items)
		}
	}
	mi.open()
	return mi
}

func (mi *mirror) open() {
	tick := 0 // a clock that moves, so queued ops sort in the order they were made
	s, err := issues.NewSession(mi.cfg, "", issues.Env{Getenv: func(k string) string { return "" }, Cwd: mi.dir, Now: func() time.Time { tick++; return fixedNow.Add(time.Duration(tick) * 2 * time.Millisecond) }})
	if err != nil {
		mi.t.Fatal(err)
	}
	mi.sess = s
}

func (mi *mirror) claim(ref, agent string, tsAgo, ttl int) {
	short, id, _ := strings.Cut(ref, "#")
	mi.write(fmt.Sprintf("claims/%s-%s.json", short, id), fmt.Sprintf(`{"agent": %q, "pid": 0, "host": "box", "ts": %d, "ttl": %d}`, agent, fixedNow.Unix()-int64(tsAgo), ttl))
}

func (mi *mirror) outbox() int {
	ents, _ := os.ReadDir(filepath.Join(mi.dir, "mirror", "outbox"))
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

func (mi *mirror) model(w, h int) Model {
	m := New(mi.sess, Options{Agent: "me", MirrorDir: "mirror"})
	return send(m, tea.WindowSizeMsg{Width: w, Height: h})
}

func send(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}
	return m
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// press sends one key per argument.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		m = send(m, keyMsg(k))
	}
	return m
}

// typed sends each rune of s as its own key.
func typed(m Model, s string) Model {
	for _, r := range s {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func refs(m Model) string {
	var out []string
	for _, e := range m.rows {
		out = append(out, e.it.Ref)
	}
	return strings.Join(out, " ")
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(p, []byte(got+"\n"), 0o644)
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if strings.TrimRight(string(want), "\n") != got {
		t.Fatalf("%s differs from golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }
