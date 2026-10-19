package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var wNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func wCtx(alive func(int) bool) WorkCtx { return WorkCtx{Now: wNow, Host: "box", Alive: alive} }

func claimAgo(agent string, agoSec int, ttl float64, pid int, host string) *Claim {
	return &Claim{agent, pid, host, float64(wNow.Unix() - int64(agoSec)), ttl}
}

func TestWorkStatusTable(t *testing.T) {
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }
	issue := func(state string, labels ...string) WorkIssue {
		return WorkIssue{Alias: "app", Repo: "example/app", Num: 7, State: state, Labels: labels}
	}
	prFor := func(n int, text string) WorkPR { return WorkPR{Number: n, Text: text} }
	for _, c := range []struct {
		name  string
		is    WorkIssue
		claim *Claim
		prs   []WorkPR
		alive func(int) bool
		want  Status
		label string
	}{
		{"plain open", issue("open"), nil, nil, alive, Status{Name: StOpen}, "open"},
		{"ready label", issue("open", "triage:ready"), nil, nil, alive, Status{Name: StReady}, "ready"},
		{"bare ready label", issue("open", "ready"), nil, nil, alive, Status{Name: StReady}, "ready"},
		{"not-ready is not ready", issue("open", "triage:not-ready"), nil, nil, alive, Status{Name: StOpen}, "open"},
		{"blocked label", issue("open", "blocked"), nil, nil, alive, Status{Name: StBlocked}, "blocked"},
		{"needs-decision", issue("open", "triage:needs-decision"), nil, nil, alive, Status{Name: StBlocked}, "blocked"},
		{"needs-information", issue("open", "triage:needs-information"), nil, nil, alive, Status{Name: StBlocked}, "blocked"},
		{"blocked beats ready", issue("open", "triage:ready", "blocked"), nil, nil, alive, Status{Name: StBlocked}, "blocked"},
		{"fresh claim is working", issue("open"), claimAgo("sonnet-17", 660, 7200, 0, "box"), nil, alive,
			Status{Name: StWorking, Agent: "sonnet-17", AgeSec: 660}, "working: sonnet-17 11m"},
		{"working beats blocked", issue("open", "blocked"), claimAgo("a", 60, 7200, 0, "box"), nil, alive, Status{Name: StWorking, Agent: "a", AgeSec: 60}, "working: a 1m"},
		{"past ttl is stale", issue("open"), claimAgo("haiku", 9660, 7200, 0, "box"), nil, alive,
			Status{Name: StStale, Agent: "haiku", AgeSec: 9660}, "stale: haiku 2h41m"},
		{"exactly ttl is still working", issue("open"), claimAgo("a", 7200, 7200, 0, "box"), nil, alive, Status{Name: StWorking, Agent: "a", AgeSec: 7200}, "working: a 2h00m"},
		{"live pid stays working", issue("open"), claimAgo("a", 60, 7200, 4242, "box"), nil, alive, Status{Name: StWorking, Agent: "a", AgeSec: 60}, "working: a 1m"},
		{"dead pid downgrades to stale", issue("open"), claimAgo("a", 60, 7200, 4242, "box"), nil, dead, Status{Name: StStale, Agent: "a", AgeSec: 60}, "stale: a 1m"},
		{"pid of another host is not checked", issue("open"), claimAgo("a", 60, 7200, 4242, "elsewhere"), nil, dead, Status{Name: StWorking, Agent: "a", AgeSec: 60}, "working: a 1m"},
		{"pid 0 is not checked", issue("open"), claimAgo("a", 60, 7200, 0, "box"), nil, dead, Status{Name: StWorking, Agent: "a", AgeSec: 60}, "working: a 1m"},
		{"stale beats blocked", issue("open", "blocked"), claimAgo("a", 9000, 60, 0, "box"), nil, alive, Status{Name: StStale, Agent: "a", AgeSec: 9000}, "stale: a 2h30m"},
		{"open PR is in review", issue("open"), nil, []WorkPR{prFor(588, "Closes #7")}, alive, Status{Name: StReview, PR: 588}, "in review: PR #588"},
		{"in review beats working", issue("open"), claimAgo("a", 60, 7200, 0, "box"), []WorkPR{prFor(5, "fixes #7")}, alive, Status{Name: StReview, PR: 5}, "in review: PR #5"},
		{"in review beats stale", issue("open"), claimAgo("a", 99999, 60, 0, "box"), []WorkPR{prFor(5, "resolves #7")}, alive, Status{Name: StReview, PR: 5}, "in review: PR #5"},
		{"newest PR wins", issue("open"), nil, []WorkPR{prFor(5, "Closes #7"), prFor(9, "Closes #7")}, alive, Status{Name: StReview, PR: 9}, "in review: PR #9"},
		{"github closing refs count", issue("open"), nil, []WorkPR{{Number: 3, Closes: []int{7}}}, alive, Status{Name: StReview, PR: 3}, "in review: PR #3"},
		{"unrelated PR does not count", issue("open"), nil, []WorkPR{prFor(3, "Closes #8")}, alive, Status{Name: StOpen}, "open"},
		{"closed is done", issue("closed"), nil, nil, alive, Status{Name: StDone}, "done"},
		{"done beats review", issue("closed"), nil, []WorkPR{prFor(5, "Closes #7")}, alive, Status{Name: StDone}, "done"},
		{"closed with lingering claim", issue("closed"), claimAgo("a", 3600, 7200, 0, "box"), nil, alive, Status{Name: StDone, StaleClaim: true}, "done (stale claim)"},
		{"done beats everything", issue("closed", "blocked", "triage:ready"), claimAgo("a", 60, 7200, 0, "box"), []WorkPR{prFor(5, "Closes #7")}, alive,
			Status{Name: StDone, StaleClaim: true}, "done (stale claim)"},
		{"local issue has no PRs", WorkIssue{Alias: "app", State: "open"}, nil, []WorkPR{prFor(1, "Closes #0")}, alive, Status{Name: StOpen}, "open"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := WorkStatus(c.is, c.claim, c.prs, wCtx(c.alive))
			if !reflect.DeepEqual(got, c.want) || got.Label() != c.label {
				t.Fatalf("got %+v (%q) want %+v (%q)", got, got.Label(), c.want, c.label)
			}
		})
	}
}

