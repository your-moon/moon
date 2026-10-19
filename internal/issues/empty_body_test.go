package issues

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func emptyFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func queued(e *env) int { return len(e.app().loadOutbox()) }

// The incident: an empty --body-file queued an edit that sync then pushed, erasing the body on GitHub.
func TestEditRefusesEmptyBody(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	for name, args := range map[string][]string{
		"empty file":   {"--body-file", emptyFile(t, "")},
		"blank file":   {"--body-file", emptyFile(t, " \n\t\n")},
		"empty --body": {"--body", ""},
		"blank --body": {"--body", "  "},
		"stdin (dash)": {"--body-file", "-"}, // stdin is empty in these tests
	} {
		out, errs, code := e.run(append([]string{"edit", "app#7"}, args...)...)
		if code != 2 || out != "" || !strings.Contains(errs, "edit: the body from ") || !strings.Contains(errs, "--allow-empty-body") {
			t.Errorf("%s: code=%d out=%q err=%q", name, code, out, errs)
		}
		if n := queued(e); n != 0 {
			t.Fatalf("%s: %d op(s) queued after a refusal", name, n)
		}
	}
}

func TestEditEmptyBodyAllowedOnRequest(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	_, errs, code := e.run("edit", "app#7", "--body-file", emptyFile(t, ""), "--allow-empty-body")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errs)
	}
	ops := e.app().loadOutbox()
	if len(ops) != 1 || ops[0].Op != "edit" || argStr(ops[0].Args, "body") != "" {
		t.Fatalf("%+v", ops)
	}
	if _, ok := ops[0].Args["body"]; !ok {
		t.Fatal("the edit must carry an explicit empty body")
	}
}

func TestEditWithRealBodyAndTitleOnlyStillQueue(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	if _, errs, code := e.run("edit", "app#7", "--body-file", emptyFile(t, "new text\n")); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if _, errs, code := e.run("edit", "app#7", "--title", "only the title"); code != 0 { // no body asked for: nothing to guard
		t.Fatalf("%d %s", code, errs)
	}
	eq(t, "queued", mustJSON(queued(e)), "2")
}

func TestNewRefusesEmptyBodyFileButNotEmptyBodyFlag(t *testing.T) {
	e := newEnv(t)
	_, errs, code := e.run("new", "--repo", "app", "--title", "t", "--body-file", emptyFile(t, ""))
	if code != 2 || !strings.Contains(errs, "new: the body from --body-file ") || queued(e) != 0 {
		t.Fatalf("code=%d err=%q queued=%d", code, errs, queued(e))
	}
	if _, _, code = e.run("new", "--repo", "app", "--title", "t", "--body", ""); code != 0 || queued(e) != 1 {
		t.Fatalf("an explicit empty --body is still allowed: code=%d queued=%d", code, queued(e))
	}
	if _, _, code = e.run("new", "--repo", "app", "--title", "t2", "--body-file", emptyFile(t, ""), "--allow-empty-body"); code != 0 || queued(e) != 2 {
		t.Fatalf("code=%d queued=%d", code, queued(e))
	}
}

func TestCommentRefusesBlankBody(t *testing.T) {
	e := newEnv(t)
	_, errs, code := e.run("comment", "app#7", "--body-file", emptyFile(t, "\n\n"))
	if code != 2 || !strings.Contains(errs, "comment needs --body or --body-file") || queued(e) != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestLocalOnlyEditKeepsBodyOnEmptyFile(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.notes]\nlocal = true\n"), 0o644)
	e.run("new", "--repo", "notes", "--title", "t", "--body", "keep me")
	_, _, code := e.run("edit", "notes#1", "--body-file", emptyFile(t, ""))
	if code != 2 || !strings.Contains(e.getIssue("notes", "1"), "keep me") {
		t.Fatalf("code=%d file=%s", code, e.getIssue("notes", "1"))
	}
}
