package issues

import (
	"strings"
	"testing"
)

const appRemote = "git@github.com:example/app.git" // newEnv declares app = example/app, lib = example/lib

// scopedEnv is the fixture with a working directory whose git remote is app.
func scopedEnv(t *testing.T) *env {
	e := newEnv(t)
	humanFixture(e)
	e.git = fakeGit(appRemote)
	return e
}

func TestScopeDefaultsPerCommand(t *testing.T) {
	e := scopedEnv(t)
	only := func(name, out, repo, other string) {
		t.Helper()
		if !strings.Contains(out, repo+"#") && !strings.Contains(out, repo+"\t") && !strings.Contains(out, repo+":") {
			t.Errorf("%s: no %s rows in %q", name, repo, out)
		}
		if strings.Contains(out, other+"#") || strings.Contains(out, other+"\t") || strings.Contains(out, other+":") {
			t.Errorf("%s: %s rows leaked into the %s scope: %q", name, other, repo, out)
		}
	}
	for name, args := range map[string][]string{"ls": {"ls", "-S", "all"}, "frontier": {"frontier"}, "milestones": {"milestones"}, "status": {"status"}} {
		out, errs, code := e.run(args...)
		if code != 0 || errs != "" {
			t.Fatalf("%s: %d %q", name, code, errs)
		}
		only(name, out, "app", "lib")
	}
	// the same commands, widened and re-aimed
	out, _, _ := e.run("ls", "-R", "all")
	if !strings.Contains(out, "app#12") || !strings.Contains(out, "lib#3") {
		t.Errorf("-R all: %q", out)
	}
	out, _, _ = e.run("ls", "--all")
	if !strings.Contains(out, "app#12") || !strings.Contains(out, "lib#3") {
		t.Errorf("--all: %q", out)
	}
	out, _, _ = e.run("ls", "-R", "lib")
	only("ls -R lib", out, "lib", "app")
	out, _, _ = e.run("ls", "-R", "lib", "-R", "app")
	if !strings.Contains(out, "app#12") || !strings.Contains(out, "lib#3") {
		t.Errorf("-R twice: %q", out)
	}
	// prs only exist for app: out of a lib checkout there are none
	if out, _, _ = e.run("prs", "-S", "all"); !strings.Contains(out, "app#31") {
		t.Errorf("prs in app: %q", out)
	}
	if out, _, _ = e.run("prs", "-S", "all", "-R", "lib"); strings.TrimSpace(out) != "" {
		t.Errorf("prs in lib: %q", out)
	}
	// claims
	if out, _, _ = e.run("claims"); !strings.Contains(out, "app#12") {
		t.Errorf("claims in app: %q", out)
	}
	if out, _, _ = e.run("claims", "-R", "lib"); strings.TrimSpace(out) != "" {
		t.Errorf("claims in lib: %q", out)
	}
	if out, _, _ = e.run("claims", "--all"); !strings.Contains(out, "app#12") {
		t.Errorf("claims --all: %q", out)
	}
	// status counts only the scoped repos
	if out, _, _ = e.run("status", "-R", "lib"); strings.Contains(out, "app:") || !strings.Contains(out, "lib:") {
		t.Errorf("status -R lib: %q", out)
	}
	// without a project the old global behaviour is untouched
	e.git = nil
	out, _, _ = e.run("ls")
	if !strings.Contains(out, "app#12") || !strings.Contains(out, "lib#3") {
		t.Errorf("global ls: %q", out)
	}
}

func TestScopePullFollowsScope(t *testing.T) {
	e := scopedEnv(t) // the fake gh has no handler: each pull fails and names its repo on stderr
	_, errs, _ := e.run("pull")
	if !strings.Contains(errs, "pull app failed") || strings.Contains(errs, "pull lib failed") {
		t.Errorf("scoped pull: %q", errs)
	}
	_, errs, _ = e.run("pull", "--all")
	if !strings.Contains(errs, "pull app failed") || !strings.Contains(errs, "pull lib failed") {
		t.Errorf("pull --all: %q", errs)
	}
	e.git = nil
	_, errs, _ = e.run("sync")
	if !strings.Contains(errs, "pull app failed") || !strings.Contains(errs, "pull lib failed") {
		t.Errorf("global sync: %q", errs)
	}
	e.git = fakeGit(appRemote)
	_, errs, _ = e.run("sync")
	if !strings.Contains(errs, "pull app failed") || strings.Contains(errs, "pull lib failed") {
		t.Errorf("scoped sync: %q", errs)
	}
}