func TestRuleTableOrderIsData(t *testing.T) {
	var names []string
	for _, r := range WorkRules {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "done,in review,working,stale,blocked,ready,open" {
		t.Fatalf("priority order: %s", got)
	}
	// reorder the table: working above in review, and the answer follows
	saved := append([]Rule{}, WorkRules...)
	defer func() { WorkRules = saved }()
	WorkRules = []Rule{saved[0], saved[2], saved[1], saved[3], saved[4], saved[5], saved[6]}
	is := WorkIssue{Alias: "app", Repo: "example/app", Num: 7, State: "open"}
	got := WorkStatus(is, claimAgo("a", 60, 7200, 0, "box"), []WorkPR{{Number: 5, Text: "Closes #7"}}, wCtx(nil))
	if got.Name != StWorking {
		t.Fatalf("reordered table should make working win, got %+v", got)
	}
}

func TestClosingRefs(t *testing.T) {
	for _, c := range []struct {
		text string
		want []int
	}{
		{"Closes #12", []int{12}},
		{"closes #12", []int{12}},
		{"CLOSED #12", []int{12}},
		{"Fixes #12", []int{12}},
		{"fixed #12 and more", []int{12}},
		{"Resolves #12", []int{12}},
		{"resolved: #12", []int{12}},
		{"Closes app#12", []int{12}},
		{"closes APP#12", []int{12}},
		{"Fixes example/app#12", []int{12}},
		{"Fixes https://github.com/example/app/issues/12", []int{12}},
		{"Closes lib#12", nil},
		{"Fixes other/repo#12", nil},
		{"Fixes https://github.com/other/repo/issues/12", nil},
		{"see #12", nil},
		{"related to #12", nil},
		{"unfixed #12", nil},
		{"Closes #123", []int{123}},
		{"Closes #1\n\nFixes lib#2\nResolves #3", []int{1, 3}},
		{"", nil},
	} {
		if got := ClosingRefs(c.text, "app", "example/app"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.text, got, c.want)
		}
	}
}

