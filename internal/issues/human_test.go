package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// humanFixture writes a small mirror of invented data. Ties are deliberate: #8 and #9 share an update time,
// and #4 and #5 share a title apart from case.
func humanFixture(e *env) {
	e.write("app/index.tsv", strings.Join([]string{
		"12\topen\tv1.0\tkind:bug,area:ui\t2026-02-10T09:00:00Z\tlogin: button does nothing on Safari when the session cookie has expired and the user retries",
		"9\topen\t\tkind:feature\t2026-02-05T09:00:00Z\texport: add CSV download",
		"8\topen\tv1.0\tarea:docs,kind:cleanup\t2026-02-05T09:00:00Z\tdocs: quickstart mentions a removed flag",
		"5\tclosed\t\tkind:bug\t2026-01-20T09:00:00Z\tsearch: Slow on big projects",
		"4\topen\t\t\t2026-01-15T09:00:00Z\tsearch: slow on big projects",
		"",
	}, "\n"))
	e.write("app/lastpull", "1772366400\n") // 2026-03-01T12:00:00Z minus 0s
	e.write("app/milestones.tsv", "v1.0\topen\t3\t4\t2026-04-01T00:00:00Z\nv0.9\tclosed\t10\t0\t\n")
	e.write("lib/index.tsv", "3\topen\t\tkind:bug\t2026-02-12T09:00:00Z\tparser: crashes on empty input\n")
	e.write("lib/lastpull", "1772366400\n")
	e.write("app/prs.tsv", strings.Join([]string{
		"31\topen\t0\tfix-login\tabc\tmain\t2026-02-09T00:00:00Z\t\t\tdana\t2026-02-10T00:00:00Z\t12\tlogin: handle expired cookie",
		"30\tmerged\t0\tcsv-export\tdef\tmain\t2026-02-01T00:00:00Z\t2026-02-06T00:00:00Z\tfff\tdana\t2026-02-06T00:00:00Z\t9\texport: CSV download",
		"29\topen\t1\twip-search\tghi\tmain\t2026-01-30T00:00:00Z\t\t\tlee\t2026-02-06T00:00:00Z\t\tsearch: index tuning",
		"",
	}, "\n"))
	e.write("claims/app-12.json", `{"agent": "alice", "pid": 0, "host": "box", "ts": 1772365200, "ttl": 7200}`+"\n") // claimed 20m ago
}

func refsOf(out string) []string {
	var r []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			r = append(r, strings.Fields(l)[0])
		}
	}
	return r
}

func TestPipedOutputUnchanged(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"ls": {[]string{"ls", "--state", "all", "--repo", "app"}, "app#12\topen\tv1.0\tkind:bug,area:ui\tlogin: button does nothing on Safari when the session cookie has expired and the user retries\n" +
			"app#9\topen\t-\tkind:feature\texport: add CSV download\napp#8\topen\tv1.0\tarea:docs,kind:cleanup\tdocs: quickstart mentions a removed flag\n" +
			"app#5\tclosed\t-\tkind:bug\tsearch: Slow on big projects\napp#4\topen\t-\t-\tsearch: slow on big projects\n"},
		"frontier":   {[]string{"frontier", "--repo", "lib"}, "lib#3\topen\t-\tkind:bug\tparser: crashes on empty input\n"},
		"milestones": {[]string{"milestones", "--repo", "app"}, "app\tv1.0\topen\t3\t4\t2026-04-01T00:00:00Z\napp\tv0.9\tclosed\t10\t0\t\n"},
		"prs":        {[]string{"prs", "--state", "all"}, "app#31\topen\tfix-login\tlogin: handle expired cookie\napp#30\tmerged\tcsv-export\texport: CSV download\napp#29\topen [draft]\twip-search\tsearch: index tuning\n"},
		"status":     {[]string{"status"}, "outbox=0 errors=0\napp: last pull 0s ago, issues=5\nlib: last pull 0s ago, issues=1\n"},
		"empty":      {[]string{"ls", "--repo", "app", "--label", "nope"}, "\n"},
	} {
		out, errs, code := e.run(c.args...)
		if code != 0 || out != c.want || errs != "" {
			t.Errorf("%s: code %d\n got: %q\nwant: %q\nerr: %q", name, code, out, c.want, errs)
		}
	}
	out, _, _ := e.run("claims")
	if !strings.HasPrefix(out, "app#12\tfresh\talice\tbox\tpid=-\tage=") || !strings.HasSuffix(out, "s\tttl=7200s\n") {
		t.Errorf("claims: %q", out)
	}
	// --format auto when not a tty is the same bytes as --format tsv
	for _, cmd := range [][]string{{"ls", "--state", "all"}, {"prs", "--state", "all"}, {"frontier"}, {"milestones"}, {"claims"}, {"status"}} {
		a, _, _ := e.run(cmd...)
		b, _, _ := e.run(append(cmd, "--format", "tsv")...)
		c, _, _ := e.run(append(cmd, "--format", "auto")...)
		if a != b || a != c {
			t.Errorf("%v: auto/tsv differ", cmd)
		}
	}
	// no colour bytes without a tty even when a table is forced
	out, _, _ = e.run("ls", "--format", "table")
	if strings.Contains(out, "\x1b") || !strings.HasPrefix(out, "REF ") {
		t.Errorf("forced table off a tty: %q", out)
	}
}

