package completion

import (
	"strings"
	"testing"

	"github.com/your-moon/moon/internal/issues"
)

// scopedOpts is the fixture with a working directory whose git remote is erp.
func (f *fixture) scopedOpts() Options {
	o := f.opts()
	o.Open = func(cfg, dir string) Source {
		return OpenSession(f.cfg, dir, issues.Env{Getenv: func(string) string { return "" }, Cwd: f.dir,
			GitRemote: func(string) (string, error) { return "git@github.com:Example/erp.git", nil }})
	}
	return o
}

func TestCompletionHonorsProjectScope(t *testing.T) {
	f := newFixture(t, standard)
	o := f.scopedOpts()
	c := func(words ...string) string { return list(Complete(o, words)) }

	// refs: only the project's repo, though the mirror also has a web issue
	if g := c("issues", "show", ""); strings.Contains(g, "web#") || !strings.Contains(g, "erp#510") {
		t.Errorf("refs in scope: %q", g)
	}
	// -R and --all widen it
	if g := c("issues", "show", "-R", "web", ""); g != "web#3" {
		t.Errorf("-R web: %q", g)
	}
	if g := c("issues", "show", "-R", "all", ""); !strings.Contains(g, "web#3") || !strings.Contains(g, "erp#510") {
		t.Errorf("-R all: %q", g)
	}
	if g := c("issues", "show", "--all", ""); !strings.Contains(g, "web#3") || !strings.Contains(g, "erp#510") {
		t.Errorf("--all: %q", g)
	}
	// one repo in scope: a bare number completes too, with and without the hash
	want(t, "bare number", c("issues", "show", "51"), "510 51")
	want(t, "bare number with #", c("issues", "show", "#51"), "#510 #51")
	if g := c("issues", "show", "-R", "all", "5"); strings.Contains(g, " 5") && !strings.Contains(g, "#") {
		t.Errorf("all repos offers alias refs, not bare numbers: %q", g)
	}
	// label and milestone values follow the scope: docs is an erp label, launch is a web milestone
	if g := c("issues", "ls", "-m", ""); strings.Contains(g, "launch") || !strings.Contains(g, "v1") {
		t.Errorf("milestones in scope: %q", g)
	}
	if g := c("issues", "ls", "--all", "-m", ""); !strings.Contains(g, "launch") {
		t.Errorf("--all milestones: %q", g)
	}
	// -R takes "all" as a value
	if g := c("issues", "ls", "-R", ""); g != "erp web all" {
		t.Errorf("-R values: %q", g)
	}
	// without a project nothing changes
	if g := f.complete("issues", "show", ""); !strings.Contains(list(g), "web#3") {
		t.Errorf("global refs: %q", list(g))
	}
}