// workFixture writes a mirror of invented data with one issue in every status. Time is 2026-03-01T12:00:00Z.
func workFixture(e *env) {
	e.putFile("app", fileIndex, strings.Join([]string{
		"28\topen\t\t\t2026-02-28T00:00:00Z\tinvalid: other repo reference",
		"27\topen\t\t\t2026-02-27T00:00:00Z\tbranch: only the branch name matches",
		"26\topen\t\t\t2026-02-26T00:00:00Z\tplain: nobody on it",
		"25\topen\t\ttriage:needs-decision\t2026-02-25T00:00:00Z\tblocked: waiting on a call",
		"24\tclosed\t\tkind:bug\t2026-02-24T00:00:00Z\tdone: closed with a lingering claim",
		"23\topen\t\tkind:feature\t2026-02-23T00:00:00Z\treview: a PR is open",
		"22\topen\t\tkind:bug\t2026-02-22T00:00:00Z\tstale: the agent went away",
		"21\topen\t\tkind:bug,area:ui\t2026-02-21T00:00:00Z\tworking: an agent is on it",
		"20\topen\t\tkind:bug,triage:ready\t2026-02-20T00:00:00Z\tready: triaged and waiting",
		"",
	}, "\n"))
	e.putFile("app", fileLastpull, "1772366400\n")
	e.putFile("app", filePRs, strings.Join([]string{
		"52\topen\t0\tdocs-52\ta\tmain\t2026-02-28T00:00:00Z\t\t\tlee\t2026-02-28T00:00:00Z\t\tdocs: other repo",
		"51\topen\t0\tfix-27\tb\tmain\t2026-02-27T00:00:00Z\t\t\tlee\t2026-02-27T00:00:00Z\t\tbranch fix-27 touches things",
		"50\topen\t0\tfeature-23\tc\tmain\t2026-02-23T00:00:00Z\t\t\tdana\t2026-02-23T00:00:00Z\t\treview: the feature",
		"49\tmerged\t0\told\td\tmain\t2026-02-01T00:00:00Z\t2026-02-02T00:00:00Z\tf\tdana\t2026-02-02T00:00:00Z\t26\tmerged long ago",
		"",
	}, "\n"))
	e.putPR("app", 50, "---\nnumber: 50\n---\n\nImplements it. Closes #23\n")
	e.putPR("app", 52, "---\nnumber: 52\n---\n\nFixes lib#28 and mentions app#28 only in passing\n")
	e.putPR("app", 51, "---\nnumber: 51\n---\n\nTouches #27 but does not close it\n")
	e.putClaim("app", "21", `{"agent": "sonnet-17", "pid": 0, "host": "box", "ts": 1772365740, "ttl": 7200}`+"\n") // 11m ago
	e.putClaim("app", "22", fmt.Sprintf(`{"agent": "haiku", "pid": 0, "host": "box", "ts": %d, "ttl": 7200}`+"\n", 1772366400-9660))
	e.putClaim("app", "24", `{"agent": "opus", "pid": 0, "host": "box", "ts": 1772362800, "ttl": 7200}`+"\n") // 1h ago, issue closed
}

func TestStatusEverywhereOnFixture(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	out, errs, code := e.run("ls", "--repo", "app", "--state", "all", "--show-status")
	if code != 0 || errs != "" {
		t.Fatal(code, errs)
	}
	want := map[string]string{
		"app#28": "open", "app#27": "open", "app#26": "open", "app#25": "blocked", "app#24": "done (stale claim)", "app#23": "in review: PR #50",
		"app#22": "stale: haiku 2h41m", "app#21": "working: sonnet-17 11m", "app#20": "ready",
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(l, "\t")
		if got := f[len(f)-1]; got != want[f[0]] {
			t.Errorf("%s: status %q want %q", f[0], got, want[f[0]])
		}
	}
	// piped output without a status flag is byte-identical to before: five columns
	plain, _, _ := e.run("ls", "--repo", "app", "--state", "all")
	for _, l := range strings.Split(strings.TrimSpace(plain), "\n") {
		if n := len(strings.Split(l, "\t")); n != 5 {
			t.Errorf("plain tsv row has %d columns: %q", n, l)
		}
	}
	// the flags that ask for it add the column
	for _, args := range [][]string{{"-w"}, {"-i"}, {"--status", "done"}, {"-s", "status"}} {
		o, _, _ := e.run(append([]string{"ls", "--repo", "app", "-S", "all"}, args...)...)
		if l := strings.Split(strings.TrimSpace(strings.Split(o, "\n")[0]), "\t"); len(l) != 6 {
			t.Errorf("%v: want 6 columns, got %q", args, l)
		}
	}
	// show: piped output unchanged unless asked; pretty and --show-status print status right after claim
	raw, _, _ := e.run("show", "app#21")
	if !strings.HasPrefix(raw, "claim: sonnet-17 (pid -") || strings.Contains(raw, "status:") {
		t.Errorf("piped show changed: %q", raw)
	}
	raw, _, _ = e.run("show", "app#24", "--show-status")
	if !strings.Contains(raw, "\nstatus: done (stale claim)\n") || !strings.HasPrefix(raw, "claim: opus") {
		t.Errorf("show --show-status: %q", raw)
	}
}

