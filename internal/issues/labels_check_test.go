package issues

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func seedLabels(e *env, names ...string) {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\tcc0000\t\n", n)
	}
	e.putFile("app", fileLabels, b.String())
}

func queuedLabels(t *testing.T, e *env) string {
	t.Helper()
	ops := e.app().loadOutbox()
	if len(ops) != 1 {
		t.Fatalf("want 1 queued op, have %d", len(ops))
	}
	return mustJSON(ops[0].Args["labels"])
}

// The incident: `new -L area::does-not-exist` queued fine and then failed in sync forever.
func TestNewRefusesUnknownLabel(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug", "area::backend", "area::frontend")
	out, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "-L", "area::does-not-exist")
	if code != 2 || out != "" || queued(e) != 0 {
		t.Fatalf("code=%d out=%q queued=%d", code, out, queued(e))
	}
	contains0(t, "message", errs, `app has no such label, nothing was queued`)
	contains0(t, "names the label", errs, `"area::does-not-exist"`)
	if strings.Contains(errs, "did you mean") {
		t.Fatalf("nothing is close to that label, so nothing should be suggested: %s", errs)
	}
}

func TestUnknownLabelListsCloseMatches(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug", "area::backend", "area::frontend", "kind::feature")
	_, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "-L", "area::backnd", "-L", "backend", "-L", "bug", "-L", "feature")
	eq(t, "exit", fmt.Sprint(code), "2")
	// a typo, a short name and a suffix each point at the real label; the valid "bug" is not listed
	want := "  \"area::backnd\" (did you mean area::backend?)\n  \"backend\" (did you mean area::backend?)\n  \"feature\" (did you mean kind::feature?)"
	contains0(t, "suggestions", errs, want)
	if strings.Contains(errs, `"bug"`) {
		t.Fatalf("a valid label must not be reported: %s", errs)
	}
}

func TestKnownLabelsAreQueuedInGitHubSpelling(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug", "Area::Backend")
	if _, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "-L", "BUG", "-L", "area::backend"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	eq(t, "queued", queuedLabels(t, e), `["bug","Area::Backend"]`)
}

func TestLabelAddChecksButRemoveDoesNot(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug", "urgent")
	_, errs, code := e.run("label", "app#7", "--add", "urgnt")
	if code != 2 || queued(e) != 0 || !strings.Contains(errs, `"urgnt" (did you mean urgent?)`) {
		t.Fatalf("code=%d queued=%d err=%q", code, queued(e), errs)
	}
	if _, errs, code = e.run("label", "app#7", "--add", "URGENT", "--remove", "gone-label"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	ops := e.app().loadOutbox()
	eq(t, "add", mustJSON(ops[0].Args["add"]), `["urgent"]`)
	eq(t, "remove", mustJSON(ops[0].Args["remove"]), `["gone-label"]`)
}

// Without a trustworthy list there is nothing to check against: never pulled, a list as long as one fetch
// (cut off at 100, so a label may be missing from it), and a local-only repo (no GitHub).
func TestLabelsNotCheckedWhenListIsUnknownOrIncomplete(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "-L", "whatever"); code != 0 {
		t.Fatal("never pulled: must be accepted")
	}
	var many []string
	for i := 0; i < labelsFetched; i++ {
		many = append(many, fmt.Sprintf("l%d", i))
	}
	seedLabels(e, many...)
	if _, _, code := e.run("new", "-R", "app", "--title", "t2", "--body", "x", "-L", "whatever"); code != 0 {
		t.Fatal("a list of exactly 100 may be truncated: must be accepted")
	}
	seedLabels(e, many[:labelsFetched-1]...)
	if _, _, code := e.run("new", "-R", "app", "--title", "t3", "--body", "x", "-L", "whatever"); code != 2 {
		t.Fatal("99 labels is a complete list: must be refused")
	}
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.notes]\nlocal = true\n"), 0o644)
	e.putFile("notes", fileLabels, "bug\tcc0000\t\n")
	if _, _, code := e.run("new", "-R", "notes", "--title", "t", "--body", "x", "-L", "free-form"); code != 0 {
		t.Fatal("local-only repo: any label is fine")
	}
}

func TestCloseLabelsCapsAtFive(t *testing.T) {
	names := []string{"bug1", "bug2", "bug3", "bug4", "bug5", "bug6", "bug7"}
	eq(t, "cap", strings.Join(closeLabels("bug", names), ","), "bug1,bug2,bug3,bug4,bug5")
}

// Short words tolerate one typo, longer ones two: "gub" is two edits from "bug", "bugs" is one.
func TestCloseLabelsTypoBudgetByLength(t *testing.T) {
	eq(t, "3 letters, 2 edits", strings.Join(closeLabels("gub", []string{"bug"}), ","), "")
	eq(t, "3 letters, 1 edit", strings.Join(closeLabels("bu", []string{"bug"}), ","), "bug")
	eq(t, "4 letters, 2 edits", strings.Join(closeLabels("bgus", []string{"bug"}), ","), "bug")
}

func TestRepoWithNoLabelsRefusesAnyLabel(t *testing.T) {
	e := newEnv(t)
	e.putFile("app", fileLabels, "")
	if _, _, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "-L", "bug"); code != 2 || queued(e) != 0 {
		t.Fatalf("code=%d queued=%d", code, queued(e))
	}
}