func TestAutoPicksTableOnTTY(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	e.tty, e.width = true, 100
	out, errs, code := e.run("ls", "--repo", "app")
	if code != 0 || !strings.HasPrefix(out, "\x1b[1mREF") {
		t.Fatalf("tty ls should be a coloured table: %d %q", code, out)
	}
	if errs != "\x1b[90mscope: app (-R app)\x1b[0m\nshowing 4 of 4\n" {
		t.Errorf("footer: %q", errs)
	}
	tsv, _, _ := e.run("ls", "--repo", "app", "--format", "tsv")
	if strings.Contains(tsv, "\x1b") || !strings.HasPrefix(tsv, "app#12\topen\tv1.0") {
		t.Errorf("tty --format tsv: %q", tsv)
	}
	e.extra["NO_COLOR"] = "1"
	if out, _, _ = e.run("ls"); strings.Contains(out, "\x1b") {
		t.Errorf("NO_COLOR ignored: %q", out)
	}
	delete(e.extra, "NO_COLOR")
	if out, _, _ = e.run("ls", "--no-color"); strings.Contains(out, "\x1b") {
		t.Errorf("--no-color ignored")
	}
}

func TestTableGolden(t *testing.T) {
	for _, c := range []struct {
		name  string
		width int
		args  []string
	}{
		{"ls_w100", 100, []string{"ls", "--state", "all"}},
		{"ls_w60", 60, []string{"ls", "--state", "all"}},
		{"prs_w80", 80, []string{"prs", "--state", "all"}},
		{"claims_w80", 80, []string{"claims"}},
		{"milestones_w80", 80, []string{"milestones"}},
		{"status_w80", 80, []string{"status"}},
		{"frontier_w80", 80, []string{"frontier"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			humanFixture(e)
			e.tty, e.width, e.extra["NO_COLOR"] = true, c.width, "1"
			out, errs, code := e.run(c.args...)
			if code != 0 {
				t.Fatal(code, errs)
			}
			got := out + "--- stderr ---\n" + errs
			path := filepath.Join("testdata", c.name+".golden")
			if os.Getenv("MOON_UPDATE_GOLDEN") != "" {
				os.WriteFile(path, []byte(got), 0o644)
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			eq(t, c.name, got, string(want))
			for _, l := range strings.Split(out, "\n") {
				if runeLen(l) > c.width {
					t.Errorf("line wider than %d: %q", c.width, l)
				}
			}
		})
	}
}

func TestColorOnlyOnTTY(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	e.tty, e.width = true, 100
	out, _, _ := e.run("ls", "--repo", "app", "--state", "all")
	if !strings.Contains(out, "\x1b[32mopen\x1b[0m") || !strings.Contains(out, "\x1b[91mclosed\x1b[0m") {
		t.Errorf("state colours missing: %q", out)
	}
	if !strings.Contains(out, "\x1b[94min review: PR #31\x1b[0m") {
		t.Errorf("status colour missing: %q", out)
	}
}

func TestSortLimit(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"default", []string{"ls", "--state", "all", "--repo", "app"}, "app#12 app#9 app#8 app#5 app#4"},
		{"number", []string{"ls", "--state", "all", "--repo", "app", "--sort", "number"}, "app#12 app#9 app#8 app#5 app#4"},
		{"number reversed", []string{"ls", "--state", "all", "--repo", "app", "--sort", "number", "-r"}, "app#4 app#5 app#8 app#9 app#12"},
		{"updated keeps the tie in index order", []string{"ls", "--state", "all", "--repo", "app", "--sort", "updated"}, "app#12 app#9 app#8 app#5 app#4"},
		{"updated reversed keeps the tie in index order", []string{"ls", "--state", "all", "--repo", "app", "--sort", "updated", "--reverse"}, "app#4 app#5 app#9 app#8 app#12"},
		{"title is case-insensitive, ties stable", []string{"ls", "--state", "all", "--repo", "app", "--sort", "title"}, "app#8 app#9 app#12 app#5 app#4"},
		{"title reversed", []string{"ls", "--state", "all", "--repo", "app", "--sort", "title", "-r"}, "app#5 app#4 app#12 app#9 app#8"},
		{"created is number order", []string{"ls", "--state", "all", "--repo", "app", "--sort", "created"}, "app#12 app#9 app#8 app#5 app#4"},
		{"two repos by updated", []string{"ls", "--sort", "updated"}, "lib#3 app#12 app#9 app#8 app#4"},
		{"-r alone reverses the default", []string{"ls", "--repo", "app", "-r"}, "app#4 app#8 app#9 app#12"},
		{"limit", []string{"ls", "--state", "all", "--repo", "app", "--limit", "2"}, "app#12 app#9"},
		{"limit after sort", []string{"ls", "--state", "all", "--repo", "app", "--sort", "title", "--limit", "2"}, "app#8 app#9"},
		{"limit 0 is all", []string{"ls", "--state", "all", "--repo", "app", "--limit", "0"}, "app#12 app#9 app#8 app#5 app#4"},
		{"limit larger than the list", []string{"ls", "--repo", "app", "--limit", "99"}, "app#12 app#9 app#8 app#4"},
		{"prs sort title", []string{"prs", "--state", "all", "--sort", "title"}, "app#30 app#31 app#29"},
		{"prs sort updated", []string{"prs", "--state", "all", "--sort", "updated"}, "app#31 app#30 app#29"},
		{"prs sort created reversed", []string{"prs", "--state", "all", "--sort", "created", "-r"}, "app#29 app#30 app#31"},
		{"prs limit", []string{"prs", "--state", "all", "--limit", "1"}, "app#31"},
		{"frontier sort title", []string{"frontier", "--sort", "title", "--limit", "2"}, "app#8 app#9"},
	} {
		out, errs, code := e.run(c.args...)
		if code != 0 || strings.Join(refsOf(out), " ") != c.want {
			t.Errorf("%s: code %d got %q want %q (%s)", c.name, code, strings.Join(refsOf(out), " "), c.want, errs)
		}
	}
	if _, errs, code := e.run("ls", "--limit", "-1"); code != 2 || !strings.Contains(errs, "--limit") {
		t.Errorf("negative limit: %d %q", code, errs)
	}
	if _, _, code := e.run("ls", "--sort", "color"); code != 2 {
		t.Errorf("bad sort key accepted")
	}
}