func TestStatusFiltersAndSort(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	refs := func(args ...string) string {
		out, errs, code := e.run(append([]string{"ls", "--repo", "app", "-S", "all"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errs)
		}
		var r []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				r = append(r, strings.SplitN(l, "\t", 2)[0])
			}
		}
		return strings.Join(r, " ")
	}
	eq(t, "-w", refs("-w"), "app#21")
	eq(t, "--working", refs("--working"), "app#21")
	eq(t, "-i", refs("-i"), "app#28 app#27 app#26 app#20")
	eq(t, "--status blocked,done", refs("--status", "blocked,done"), "app#25 app#24")
	eq(t, "--status in-review", refs("--status", "in-review"), "app#23")
	eq(t, "--status review", refs("--status", "review"), "app#23")
	eq(t, "-w -i union", refs("-w", "-i"), "app#28 app#27 app#26 app#21 app#20")
	eq(t, "-s status", refs("-s", "status"), "app#22 app#21 app#23 app#25 app#20 app#28 app#27 app#26 app#24")
	eq(t, "-s status -r", refs("-s", "status", "-r"), "app#24 app#28 app#27 app#26 app#20 app#25 app#23 app#21 app#22")
	_, errs, code := e.run("ls", "--status", "bogus")
	if code != 2 || !strings.Contains(errs, "invalid status 'bogus'") {
		t.Errorf("bad status: %d %q", code, errs)
	}
	// frontier keeps its meaning: open issues with no fresh claim
	out, _, _ := e.run("frontier", "--repo", "app")
	if strings.Contains(out, "app#21\t") || !strings.Contains(out, "app#22\t") {
		t.Errorf("frontier:\n%s", out)
	}
}

func TestClaimsShowStatusWords(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	out, _, _ := e.run("claims", "--show-status")
	for _, want := range []string{"app#21\tfresh\tsonnet-17\tbox\tpid=-\tage=660s\tttl=7200s\tworking: sonnet-17 11m\n",
		"app#22\tSTALE\thaiku", "\tstale: haiku 2h41m\n", "app#24\tfresh\topus\tbox\tpid=-\tage=3600s\tttl=7200s\tdone (stale claim)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("claims lacks %q:\n%s", want, out)
		}
	}
	plain, _, _ := e.run("claims")
	if strings.Contains(plain, "done") || strings.Contains(plain, "working") {
		t.Errorf("plain claims tsv must not change:\n%s", plain)
	}
	e.tty, e.width, e.extra["NO_COLOR"] = true, 80, "1"
	tbl, _, _ := e.run("claims")
	for _, want := range []string{"working", "stale ", "done (stale claim)"} {
		if !strings.Contains(tbl, want) {
			t.Errorf("claims table lacks %q:\n%s", want, tbl)
		}
	}
	js, _, _ := e.run("claims", "--format", "json")
	if !strings.Contains(js, `"status": "done", "issue_closed": true`) {
		t.Errorf("claims json: %s", js)
	}
}

func TestSweep(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	e.putClaim("app", "23", `{"agent": "x", "pid": 0, "host": "box", "ts": 1772366000, "ttl": 7200}`+"\n") // fresh, issue in review
	before := e.hasClaim("app", "21") && e.hasClaim("app", "22") && e.hasClaim("app", "24")
	out, _, code := e.run("sweep")
	if code != 0 || !before {
		t.Fatal(code)
	}
	for _, want := range []string{"would release\tapp#22\tstale\thaiku\t", "would release\tapp#24\tissue closed\topus\t"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "app#21") || strings.Contains(out, "app#23") {
		t.Errorf("a fresh claim on an open issue must never be listed:\n%s", out)
	}
	if !e.hasClaim("app", "22") || !e.hasClaim("app", "24") {
		t.Fatal("dry run removed a claim")
	}
	out, _, _ = e.run("sweep", "--release")
	if !strings.Contains(out, "released\tapp#22") || !strings.Contains(out, "released\tapp#24") {
		t.Errorf("release output:\n%s", out)
	}
	if e.hasClaim("app", "22") || e.hasClaim("app", "24") {
		t.Error("claims not removed")
	}
	if !e.hasClaim("app", "21") || !e.hasClaim("app", "23") {
		t.Error("a fresh claim on an open issue was released")
	}
	if out, _, _ = e.run("sweep"); strings.TrimSpace(out) != "" {
		t.Errorf("second sweep should find nothing: %q", out)
	}
	e.tty, e.width, e.extra["NO_COLOR"] = true, 80, "1"
	if out, _, _ = e.run("sweep"); !strings.Contains(out, "nothing to sweep") {
		t.Errorf("table: %q", out)
	}
}

