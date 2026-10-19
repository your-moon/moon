package completion

import (
	"strings"
	"testing"

	"github.com/your-moon/moon/internal/issues"
)

func want(t *testing.T, name, got, exp string) {
	t.Helper()
	if got != exp {
		t.Errorf("%s:\n got: %s\nwant: %s", name, got, exp)
	}
}

func TestTopLevelAndSubcommands(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "top", list(f.complete("")), "tui issues version completion help")
	want(t, "top prefix", list(f.complete("i")), "issues")
	want(t, "completion shells", list(f.complete("completion", "")), "zsh bash fish")
	r := f.complete("issues", "")
	for _, c := range []string{"ls", "show", "claim", "release", "pr", "milestones", "tui", "help"} {
		if !strings.Contains(" "+list(r)+" ", " "+c+" ") {
			t.Errorf("issues commands lack %s: %s", c, list(r))
		}
	}
	want(t, "issues cl", list(f.complete("issues", "cl")), "claim claims close")
	want(t, "pr", list(f.complete("issues", "pr", "")), "show")
	if r := f.complete("issues", "milestones", ""); !strings.HasPrefix(list(r), "show") {
		t.Errorf("milestones offers show first: %s", list(r))
	}
	if r.Dir != DirNoFiles {
		t.Errorf("directive %d", r.Dir)
	}
}

func TestFlagsAndDescriptions(t *testing.T) {
	f := newFixture(t, standard)
	r := f.complete("issues", "ls", "--s")
	want(t, "--s", list(r), "--state --status --show-status --sort")
	for _, c := range r.Cands {
		if c.Desc == "" {
			t.Errorf("%s has no description", c.Value)
		}
	}
	r = f.complete("issues", "ls", "-")
	got := " " + list(r) + " "
	for _, c := range []string{"-s", "-l", "-r", "-R", "-L", "-m", "-S", "-q", "-j", "-t", "-h", "--sort", "--limit"} {
		if !strings.Contains(got, " "+c+" ") {
			t.Errorf("ls - lacks %s: %s", c, got)
		}
	}
	// a flag already given is not offered again, but a repeatable one is
	if g := list(f.complete("issues", "ls", "--sort", "title", "--s")); strings.Contains(g, "--sort") {
		t.Errorf("--sort offered twice: %s", g)
	}
	if g := list(f.complete("issues", "new", "--label", "bug", "--lab")); g != "--label" {
		t.Errorf("--label is repeatable: %q", g)
	}
	// short flags exist only where the command has the long one
	if g := list(f.complete("issues", "claims", "-")); strings.Contains(g, "-s ") || strings.HasSuffix(g, " -s") {
		t.Errorf("claims has no -s: %s", g)
	}
	want(t, "--config global", list(f.complete("issues", "ls", "--con")), "--config")
}

func TestEnumValues(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "-s", list(f.complete("issues", "ls", "-s", "")), "number updated created title status")
	want(t, "--sort=", list(f.complete("issues", "ls", "--sort=u")), "--sort=updated")
	want(t, "--state", list(f.complete("issues", "ls", "--state", "")), "open closed all")
	want(t, "-S", list(f.complete("issues", "ls", "-S", "c")), "closed")
	want(t, "prs -S", list(f.complete("issues", "prs", "-S", "")), "open merged closed all")
	want(t, "-f", list(f.complete("issues", "ls", "-f", "")), "auto table tsv json")
	want(t, "--format", list(f.complete("issues", "ls", "--format", "t")), "table tsv")
	want(t, "milestones sort", list(f.complete("issues", "milestones", "-s", "")), "due progress title open")
	want(t, "close reason", list(f.complete("issues", "close", "app#1", "--reason", "")), "completed not_planned")
	want(t, "limit has none", list(f.complete("issues", "ls", "-l", "")), "")
	if r := f.complete("issues", "ls", "-l", ""); r.Dir != DirNoFiles {
		t.Errorf("--limit must not fall back to files")
	}
}