func TestDefaultLimitOnTTY(t *testing.T) {
	e := newEnv(t)
	var b strings.Builder
	for i := 40; i >= 1; i-- {
		fmt.Fprintf(&b, "%d\topen\t\t\t2026-02-01T00:00:00Z\tissue number %d\n", i, i)
	}
	e.write("app/index.tsv", b.String())
	e.write("app/lastpull", "1772366400\n")
	if out, _, _ := e.run("ls", "--repo", "app"); len(refsOf(out)) != 40 {
		t.Errorf("piped default must be unlimited, got %d", len(refsOf(out)))
	}
	e.tty, e.width = true, 100
	out, errs, _ := e.run("ls", "--repo", "app", "--no-color")
	if n := len(refsOf(out)) - 1; n != 30 || errs != "scope: app (-R app)\nshowing 30 of 40 (use --limit 0 for all)\n" {
		t.Errorf("tty default: %d rows, footer %q", n, errs)
	}
	out, errs, _ = e.run("ls", "--repo", "app", "--no-color", "--limit", "0")
	if n := len(refsOf(out)) - 1; n != 40 || errs != "scope: app (-R app)\nshowing 40 of 40\n" {
		t.Errorf("--limit 0: %d rows, footer %q", n, errs)
	}
	if out, _, _ = e.run("ls", "--repo", "app", "--format", "tsv"); len(refsOf(out)) != 40 {
		t.Errorf("tty tsv must be unlimited")
	}
	if out, _, _ = e.run("ls", "--repo", "app", "--format", "json"); strings.Count(out, `"ref"`) != 40 {
		t.Errorf("tty json must be unlimited")
	}
}

func TestJSONFormat(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	out, _, code := e.run("ls", "--repo", "lib", "--format", "json")
	eq(t, "ls json", out, `[{"ref": "lib#3", "state": "open", "status": "open", "status_detail": "open", "milestone": null, "labels": ["kind:bug"], "title": "parser: crashes on empty input", "updated_at": "2026-02-12T09:00:00Z"}]`+"\n")
	if code != 0 {
		t.Fatal(code)
	}
	a, _, _ := e.run("prs", "--json")
	b, _, _ := e.run("prs", "--format", "json")
	eq(t, "prs --json vs --format json", a, b)
	if out, _, _ = e.run("claims", "--format", "json"); !strings.Contains(out, `"agent": "alice"`) || !strings.Contains(out, `"ttl_seconds": 7200`) {
		t.Errorf("claims json: %q", out)
	}
	if out, _, _ = e.run("status", "--format", "json"); !strings.HasPrefix(out, `{"outbox": 0, "errors": 0`) {
		t.Errorf("status json: %q", out)
	}
	if _, errs, code := e.run("show", "app#12", "--format", "json"); code != 2 || !strings.Contains(errs, "no json") {
		t.Errorf("show json: %d %q", code, errs)
	}
}

func TestShowPretty(t *testing.T) {
	e := newEnv(t)
	e.write("app/index.tsv", "7\topen\tv1.0\tkind:bug\t2026-02-01T00:00:00Z\twidget: renders blank\n")
	n := fixtureIssue()
	n.Body = "This sentence is long enough that it has to be wrapped when the terminal is only fifty columns wide, which it is.\n\n```\ncode lines are never wrapped even when they are far longer than the terminal width allows\n```\n"
	n.Milestone = &struct {
		Title string `json:"title"`
	}{"v1.0"}
	n.Title = "widget: renders blank"
	e.write("app/7.md", renderIssue(n))
	plainOut, _, _ := e.run("show", "app#7")
	e.tty, e.width, e.extra["NO_COLOR"] = true, 50, "1"
	out, _, code := e.run("show", "app#7")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"app#7  widget: renders blank\n", "open · milestone v1.0 · by ghost · updated 2026-02-01T00:00:00Z\n", "labels: area::ui, bug\n", "claim:  none\n", "status: open\n",
		"code lines are never wrapped even when they are far longer than the terminal width allows\n", "## Comments\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if runeLen(l) > 50 && !strings.HasPrefix(l, "code lines") && !strings.Contains(l, "updated") {
			t.Errorf("prose line wider than 50: %q", l)
		}
	}
	if strings.Contains(out, "\nnumber: 7") || strings.HasPrefix(plainOut, "app#7") || !strings.HasPrefix(plainOut, "claim: none\n---\nnumber: 7\n") {
		t.Errorf("piped show must stay raw:\n%q", plainOut)
	}
}