func TestSweepDeadPid(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	host, _ := os.Hostname()
	// a fresh claim whose process is gone (pid 2^22+ is never alive) on this host is stale and swept
	e.putClaim("app", "26", fmt.Sprintf(`{"agent": "ghost", "pid": 4194000, "host": %q, "ts": 1772366300, "ttl": 7200}`+"\n", host))
	e.extra["ISSUES_TEST_HOST"] = host
	a := e.app()
	a.Host = host
	items := a.sweepable()
	found := false
	for _, it := range items {
		found = found || it.ref == "app#26" && it.reason == "stale"
	}
	if !found {
		t.Fatalf("dead pid claim not swept: %+v", items)
	}
}

func TestLabelMatching(t *testing.T) {
	universe := []string{"area::auth", "area:ui", "kind:bug", "kind::feature", "triage:ready", "wontfix"}
	for _, c := range []struct {
		want string
		hit  []string
	}{
		{"kind:bug", []string{"kind:bug"}}, // stored name
		{"bug", []string{"kind:bug"}},      // shown short name
		{"ready", []string{"triage:ready"}},
		{"ui", []string{"area:ui"}},
		{"auth", []string{"area::auth"}},
		{"feature", []string{"kind::feature"}},
		{"kind:feature", []string{"kind::feature"}},
		{"BUG", []string{"kind:bug"}},
		{"wontfix", []string{"wontfix"}},
		{"nothing", nil},
		{"ug", nil}, // only whole segments after a colon, never a bare substring
	} {
		if got := matchLabel(c.want, universe); !reflect.DeepEqual(got, c.hit) {
			t.Errorf("%q: got %v want %v", c.want, got, c.hit)
		}
	}
	if got := matchLabel("ready", []string{"triage:ready", "status:ready"}); len(got) != 2 {
		t.Errorf("ambiguous: %v", got)
	}
	if got := matchLabel("bug", []string{"bug", "kind:bug"}); !reflect.DeepEqual(got, []string{"bug"}) {
		t.Errorf("an exact stored name wins: %v", got)
	}
}

func TestLabelFlagAcceptsShownNames(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	for _, l := range []string{"kind:bug", "bug"} {
		out, _, code := e.run("ls", "--repo", "app", "-S", "all", "-L", l)
		if code != 0 || strings.Join(refsOf(out), " ") != "app#12 app#5" {
			t.Errorf("-L %s: %d %q", l, code, out)
		}
	}
	if out, _, _ := e.run("ls", "--repo", "app", "-L", "ui"); strings.Join(refsOf(out), " ") != "app#12" {
		t.Errorf("-L ui (area:ui): %q", out)
	}
	if out, _, code := e.run("ls", "--repo", "app", "-L", "nope"); code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("no match is an empty list: %d %q", code, out)
	}
	// two labels end in "ready": the short name is ambiguous
	e.putFile("app", fileIndex, "3\topen\t\ttriage:ready\t2026-02-12T09:00:00Z\tone\n2\topen\t\tstatus:ready\t2026-02-12T09:00:00Z\ttwo\n")
	out, errs, code := e.run("ls", "--repo", "app", "-L", "ready")
	if code != 2 || out != "" || !strings.Contains(errs, "ambiguous") || !strings.Contains(errs, "status:ready, triage:ready") {
		t.Errorf("ambiguous: %d %q %q", code, out, errs)
	}
	if out, _, code = e.run("ls", "--repo", "app", "-L", "triage:ready"); code != 0 || strings.Join(refsOf(out), " ") != "app#3" {
		t.Errorf("stored name resolves the ambiguity: %d %q", code, out)
	}
	if _, errs, code = e.run("frontier", "--repo", "app", "-L", "ready"); code != 2 || !strings.Contains(errs, "ambiguous") {
		t.Errorf("frontier ambiguous: %d %q", code, errs)
	}
}

func TestGhMissingMessage(t *testing.T) {
	d := t.TempDir()
	for name, bin := range map[string]string{"missing": filepath.Join(d, "nope"), "not executable": func() string {
		p := filepath.Join(d, "gh")
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644)
		return p
	}()} {
		_, err := ExecGH{Bin: bin}.Run([]string{"version"}, "", time.Second)
		if err == nil || err.Error() != "could not run gh (set ISSUES_GH or install the GitHub CLI)" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a gh that runs and fails keeps its own message
	p := filepath.Join(d, "bad")
	os.WriteFile(p, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755)
	if _, err := (ExecGH{Bin: p}).Run(nil, "", time.Second); err == nil || err.Error() != "boom" {
		t.Errorf("failing gh: %v", err)
	}
}