func TestMidLineFlags(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "ls -s", list(f.complete("issues", "ls", "-s", "")), "number updated created title status")
	want(t, "ls -R erp -L", list(f.complete("issues", "ls", "-R", "erp", "-L", "")), "area::auth auth bug docs feature kind:bug")
	want(t, "ls -R web -L", list(f.complete("issues", "ls", "-R", "web", "-L", "")), "docs")
	want(t, "ls -R", list(f.complete("issues", "ls", "-R", "")), "erp web all")
	want(t, "--repo=", list(f.complete("issues", "ls", "--repo=w")), "--repo=web")
	// the config can come from the line
	want(t, "--config before cmd", list(f.complete("issues", "--config", f.cfg, "ls", "-R", "")), "erp web all")
	want(t, "--config after cmd", list(f.complete("issues", "ls", "--config="+f.cfg, "-R", "")), "erp web all")
}

func TestCombinedShortFlags(t *testing.T) {
	f := newFixture(t, standard)
	r := f.complete("issues", "ls", "-r")
	got := " " + list(r) + " "
	for _, c := range []string{"-rs", "-rl", "-rL", "-rR", "-rj"} {
		if !strings.Contains(got, " "+c+" ") {
			t.Errorf("-r lacks %s: %s", c, got)
		}
	}
	if strings.Contains(got, " -rr ") {
		t.Errorf("-r offered again: %s", got)
	}
	want(t, "-rs value", list(f.complete("issues", "ls", "-rs")), "-rsnumber -rsupdated -rscreated -rstitle -rsstatus")
	want(t, "-rs then word", list(f.complete("issues", "ls", "-rs", "")), "number updated created title status")
	want(t, "-rl5 inline value", list(f.complete("issues", "ls", "-rl5")), "")
	want(t, "-rR then word", list(f.complete("issues", "ls", "-rR", "")), "erp web all")
	want(t, "-x unknown", list(f.complete("issues", "ls", "-x")), "")
}

func TestLabelsStoredAndShort(t *testing.T) {
	f := newFixture(t, standard)
	r := f.complete("issues", "ls", "-L", "")
	want(t, "labels", list(r), "area::auth auth bug docs feature kind:bug")
	for _, c := range r.Cands {
		if c.Value == "bug" && c.Desc != "short for kind:bug" {
			t.Errorf("short label description: %q", c.Desc)
		}
		if c.Value == "kind:bug" && c.Desc != "3 issues" {
			t.Errorf("stored label description: %q", c.Desc)
		}
	}
	want(t, "prefix", list(f.complete("issues", "ls", "--label=k")), "--label=kind:bug")
	want(t, "label --add", list(f.complete("issues", "label", "erp#2", "--add", "d")), "docs")
}

func TestMilestonesAndQuoting(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "-m erp", list(f.complete("issues", "ls", "-R", "erp", "-m", "")), "old v1 v1 beta")
	want(t, "-m all", list(f.complete("issues", "ls", "-m", "")), "launch old v1 v1 beta")
	want(t, "-m prefix fold", list(f.complete("issues", "ls", "-m", "V1")), "v1 v1 beta")
	want(t, "milestone positional", list(f.complete("issues", "milestone", "erp#2", "")), "none launch old v1 v1 beta")
	want(t, "milestones show", list(f.complete("issues", "milestones", "show", "v")), "v1 v1 beta")
	for _, c := range f.complete("issues", "ls", "-m", "v1").Cands {
		if c.Value == "v1" && !strings.HasPrefix(c.Desc, "open, 3/7") {
			t.Errorf("milestone description %q", c.Desc)
		}
	}
}

func TestRefs(t *testing.T) {
	f := newFixture(t, standard)
	r := f.complete("issues", "show", "")
	want(t, "show newest first", list(r), "erp#510 erp#51 erp#50 erp#2 erp#3 web#3 erp#52 erp#1")
	if r.Cands[0].Desc != "search: add filters" {
		t.Errorf("description is the title: %q", r.Cands[0].Desc)
	}
	want(t, "erp#5 prefix", list(f.complete("issues", "show", "erp#5")), "erp#510 erp#51 erp#50 erp#52")
	want(t, "erp#51", list(f.complete("issues", "show", "erp#51")), "erp#510 erp#51")
	want(t, "alias prefix", list(f.complete("issues", "show", "we")), "web#3")
	want(t, "no match", list(f.complete("issues", "show", "zzz")), "")
	want(t, "second positional is not a ref", list(f.complete("issues", "show", "erp#1", "")), "")
	for _, cmd := range []string{"comment", "label", "edit", "milestone"} {
		if got := list(f.complete("issues", cmd, "erp#5")); got != "erp#510 erp#51 erp#50 erp#52" {
			t.Errorf("%s refs: %s", cmd, got)
		}
	}
}

