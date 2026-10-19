package issues

import (
	"strings"
	"testing"
)

func failFirstOp(e *env) {
	e.t.Helper()
	ops := e.app().loadOutbox()
	msg := "boom"
	ops[0].LastError = &msg
	if err := e.app().mirror().saveOp(ops[0]); err != nil {
		e.t.Fatal(err)
	}
}

// effectiveStates is the state of app#7 as ls, show, show --json, Session.Load and Session.Detail print it.
func effectiveStates(t *testing.T, e *env) map[string]string {
	t.Helper()
	got := map[string]string{}
	got["ls"] = strings.Split(lsRow(t, e, "app#7"), "\t")[1]
	out, _, _ := e.run("show", "app#7")
	for _, l := range strings.Split(out, "\n") {
		if _, set := got["show"]; !set && strings.HasPrefix(l, "state: ") {
			got["show"] = strings.Trim(strings.TrimPrefix(l, "state: "), `"`)
		}
		if _, rest, ok := strings.Cut(l, "effective state: "); ok { // printed when queued writes apply
			got["show"], _, _ = strings.Cut(rest, " ")
			break
		}
	}
	got["show --json"] = showJSONOf(t, e, "app#7")["state"].(string)
	s := e.session()
	for _, it := range s.Load().Items {
		if it.Ref == "app#7" {
			got["Load"] = it.State
		}
	}
	d, err := s.Detail("app#7")
	if err != nil {
		t.Fatal(err)
	}
	got["Detail"] = d.State
	return got
}

// moon#39: every reader shows the same effective state.
func TestFailedOpShownAsOpenEverywhere(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	e.run("close", "app#7")
	for k, v := range effectiveStates(t, e) {
		if v != "closed" {
			t.Errorf("queued close, %s: state %q, want closed", k, v)
		}
	}
	failFirstOp(e)
	for k, v := range effectiveStates(t, e) {
		if v != "open" {
			t.Errorf("failed close, %s: state %q, want open", k, v)
		}
	}
}