func TestBareNumbers(t *testing.T) {
	e := scopedEnv(t)
	e.write("app/12.md", "---\ntitle: x\nstate: open\n---\nbody\n")
	out, _, code := e.run("show", "12")
	if code != 0 || !strings.Contains(out, "claim: alice") {
		t.Fatalf("show 12 in app scope: %d %q", code, out)
	}
	if out2, _, _ := e.run("show", "#12"); out2 != out {
		t.Errorf("#12 differs from 12")
	}
	if out2, _, _ := e.run("show", "app#12"); !strings.HasPrefix(out2, "claim: alice") {
		t.Errorf("explicit ref: %q", out2)
	}
	// an alias in the ref still wins over the scope
	e.write("lib/3.md", "---\ntitle: p\nstate: open\n---\nbody\n")
	if out3, _, code := e.run("show", "lib#3"); code != 0 || !strings.Contains(out3, "claim: none") {
		t.Errorf("lib#3 from an app checkout: %d %q", code, out3)
	}
	// claim and comment resolve against the scope
	if out4, _, code := e.run("claim", "9", "--agent", "bob"); code != 0 || !strings.Contains(out4, "claimed app#9 for bob") {
		t.Errorf("claim 9: %d %q", code, out4)
	}
	if out5, _, code := e.run("comment", "9", "--body", "hi"); code != 0 || !strings.Contains(out5, "ref app#9") {
		t.Errorf("comment 9: %d %q", code, out5)
	}
	if out6, _, code := e.run("release", "9", "--agent", "bob"); code != 0 || !strings.Contains(out6, "app#9: released") {
		t.Errorf("release 9: %d %q", code, out6)
	}
	if out7, _, code := e.run("close", "-R", "lib", "3"); code != 0 || !strings.Contains(out7, "ref lib#3") {
		t.Errorf("close -R lib 3: %d %q", code, out7)
	}
}

func TestBareNumberInGlobalScope(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	// #3 exists only in lib, #12 only in app: both resolve without an alias
	if out, _, code := e.run("claim", "3", "--agent", "bob"); code != 0 || !strings.Contains(out, "claimed lib#3") {
		t.Errorf("unique number: %d %q", code, out)
	}
	if out, _, code := e.run("claim", "9", "--agent", "bob"); code != 0 || !strings.Contains(out, "claimed app#9") {
		t.Errorf("unique number: %d %q", code, out)
	}
	// #12 in both repos: ambiguous, exit 2, candidates listed, nothing claimed
	e.write("lib/index.tsv", "12\topen\t\t\t2026-02-12T09:00:00Z\tsame number\n3\topen\t\tkind:bug\t2026-02-12T09:00:00Z\tparser: crashes on empty input\n")
	out, errs, code := e.run("claim", "12", "--agent", "carol")
	if code != 2 || out != "" || !strings.Contains(errs, "ambiguous") || !strings.Contains(errs, "app#12 or lib#12") {
		t.Errorf("ambiguous: %d %q %q", code, out, errs)
	}
	if e.exists("claims/lib-12.json") {
		t.Errorf("an ambiguous number must not claim anything")
	}
	// unknown everywhere
	_, errs, code = e.run("show", "999")
	if code != 2 || !strings.Contains(errs, "no repo in scope has 999") || !strings.Contains(errs, "app, lib") {
		t.Errorf("unknown number: %d %q", code, errs)
	}
	// -R narrows the ambiguity
	e.write("lib/12.md", "---\ntitle: x\nstate: open\n---\nbody\n")
	if _, _, code = e.run("show", "-R", "lib", "12"); code != 0 {
		t.Errorf("-R lib 12: %d", code)
	}
	// a local id, not pushed yet, is found through the outbox
	if _, _, code = e.run("new", "-R", "lib", "--title", "t", "--body", "b"); code != 0 {
		t.Fatal("new failed")
	}
	if out, _, code := e.run("comment", "L-1", "--body", "x"); code != 0 || !strings.Contains(out, "ref lib#L-1") {
		t.Errorf("local id: %d %q", code, out)
	}
}