func TestClaimedAndUnclaimedFilters(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "claim: open and unclaimed", list(f.complete("issues", "claim", "")), "erp#51 erp#50 erp#3 web#3")
	want(t, "release: no claim is the unknown agent's", list(f.complete("issues", "release", "")), "")
	want(t, "reopen: closed", list(f.complete("issues", "reopen", "")), "erp#52 erp#1")
	want(t, "close: open", list(f.complete("issues", "close", "")), "erp#510 erp#51 erp#50 erp#2 erp#3 web#3")
	want(t, "claim erp#5", list(f.complete("issues", "claim", "erp#5")), "erp#51 erp#50")
}

func TestPRRefs(t *testing.T) {
	f := newFixture(t, standard)
	f.write("erp/prs.tsv", "7\topen\t0\thead\toid\tmain\t2026-02-01T00:00:00Z\t\t\talice\t2026-02-02T00:00:00Z\t\tadd widget\n")
	r := f.complete("issues", "pr", "show", "")
	want(t, "pr show", list(r), "erp#7")
	if r.Cands[0].Desc != "add widget" {
		t.Errorf("desc %q", r.Cands[0].Desc)
	}
}

func TestConfigCompletesFiles(t *testing.T) {
	f := newFixture(t, standard)
	for _, w := range [][]string{{"issues", "--config", ""}, {"issues", "ls", "--config", ""}, {"issues", "new", "--body-file", ""}, {"tui", "--dir", ""}} {
		if r := f.complete(w...); r.Dir != DirDefault || len(r.Cands) != 0 {
			t.Errorf("%v: want file completion, got %d cands dir %d", w, len(r.Cands), r.Dir)
		}
	}
	want(t, "tui flags", list(f.complete("tui", "-")), "--config --dir --agent")
}

func TestNoConfigIsSilent(t *testing.T) {
	d := t.TempDir()
	o := Options{Top: []Command{{"issues", ""}}, Open: func(cfg, dir string) Source {
		return OpenSession(cfg, dir, issuesEnv(d))
	}}
	var out strings.Builder
	for _, w := range [][]string{{"issues", "show", ""}, {"issues", "ls", "-L", ""}, {"issues", "ls", "-m", ""}, {"issues", "ls", "-R", ""}} {
		out.Reset()
		RunComplete(o, w, &out)
		if out.String() != ":4\n" {
			t.Errorf("%v: want nothing but the directive, got %q", w, out.String())
		}
	}
	// static completion still works without a config
	if r := Complete(o, []string{"issues", "ls", "--so"}); list(r) != "--sort" {
		t.Errorf("flags need no config: %s", list(r))
	}
}

func TestRunCompleteFormat(t *testing.T) {
	f := newFixture(t, standard)
	var b strings.Builder
	RunComplete(f.opts(), []string{"issues", "claim", "erp#5"}, &b)
	want(t, "wire format", b.String(), "erp#51\twidget: renders blank\nerp#50\tdocs: explain config lookup\n:4\n")
}