func TestHelp(t *testing.T) {
	e := newEnv(t)
	for name := range commands() {
		words := strings.Fields(name)
		for _, form := range [][]string{append(append([]string{}, words...), "--help"), append(append([]string{}, words...), "-h"), append([]string{"help"}, words[0])} {
			if name == "pr show" && form[0] == "help" {
				continue // `help pr` maps to pr show below
			}
			out, errs, code := e.run(form...)
			if code != 0 || errs != "" {
				t.Errorf("%v: exit %d stderr %q", form, code, errs)
			}
			examples := 0
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "  moon issues") || strings.HasPrefix(l, "  echo") || strings.HasPrefix(l, "  ISSUES_KEEP") {
					examples++
				}
			}
			if !strings.Contains(out, "usage: ") || !strings.Contains(out, "examples:") || examples < 2 {
				t.Errorf("%v: weak help (%d examples):\n%s", form, examples, out)
			}
		}
	}
	if out, _, code := e.run("pr", "--help"); code != 0 || !strings.Contains(out, "moon issues pr show") {
		t.Errorf("pr --help: %d %q", code, out)
	}
	if out, _, code := e.run("help", "pr"); code != 0 || !strings.Contains(out, "moon issues pr show") {
		t.Errorf("help pr: %d %q", code, out)
	}
	if _, _, code := e.run("help"); code != 0 {
		t.Errorf("help: %d", code)
	}
	// a value that happens to be "-h" is not a help request
	e.write("app/index.tsv", "")
	if out, _, code := e.run("ls", "-q", "-h"); code != 0 || strings.Contains(out, "usage:") {
		t.Errorf("-q -h: %d %q", code, out)
	}
	// the sort flags are documented on ls
	if out, _, _ := e.run("ls", "--help"); !strings.Contains(out, "--sort {number|updated|created|title|status}") || !strings.Contains(out, "-r, --reverse") {
		t.Errorf("ls help lacks sort flags:\n%s", out)
	}
}

