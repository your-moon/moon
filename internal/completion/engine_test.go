package completion

import (
	"strings"
	"testing"
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
	want(t, "--s", list(r), "--state --sort")
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
	want(t, "-s", list(f.complete("issues", "ls", "-s", "")), "number updated created title")
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
	want(t, "ls -s", list(f.complete("issues", "ls", "-s", "")), "number updated created title")
	want(t, "ls -R erp -L", list(f.complete("issues", "ls", "-R", "erp", "-L", "")), "area::auth bug docs feature kind:bug")
	want(t, "ls -R web -L", list(f.complete("issues", "ls", "-R", "web", "-L", "")), "docs")
	want(t, "ls -R", list(f.complete("issues", "ls", "-R", "")), "erp web")
	want(t, "--repo=", list(f.complete("issues", "ls", "--repo=w")), "--repo=web")
	// the config can come from the line
	want(t, "--config before cmd", list(f.complete("issues", "--config", f.cfg, "ls", "-R", "")), "erp web")
	want(t, "--config after cmd", list(f.complete("issues", "ls", "--config="+f.cfg, "-R", "")), "erp web")
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
	want(t, "-rs value", list(f.complete("issues", "ls", "-rs")), "-rsnumber -rsupdated -rscreated -rstitle")
	want(t, "-rs then word", list(f.complete("issues", "ls", "-rs", "")), "number updated created title")
	want(t, "-rl5 inline value", list(f.complete("issues", "ls", "-rl5")), "")
	want(t, "-rR then word", list(f.complete("issues", "ls", "-rR", "")), "erp web")
	want(t, "-x unknown", list(f.complete("issues", "ls", "-x")), "")
}

func TestLabelsStoredAndShort(t *testing.T) {
	f := newFixture(t, standard)
	r := f.complete("issues", "ls", "-L", "")
	want(t, "labels", list(r), "area::auth bug docs feature kind:bug")
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
	want(t, "release: claimed", list(f.complete("issues", "release", "")), "erp#510 erp#2")
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