func TestWorkStatusFlagsAndSweep(t *testing.T) {
	f := newFixture(t, standard)
	if g := list(f.complete("issues", "sw")); g != "sweep" {
		t.Errorf("sweep: %q", g)
	}
	want(t, "sweep --release", list(f.complete("issues", "sweep", "--r")), "--release --repo")
	for _, cmd := range []string{"ls", "frontier", "milestones show"} {
		words := append([]string{"issues"}, strings.Fields(cmd)...)
		got := " " + list(f.complete(append(words, "-")...)) + " "
		for _, w := range []string{"-w", "--working", "-i", "--idle", "--status", "--show-status", "-B", "--labels", "-n", "--no-color", "--color"} {
			if !strings.Contains(got, " "+w+" ") {
				t.Errorf("%s - lacks %s: %s", cmd, w, got)
			}
		}
	}
	for _, cmd := range []string{"show", "claims"} {
		if g := list(f.complete("issues", cmd, "--show")); g != "--show-status" {
			t.Errorf("%s --show: %q", cmd, g)
		}
	}
	want(t, "-B", list(f.complete("issues", "ls", "-B", "")), "full short none")
	want(t, "--labels=", list(f.complete("issues", "ls", "--labels=s")), "--labels=short")
	want(t, "--color", list(f.complete("issues", "ls", "--color", "")), "auto always never")
	want(t, "frontier -s", list(f.complete("issues", "frontier", "-s", "")), "number updated created title status")
	want(t, "prs has no status sort", list(f.complete("issues", "prs", "-s", "")), "number updated created title")
	want(t, "--status", list(f.complete("issues", "ls", "--status", "")), "stale working in-review blocked ready open done")
	want(t, "--status list", list(f.complete("issues", "ls", "--status", "working,s")), "working,stale")
	want(t, "--status=", list(f.complete("issues", "ls", "--status=in")), "--status=in-review")
	want(t, "-w is a bool", list(f.complete("issues", "ls", "-w", "-s", "st")), "status")
	if g := list(f.complete("issues", "ls", "-w", "-")); strings.Contains(g, " -w") || strings.HasPrefix(g, "-w ") {
		t.Errorf("-w offered twice: %s", g)
	}
}

// -L accepts the stored name or the shown short name; completion offers both, and both find the same issues.
func TestShortLabelCompletesAndFindsTheSameIssues(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "complete bug", list(f.complete("issues", "ls", "-L", "bu")), "bug")
	want(t, "complete kind:bug", list(f.complete("issues", "ls", "-L", "kind:b")), "kind:bug")
	want(t, "area short", list(f.complete("issues", "ls", "-L", "au")), "auth")
	runLs := func(label string) string {
		var out, errb strings.Builder
		code := issues.Main([]string{"--config", f.cfg, "ls", "-L", label, "-R", "erp", "-S", "all"}, issues.Env{
			Getenv: func(k string) string {
				if k == "ISSUES_NO_AUTOSYNC" {
					return "1"
				}
				return ""
			}, Out: &out, Err: &errb, Cwd: f.dir})
		if code != 0 {
			t.Fatalf("ls -L %s: %d %s", label, code, errb.String())
		}
		return out.String()
	}
	short, stored := runLs("bug"), runLs("kind:bug")
	if short == "" || short != stored || strings.Count(short, "\n") != 3 {
		t.Errorf("-L bug and -L kind:bug must list the same three issues:\n%q\n%q", short, stored)
	}
	if a, b := runLs("auth"), runLs("area::auth"); a == "" || a != b {
		t.Errorf("-L auth vs -L area::auth:\n%q\n%q", a, b)
	}
}

func TestOutboxCompletion(t *testing.T) {
	f := newFixture(t, standard)
	want(t, "outbox subcommands", list(f.complete("issues", "outbox", "")), "ls edit drop")
	want(t, "outbox edit flags", list(f.complete("issues", "outbox", "edit", "abcd", "--cas")), "")
	want(t, "outbox drop flag", list(f.complete("issues", "outbox", "drop", "abcd", "--cas")), "--cascade")
	want(t, "outbox ls format", list(f.complete("issues", "outbox", "ls", "--format", "j")), "json")
}

// moon#33: claim is offered on an open issue that is unclaimed, stale, or held by this same agent (renew),
// and not on one held live by another agent.
func TestClaimOffersOwnClaimsNotOthers(t *testing.T) {
	f := newFixture(t, standard) // erp#2 is held by alice, erp#510 by bob, both live
	opts := f.opts()
	opts.Open = func(cfg, dir string) Source {
		return OpenSession(f.cfg, dir, issues.Env{Getenv: func(k string) string {
			if k == "USER" {
				return "alice"
			}
			return ""
		}, Cwd: f.dir})
	}
	got := list(Complete(opts, []string{"issues", "claim", ""}))
	want(t, "claim as alice", got, "erp#51 erp#50 erp#2 erp#3 web#3")
	if strings.Contains(got, "erp#510") {
		t.Errorf("offered an issue held live by bob: %s", got)
	}
}