func TestDidYouMean(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		args []string
		hint string
	}{
		{[]string{"lst"}, "did you mean ls?"},
		{[]string{"claimz", "x"}, "did you mean claim"},
		{[]string{"pul"}, "did you mean pull?"},
		{[]string{"help", "frontir"}, "did you mean frontier?"},
		{[]string{"ls", "--stat", "all"}, "did you mean --state?"},
		{[]string{"ls", "--sorted", "title"}, "did you mean --sort?"},
		{[]string{"prs", "--limt", "3"}, "did you mean --limit?"},
	} {
		out, errs, code := e.run(c.args...)
		if code != 2 || out != "" || !strings.Contains(errs, c.hint) {
			t.Errorf("%v: exit %d out %q err %q (want %q)", c.args, code, out, errs, c.hint)
		}
	}
	// nothing close: no guess, still exit 2 and says what was wrong
	if _, errs, code := e.run("zzzzzz"); code != 2 || strings.Contains(errs, "did you mean") || !strings.Contains(errs, `unknown command "zzzzzz"`) {
		t.Errorf("far typo: %d %q", code, errs)
	}
	if _, errs, code := e.run("ls", "--zzzzzz"); code != 2 || !strings.Contains(errs, "unrecognized arguments: --zzzzzz") || strings.Contains(errs, "did you mean") {
		t.Errorf("far flag: %d %q", code, errs)
	}
	for in, want := range map[string]string{"lst": "ls", "ls": "ls", "": "", "milestone": "milestone", "mile": "milestone", "xq": ""} {
		if got := closest(in, commandNames()); got != want {
			t.Errorf("closest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFriendlyErrors(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	// piped: the messages scripts have always seen
	_, errs, code := e.run("show", "app#99999")
	if code != 1 || errs != "app#99999 not in the local mirror (run: issues.py pull --repo app)\n" {
		t.Errorf("piped unknown ref: %d %q", code, errs)
	}
	_, errs, code = e.run("show", "nope#1")
	eq(t, "piped bad alias", errs, "bad ref 'nope#1' (want <alias>#123 or <alias>#L-<id>)\n")
	out, _, code := e.run("claim", "app#12", "--agent", "bob")
	if code != 3 || !strings.HasPrefix(out, "TAKEN app#12 is held by alice (pid - on box, claimed ") {
		t.Errorf("piped taken: %d %q", code, out)
	}
	// terminal
	e.tty, e.width = true, 100
	_, errs, code = e.run("show", "app#99999")
	if code != 1 || errs != "no issue app#99999 in the mirror; run moon issues pull\n" {
		t.Errorf("tty unknown ref: %d %q", code, errs)
	}
	_, errs, code = e.run("pr", "show", "app#99999")
	if code != 1 || !strings.HasPrefix(errs, "no pull request app#99999 in the mirror; run moon issues pull") {
		t.Errorf("tty unknown pr: %d %q", code, errs)
	}
	_, errs, code = e.run("show", "ap#1")
	if code != 2 || !strings.Contains(errs, "did you mean app?") || !strings.Contains(errs, "configured aliases: app, lib") {
		t.Errorf("tty bad alias: %d %q", code, errs)
	}
	out, _, code = e.run("claim", "app#12", "--agent", "bob")
	if code != 3 || !strings.Contains(out, "app#12 is already claimed by alice (on box) for 20m;") || !strings.Contains(out, "expires in 1h40m") ||
		!strings.Contains(out, "moon issues release app#12 --agent alice") {
		t.Errorf("tty taken: %d %q", code, out)
	}
	// no mirror yet
	e2 := newEnv(t)
	e2.tty, e2.width = true, 100
	out, errs, code = e2.run("ls")
	if code != 0 || out != "" || errs != "\x1b[90mscope: all repos (not inside a project)\x1b[0m\nno mirror yet for app, lib; run moon issues pull\n" {
		t.Errorf("tty no mirror: %d %q %q", code, out, errs)
	}
	_, errs, code = e2.run("show", "app#1")
	if code != 1 || errs != "no mirror yet for app; run moon issues pull\n" {
		t.Errorf("tty show no mirror: %d %q", code, errs)
	}
	humanFixture(e)
	if _, errs, _ = e.run("ls", "--label", "nope"); !strings.Contains(errs, "no issues match") {
		t.Errorf("tty empty result: %q", errs)
	}
}

func TestRenderHelpers(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{{"hello", 5, "hello"}, {"hello", 4, "hel…"}, {"hello", 1, "…"}, {"hello", 0, ""}, {"héllo wörld", 6, "héllo…"}, {"", 3, ""}} {
		if got := ellipsize(c.in, c.n); got != c.want {
			t.Errorf("ellipsize(%q,%d) = %q want %q", c.in, c.n, got, c.want)
		}
	}
	got := wrapLine("  - one two three four five six seven", 20)
	if strings.Join(got, "|") != "  - one two three|  four five six|  seven" {
		t.Errorf("wrapLine: %q", got)
	}
	if got := wrapLine("abcdefghijklmnopqrstuvwxyz0123456789", 10); len(got) != 1 {
		t.Errorf("below the minimum width nothing is cut: %q", got)
	}
	if got := wrapLine("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa b", 24); len(got) != 3 || runeLen(got[0]) != 24 {
		t.Errorf("long word is split: %q", got)
	}
	for sec, want := range map[int]string{-5: "0s", 59: "59s", 60: "1m", 3599: "59m", 3600: "1h00m", 6000: "1h40m", 90000: "1d"} {
		if got := humanDur(sec); got != want {
			t.Errorf("humanDur(%d) = %q want %q", sec, got, want)
		}
	}
	tbl := renderTable([]col{{head: "A"}, {head: "T", flex: true}}, [][]cell{{plain("x"), plain("a very long title indeed")}}, 20, nil)
	eq(t, "table", tbl, "A  T\nx  a very long titl…\n")
}

// ---------- milestones ----------

func msFixture(e *env) {
	humanFixture(e)
	e.write("app/milestones.tsv", strings.Join([]string{
		"v1.0\topen\t3\t4\t2026-04-01T00:00:00Z",
		"v1.1-beta\topen\t0\t0\t",
		"V2.0\topen\t249\t1\t2026-02-01T00:00:00Z",
		"v0.9\tclosed\t10\t0\t2025-12-01T00:00:00Z",
		"v1.2\topen\t1\t2\t2026-06-30T00:00:00Z",
		"",
	}, "\n"))
}

func TestMilestonePercent(t *testing.T) {
	for _, c := range []struct{ closed, open, pct int }{{0, 0, 0}, {1, 2, 33}, {2, 1, 66}, {249, 1, 99}, {995, 5, 99}, {3, 0, 100}, {0, 5, 0}, {1, 0, 100}} {
		m := ms{closed: c.closed, open: c.open}
		if m.pct() != c.pct {
			t.Errorf("%d closed %d open: %d%%, want %d%%", c.closed, c.open, m.pct(), c.pct)
		}
	}
	if bar(249, 250, 10, false) != "[#########-]" || bar(250, 250, 10, false) != "[##########]" || bar(0, 0, 4, false) != "[----]" || bar(1, 3, 6, true) != "██░░░░" {
		t.Errorf("bar rounding: %q %q %q %q", bar(249, 250, 10, false), bar(250, 250, 10, false), bar(0, 0, 4, false), bar(1, 3, 6, true))
	}
}

func TestMilestonesTable(t *testing.T) {
	for _, w := range []int{60, 80, 120} {
		e := newEnv(t)
		msFixture(e)
		e.tty, e.width, e.extra["NO_COLOR"] = true, w, "1"
		out, errs, code := e.run("milestones")
		path := filepath.Join("testdata", fmt.Sprintf("milestones_table_w%d.golden", w))
		got := out + "--- stderr ---\n" + errs
		if os.Getenv("MOON_UPDATE_GOLDEN") != "" {
			os.WriteFile(path, []byte(got), 0o644)
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 || got != string(want) {
			t.Errorf("w%d: code %d\n%s", w, code, got)
		}
		for _, l := range strings.Split(out, "\n") {
			if runeLen(l) > w {
				t.Errorf("w%d line too wide: %q", w, l)
			}
		}
		if strings.Contains(out, "v0.9") {
			t.Errorf("closed milestone shown by default on a tty")
		}
	}
	e := newEnv(t)
	msFixture(e)
	e.tty, e.width = true, 80
	e.extra["LANG"] = "en_US.UTF-8"
	if out, _, _ := e.run("milestones"); !strings.Contains(out, "█") {
		t.Errorf("no unicode bar: %q", out)
	}
	e.extra["LANG"] = "C"
	if out, _, _ := e.run("milestones"); strings.Contains(out, "█") || !strings.Contains(out, "[") {
		t.Errorf("non-UTF-8 must be ascii: %q", out)
	}
	e.extra["LANG"] = "en_US.UTF-8"
	if out, _, _ := e.run("milestones", "--no-color"); strings.Contains(out, "█") {
		t.Errorf("--no-color must be ascii")
	}
}

func TestMilestonesSortFilter(t *testing.T) {
	e := newEnv(t)
	msFixture(e)
	titles := func(args ...string) string {
		out, errs, code := e.run(append([]string{"milestones", "--format", "tsv"}, args...)...)
		if code != 0 {
			t.Fatal(code, errs)
		}
		var r []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			r = append(r, strings.Split(l, "\t")[1])
		}
		return strings.Join(r, " ")
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "v1.0 v1.1-beta V2.0 v0.9 v1.2"},
		{[]string{"--state", "open"}, "v1.0 v1.1-beta V2.0 v1.2"},
		{[]string{"--state", "closed"}, "v0.9"},
		{[]string{"--sort", "due"}, "v0.9 V2.0 v1.0 v1.2 v1.1-beta"},
		{[]string{"--sort", "due", "-r"}, "v1.1-beta v1.2 v1.0 V2.0 v0.9"},
		{[]string{"--sort", "progress"}, "v0.9 V2.0 v1.0 v1.2 v1.1-beta"},
		{[]string{"--sort", "progress", "--reverse"}, "v1.1-beta v1.2 v1.0 V2.0 v0.9"},
		{[]string{"--sort", "open"}, "v1.0 v1.2 V2.0 v1.1-beta v0.9"},
		{[]string{"--sort", "title"}, "v0.9 v1.0 v1.1-beta v1.2 V2.0"},
		{[]string{"--sort", "title", "--limit", "2"}, "v0.9 v1.0"},
		{[]string{"--limit", "0"}, "v1.0 v1.1-beta V2.0 v0.9 v1.2"},
		{[]string{"-r"}, "v1.2 v0.9 V2.0 v1.1-beta v1.0"},
	} {
		if got := titles(c.args...); got != c.want {
			t.Errorf("%v:\n got %s\nwant %s", c.args, got, c.want)
		}
	}
	out, _, _ := e.run("milestones")
	eq(t, "piped raw", out, "app\tv1.0\topen\t3\t4\t2026-04-01T00:00:00Z\napp\tv1.1-beta\topen\t0\t0\t\napp\tV2.0\topen\t249\t1\t2026-02-01T00:00:00Z\napp\tv0.9\tclosed\t10\t0\t2025-12-01T00:00:00Z\napp\tv1.2\topen\t1\t2\t2026-06-30T00:00:00Z\n")
	e.tty, e.width = true, 80
	if out, _, _ = e.run("milestones", "--state", "all", "--no-color"); !strings.Contains(out, "v0.9") {
		t.Errorf("--state all on a tty: %q", out)
	}
	out, _, _ = e.run("milestones", "--format", "json", "--state", "all")
	if !strings.Contains(out, `"title": "V2.0", "state": "open", "closed": 249, "open": 1, "total": 250, "percent": 99, "due_on": "2026-02-01T00:00:00Z"`) {
		t.Errorf("json: %q", out)
	}
}

func TestMilestoneMatching(t *testing.T) {
	e := newEnv(t)
	msFixture(e)
	e.write("app/index.tsv", "12\topen\tv1.0\t\t2026-02-10T09:00:00Z\tone\n9\topen\tV2.0\t\t2026-02-05T09:00:00Z\ttwo\n8\topen\tv1.2\t\t2026-02-05T09:00:00Z\tthree\n")
	for typed, want := range map[string]string{"v1.0": "app#12", "V1.0": "app#12", "v2": "app#9", "v1.2": "app#8"} {
		out, errs, code := e.run("ls", "--repo", "app", "--milestone", typed)
		if code != 0 || strings.Join(refsOf(out), " ") != want {
			t.Errorf("--milestone %q: %d %q (%s)", typed, code, out, errs)
		}
	}
	if _, _, code := e.run("ls", "--repo", "app", "--milestone", "v1.0 "); code != 2 {
		t.Errorf("trailing space should not match")
	}
	_, errs, code := e.run("ls", "--repo", "app", "--milestone", "v1")
	if code != 2 || !strings.Contains(errs, "ambiguous") || !strings.Contains(errs, "v1.0, v1.1-beta, v1.2") {
		t.Errorf("ambiguous prefix: %d %q", code, errs)
	}
	_, errs, code = e.run("ls", "--repo", "app", "--milestone", "v1.9")
	if code != 2 || !strings.Contains(errs, "did you mean v0.9 or v1.0 or v1.2?") {
		t.Errorf("did you mean: %d %q", code, errs)
	}
	_, errs, code = e.run("frontier", "--milestone", "zzzzzz")
	if code != 2 || !strings.Contains(errs, "known milestones:") {
		t.Errorf("unknown: %d %q", code, errs)
	}
	if _, _, code = e.run("frontier", "--milestone", "v2"); code != 0 {
		t.Errorf("frontier prefix")
	}
	e2 := newEnv(t)
	if _, _, code := e2.run("ls", "--milestone", "whatever"); code != 0 {
		t.Errorf("no mirror: %d", code)
	}
}

func TestMilestonesShow(t *testing.T) {
	e := newEnv(t)
	msFixture(e)
	out, errs, code := e.run("milestones", "show", "v1.0", "--repo", "app")
	if code != 0 || errs != "" || out != "app#12\topen\tv1.0\tkind:bug,area:ui\tlogin: button does nothing on Safari when the session cookie has expired and the user retries\napp#8\topen\tv1.0\tarea:docs,kind:cleanup\tdocs: quickstart mentions a removed flag\n" {
		t.Errorf("piped show: %d %q %q", code, out, errs)
	}
	out, _, _ = e.run("milestones", "show", "V1.0", "--sort", "title", "--limit", "1")
	if strings.Join(refsOf(out), " ") != "app#8" {
		t.Errorf("sorted/limited: %q", out)
	}
	e.tty, e.width, e.extra["NO_COLOR"] = true, 80, "1"
	out, errs, code = e.run("milestones", "show", "v1.0")
	if code != 0 || !strings.HasPrefix(out, "v1.0  open  (app)\n") || !strings.Contains(out, "3/7 closed · 42% · 4 open · due 2026-04-01") ||
		!strings.Contains(out, "REF     REPO  STATE  STATUS") || !strings.Contains(out, "app#8") || errs != "scope: all repos (not inside a project)\nshowing 2 of 2\n" {
		t.Errorf("tty show: %d\n%s\n%q", code, out, errs)
	}
	if _, errs, code = e.run("milestones", "show", "v9"); code != 2 || !strings.Contains(errs, "no milestone 'v9'") {
		t.Errorf("unknown: %d %q", code, errs)
	}
}

// ---------- short flags ----------

func TestShortFlagsEqualLong(t *testing.T) {
	e := newEnv(t)
	msFixture(e)
	e.write("app/index.tsv", "12\topen\tv1.0\tkind:bug\t2026-02-10T09:00:00Z\tone\n9\topen\tv1.0\tkind:feature\t2026-02-05T09:00:00Z\ttwo\n8\topen\t\tkind:bug\t2026-02-07T09:00:00Z\tthree\n")
	for _, c := range []struct{ short, long []string }{
		{[]string{"ls", "-s", "updated", "-S", "all"}, []string{"ls", "--sort", "updated", "--state", "all"}},
		{[]string{"ls", "-l", "2"}, []string{"ls", "--limit", "2"}},
		{[]string{"ls", "-r"}, []string{"ls", "--reverse"}},
		{[]string{"ls", "-f", "json"}, []string{"ls", "--format", "json"}},
		{[]string{"ls", "-j"}, []string{"ls", "--format", "json"}},
		{[]string{"ls", "-t"}, []string{"ls", "--format", "tsv"}},
		{[]string{"ls", "-L", "kind:bug"}, []string{"ls", "--label", "kind:bug"}},
		{[]string{"ls", "-m", "v1.0"}, []string{"ls", "--milestone", "v1.0"}},
		{[]string{"ls", "-R", "app"}, []string{"ls", "--repo", "app"}},
		{[]string{"ls", "-q", "two"}, []string{"ls", "--q", "two"}},
		{[]string{"ls", "--query", "two"}, []string{"ls", "-q", "two"}},
		{[]string{"ls", "-n", "-f", "table"}, []string{"ls", "--no-color", "--format", "table"}},
		{[]string{"frontier", "-s", "title", "-l", "1"}, []string{"frontier", "--sort", "title", "--limit", "1"}},
		{[]string{"frontier", "-L", "kind:bug", "-m", "v1.0", "-R", "app", "-r"}, []string{"frontier", "--label", "kind:bug", "--milestone", "v1.0", "--repo", "app", "--reverse"}},
		{[]string{"prs", "-S", "all", "-s", "title", "-l", "2", "-r"}, []string{"prs", "--state", "all", "--sort", "title", "--limit", "2", "--reverse"}},
		{[]string{"prs", "-R", "app", "-j", "-S", "all"}, []string{"prs", "--repo", "app", "--json", "--state", "all"}},
		{[]string{"milestones", "-s", "due", "-r", "-l", "3", "-S", "open", "-R", "app"}, []string{"milestones", "--sort", "due", "--reverse", "--limit", "3", "--state", "open", "--repo", "app"}},
		{[]string{"milestones", "-j"}, []string{"milestones", "--format", "json"}},
		{[]string{"milestones", "show", "v1.0", "-s", "t", "-l", "1", "-S", "all"}, []string{"milestones", "show", "v1.0", "--sort", "title", "--limit", "1", "--state", "all"}},
		{[]string{"claims", "-j"}, []string{"claims", "--format", "json"}},
		{[]string{"claims", "-t"}, []string{"claims", "--format", "tsv"}},
		{[]string{"status", "-j"}, []string{"status", "--format", "json"}},
		{[]string{"show", "app#12", "-f", "tsv"}, []string{"show", "app#12", "--format", "tsv"}},
	} {
		so, se, sc := e.run(c.short...)
		lo, le, lc := e.run(c.long...)
		if so != lo || se != le || sc != lc {
			t.Errorf("%v vs %v:\n short %d %q %q\n long  %d %q %q", c.short, c.long, sc, so, se, lc, lo, le)
		}
	}
	// the short forms really do something (not just equal because both are ignored)
	if out, _, _ := e.run("ls", "-s", "title", "-l", "1"); strings.Join(refsOf(out), " ") != "app#12" {
		t.Errorf("-s title -l 1: %q", out)
	}
	if out, _, _ := e.run("ls", "-R", "app", "-r", "-l", "1"); strings.Join(refsOf(out), " ") != "app#8" {
		t.Errorf("-r -l 1: %q", out)
	}
	// agent: claim with -a
	if out, _, code := e.run("claim", "app#8", "-a", "zed"); code != 0 || out != "claimed app#8 for zed\n" {
		t.Errorf("claim -a: %d %q", code, out)
	}
	if out, _, code := e.run("release", "app#8", "-a", "zed"); code != 0 || out != "app#8: released\n" {
		t.Errorf("release -a: %d %q", code, out)
	}
	// repeated labels on new
	if _, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "b", "-L", "bug", "-L", "ui"); code != 0 {
		t.Errorf("new -L -L: %d %q", code, errs)
	}
	if got := e.read("outbox/" + func() string { ents, _ := os.ReadDir(filepath.Join(e.home, "outbox")); return ents[0].Name() }()); !strings.Contains(got, `"bug", "ui"`) {
		t.Errorf("labels not repeated: %s", got)
	}
}