func TestNewNeedsOneRepo(t *testing.T) {
	e := newEnv(t)
	// global scope: refuse and list the aliases
	out, errs, code := e.run("new", "--title", "t", "--body", "b")
	if code != 2 || out != "" || !strings.Contains(errs, "new needs one repo") || !strings.Contains(errs, "app, lib") || !strings.Contains(errs, "not inside a project") {
		t.Errorf("global new: %d %q %q", code, out, errs)
	}
	if e.exists("outbox") {
		t.Errorf("a refused new must queue nothing")
	}
	// several repos named: refuse too
	if _, errs, code = e.run("new", "-R", "app", "-R", "lib", "--title", "t", "--body", "b"); code != 2 || !strings.Contains(errs, "-R named several repos") {
		t.Errorf("two -R: %d %q", code, errs)
	}
	if _, errs, code = e.run("new", "--all", "--title", "t", "--body", "b"); code != 2 {
		t.Errorf("--all: %d %q", code, errs)
	}
	// -R names it
	if out, _, code = e.run("new", "-R", "lib", "--title", "t", "--body", "b"); code != 0 || !strings.Contains(out, "ref lib#L-1") {
		t.Errorf("-R lib: %d %q", code, out)
	}
	// inside a project no --repo is needed
	e.git = fakeGit(appRemote)
	if out, _, code = e.run("new", "--title", "t2", "--body", "b"); code != 0 || !strings.Contains(out, "ref app#L-1") {
		t.Errorf("scoped new: %d %q", code, out)
	}
}

func TestScopeHeader(t *testing.T) {
	e := scopedEnv(t)
	e.extra["NO_COLOR"] = "1"
	_, errs, _ := e.run("ls")
	if strings.Contains(errs, "scope:") {
		t.Errorf("piped output has no header: %q", errs)
	}
	e.tty, e.width = true, 100
	out, errs, _ := e.run("ls")
	if !strings.HasPrefix(errs, "scope: app (git remote origin)\n") {
		t.Errorf("tty header: %q", errs)
	}
	if strings.Contains(out, "scope:") || strings.Contains(out, "REPO") {
		t.Errorf("a single-repo scope needs no REPO column and keeps the header off stdout: %q", out)
	}
	for _, args := range [][]string{{"ls", "--format", "tsv"}, {"ls", "-j"}, {"ls", "-t"}} {
		if _, errs, _ = e.run(args...); strings.Contains(errs, "scope:") {
			t.Errorf("%v: machine format on a tty has no header: %q", args, errs)
		}
	}
	for _, cmd := range []string{"frontier", "prs", "milestones", "claims", "status"} {
		if _, errs, _ = e.run(cmd); !strings.HasPrefix(errs, "scope: app (git remote origin)\n") {
			t.Errorf("%s header: %q", cmd, errs)
		}
	}
	// outside a project: all repos, with a REPO column
	e.git = nil
	out, errs, _ = e.run("ls")
	if !strings.HasPrefix(errs, "scope: all repos (not inside a project)\n") || !strings.Contains(out, "REF     REPO  STATE") {
		t.Errorf("global tty: %q\n%s", errs, out)
	}
	if _, errs, _ = e.run("ls", "-R", "all"); !strings.HasPrefix(errs, "scope: all repos (-R all)\n") {
		t.Errorf("-R all header: %q", errs)
	}
	if _, errs, _ = e.run("ls", "-R", "lib"); !strings.HasPrefix(errs, "scope: lib (-R lib)\n") {
		t.Errorf("-R header: %q", errs)
	}
	// the header is dim on a coloured terminal
	delete(e.extra, "NO_COLOR")
	if _, errs, _ = e.run("ls"); !strings.HasPrefix(errs, "\x1b[90mscope: all repos") {
		t.Errorf("coloured header: %q", errs)
	}
}

func TestPipedGlobalOutputKeepsAllRepos(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	out, _, _ := e.run("ls")
	if strings.Contains(out, "REPO") || !strings.Contains(out, "app#12\topen\tv1.0") || !strings.Contains(out, "lib#3\topen\t-\tkind:bug") {
		t.Errorf("piped global ls changed: %q", out)
	}
}