func TestShortFlagsCombined(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	want := func(args []string, refs string) {
		t.Helper()
		out, errs, code := e.run(args...)
		if code != 0 || strings.Join(refsOf(out), " ") != refs {
			t.Errorf("%v: %d %q (%s), want %s", args, code, out, errs, refs)
		}
	}
	all := []string{"ls", "-R", "app", "-S", "all"}
	with := func(x ...string) []string { return append(append([]string{}, all...), x...) }
	want(with("-rl2"), "app#4 app#5")
	want(with("-l2"), "app#12 app#9")
	want(with("-l=2"), "app#12 app#9")
	want(with("--limit=2"), "app#12 app#9")
	want(with("-rs", "title", "-l", "1"), "app#5")
	want(with("-rstitle"), "app#5 app#4 app#12 app#9 app#8")
	want(with("-s", "up", "-l1"), "app#12")
	want(with("-s", "n", "-r", "-l1"), "app#4")
	want(with("-s", "t", "-l1"), "app#8")
	want(with("-s", "c", "-l1"), "app#12")
	if out, _, _ := e.run(with("-rj")...); !strings.HasPrefix(out, `[{"ref": "app#4"`) {
		t.Errorf("-rj: %q", out)
	}
	want([]string{"ls", "-R", "app", "-S", "a", "-l", "1"}, "app#12")
	want([]string{"ls", "2", "-R", "app", "-S", "all"}, "app#12 app#9")
	want([]string{"ls", "-R", "app", "-S", "all", "0"}, "app#12 app#9 app#8 app#5 app#4")
	want([]string{"frontier", "1"}, "app#9")
	// an explicit limit wins over the bare number
	if _, _, code := e.run("ls", "abc"); code != 2 {
		t.Errorf("ls abc must still be an error")
	}
	if out, _, _ := e.run("ls", "-R", "app", "-j", "-l", "1"); strings.Count(out, `"ref"`) != 1 {
		t.Errorf("-j -l 1: %q", out)
	}
	if out, _, _ := e.run("milestones", "-s", "p", "-t"); !strings.HasPrefix(out, "app\t") {
		t.Errorf("milestones -s p -t: %q", out)
	}
	// long flags and -q keep working as before
	if out, _, code := e.run("ls", "--state=all", "--repo=app", "-q", "docs"); code != 0 || strings.Join(refsOf(out), " ") != "app#8" {
		t.Errorf("long forms: %d %q", code, out)
	}
	if out, _, _ := e.run("ls", "-q", "-h"); strings.Contains(out, "usage:") {
		t.Errorf("-q -h")
	}
	// bad values still say what is wrong
	if _, errs, code := e.run("ls", "-s", "zzz"); code != 2 || !strings.Contains(errs, "invalid choice: 'zzz'") {
		t.Errorf("-s zzz: %d %q", code, errs)
	}
	if _, errs, code := e.run("ls", "-S", "o"); code != 0 {
		t.Errorf("-S o is a unique prefix: %d %q", code, errs)
	}
	if _, errs, code := e.run("ls", "-f", "t"); code != 2 || !strings.Contains(errs, "invalid choice: 't'") {
		t.Errorf("-f t is ambiguous (table, tsv): %d %q", code, errs)
	}
	if _, errs, code := e.run("ls", "-l"); code != 2 || !strings.Contains(errs, "expected one argument") {
		t.Errorf("-l without value: %d %q", code, errs)
	}
}

func TestUnknownShortFlag(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		args []string
		hint string
	}{
		{[]string{"ls", "-x"}, "unknown flag -x"},
		{[]string{"ls", "-Q", "a"}, "unknown flag -Q, did you mean -q"},
		{[]string{"ls", "-rx"}, "unknown flag -x"},
		{[]string{"ls", "-c"}, "short flags here:"},
		{[]string{"prs", "-F", "x"}, "unknown flag -F, did you mean -f (--format)?"},
		{[]string{"claims", "-s", "x"}, "unknown flag -s"},
		{[]string{"push", "-l"}, "unknown flag -l"},
	} {
		out, errs, code := e.run(c.args...)
		if code != 2 || out != "" || !strings.Contains(errs, c.hint) {
			t.Errorf("%v: %d %q %q (want %q)", c.args, code, out, errs, c.hint)
		}
	}
}

func TestShortFlagsInHelp(t *testing.T) {
	e := newEnv(t)
	out, _, _ := e.run("help")
	for _, want := range []string{"-s --sort", "-l --limit", "-r --reverse", "-f --format (-j = json, -t = tsv)", "-L --label", "-m --milestone", "-S --state", "-R --repo", "-a --agent", "-q --query", "-n --no-color", "-h --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	out, _, _ = e.run("ls", "--help")
	for _, want := range []string{"-s, --sort", "-l, --limit", "-L, --label", "-S, --state", "-f, --format", "-n, --no-color", "-j = json"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls --help lacks %q:\n%s", want, out)
		}
	}
	for name, h := range helpTexts {
		if strings.Contains(h.examples[0]+h.examples[1], "--sort") || strings.Contains(h.examples[0]+h.examples[1], "--limit") {
			t.Errorf("%s: examples should use the short forms", name)
		}
	}
}