func TestWhere(t *testing.T) {
	e := scopedEnv(t)
	out, errs, code := e.run("where")
	if code != 0 || errs != "" {
		t.Fatalf("where: %d %q", code, errs)
	}
	for _, want := range []string{"scope:  app\n", "kind:   project\n", "reason: git remote origin\n", "config: " + e.cfg + " (", "rules, first match wins:",
		"1. flag -R/--repo/--all", "no match; not given", "2. project config", "declares 2 repos, needs exactly one", "3. paths in config", "no repo declares a path",
		"=> 4. git remote origin", "matches app; remote origin is example/app", "5. global", "matches app, lib"} {
		if !strings.Contains(out, want) {
			t.Errorf("where lacks %q:\n%s", want, out)
		}
	}
	e.git = nil
	out, _, _ = e.run("where")
	if !strings.Contains(out, "scope:  all repos\n") || !strings.Contains(out, "kind:   global\n") || !strings.Contains(out, "=> 5. global") || !strings.Contains(out, "no remote origin") {
		t.Errorf("global where:\n%s", out)
	}
	out, _, _ = e.run("where", "-R", "lib")
	if !strings.Contains(out, "=> 1. flag") || !strings.Contains(out, "given: lib") || !strings.Contains(out, "reason: -R lib\n") {
		t.Errorf("where -R:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("never print the remote URL")
	}
}

func TestWhereNeverPrintsCredentials(t *testing.T) {
	e := newEnv(t)
	e.git = fakeGit("https://user:hunter2@github.com/example/app.git")
	out, _, _ := e.run("where")
	if strings.Contains(out, "hunter2") || !strings.Contains(out, "remote origin is example/app") {
		t.Errorf("where leaked or missed the remote:\n%s", out)
	}
}

func TestScopeHelpAndFlags(t *testing.T) {
	e := newEnv(t)
	out, _, _ := e.run("--help")
	if !strings.Contains(out, "Projects and scope") || !strings.Contains(out, "-R all") {
		t.Errorf("usage lacks the scope rules:\n%s", out)
	}
	if _, errs, code := e.run("ls", "-R", "nope"); code != 2 || !strings.Contains(errs, "invalid choice") {
		t.Errorf("unknown alias: %d %q", code, errs)
	}
}

func TestMilestoneWritesFollowScope(t *testing.T) {
	e := newEnv(t)
	// all-repos mode: which repo?
	_, errs, code := e.run("milestones", "new", "Beta")
	if code != 2 || !strings.Contains(errs, "which repo?") || !strings.Contains(errs, "app, lib") {
		t.Errorf("global milestones new: %d %q", code, errs)
	}
	// inside a project the repo is implied, and -R still wins
	e.git = fakeGit(appRemote)
	if out, errs, code := e.run("milestones", "new", "Beta"); code != 0 || !strings.Contains(out, `milestone app#M-1 "Beta"`) {
		t.Errorf("scoped milestones new: %d %q %q", code, out, errs)
	}
	if out, _, code := e.run("milestones", "new", "Gamma", "-R", "lib"); code != 0 || !strings.Contains(out, `milestone lib#M-1 "Gamma"`) {
		t.Errorf("-R lib: %d %q", code, out)
	}
	// a milestone that exists only in another repo is not found from this project, but --all finds it
	if _, errs, code := e.run("milestones", "close", "Gamma"); code == 0 {
		t.Errorf("Gamma lives in lib, not in the app project: %q", errs)
	}
	if out, errs, code := e.run("milestones", "close", "Gamma", "--all"); code != 0 {
		t.Errorf("--all: %d %q %q", code, out, errs)
	}
}

func TestSweepFollowsScope(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	e.write("claims/lib-3.json", `{"agent": "zed", "pid": 0, "host": "box", "ts": 1772300000, "ttl": 60}`+"\n") // long expired
	e.git = fakeGit(appRemote)
	if out, _, _ := e.run("sweep"); strings.Contains(out, "lib#3") {
		t.Errorf("sweep in the app project must not list lib: %q", out)
	}
	if out, _, _ := e.run("sweep", "--all"); !strings.Contains(out, "lib#3") {
		t.Errorf("sweep --all: %q", out)
	}
}
