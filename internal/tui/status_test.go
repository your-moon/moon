package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/your-moon/moon/internal/issues"
)

const longLbl = "kind:bug,area:payments-gateway,triage:needs-information,priority-high,customer-reported"

// invented data: one issue in every status, one with a pile of long labels
var statusItems = []fx{
	{9, "open", "v2", longLbl, "2026-02-27T00:00:00Z", "2026-02-09T00:00:00Z", "checkout: card form loses focus after the redirect", "Steps are in the first comment."},
	{8, "open", "", "triage:needs-decision,kind:feature", "2026-02-26T00:00:00Z", "2026-02-08T00:00:00Z", "export: which CSV dialect do we promise", ""},
	{7, "closed", "v1", "kind:bug", "2026-02-25T00:00:00Z", "2026-02-07T00:00:00Z", "login: crash on empty password", ""},
	{6, "open", "v1", "kind:bug,area:auth", "2026-02-24T00:00:00Z", "2026-02-06T00:00:00Z", "auth: refresh token loops", ""},
	{5, "open", "v2", "kind:feature", "2026-02-23T00:00:00Z", "2026-02-05T00:00:00Z", "export: add CSV download", ""},
	{4, "open", "", "kind:bug,triage:ready", "2026-02-22T00:00:00Z", "2026-02-04T00:00:00Z", "docs: explain config lookup", ""},
	{3, "open", "", "", "2026-02-21T00:00:00Z", "2026-02-03T00:00:00Z", "chore: bump the linter", ""},
}

// statusMirror: #6 has a fresh claim (11 min), #3 a stale one, #5 has an open PR, #7 is closed with a claim left behind.
func statusMirror(t *testing.T) *mirror {
	mi := newMirror(t, statusItems)
	mi.claim("app#6", "sonnet-17", 660, 7200)
	mi.claim("app#3", "haiku", 9660, 7200)
	mi.claim("app#7", "opus", 3600, 7200)
	mi.putPRs("app", "50\topen\t0\tfeature-5\tabc\tmain\t2026-02-22T00:00:00Z\t\t\tdana\t2026-02-23T00:00:00Z\t5\texport: CSV download\n")
	return mi
}

func rowOf(view, ref string) string {
	for _, l := range strings.Split(view, "\n") {
		if len(l) > 2 && strings.HasPrefix(l[2:], ref+" ") { // two columns of mark, then the ref
			return l
		}
	}
	return ""
}

func TestStatusColumnAndDetail(t *testing.T) {
	m := statusMirror(t).model(120, 30)
	v := m.View()
	for ref, want := range map[string]string{"app#6": "working", "app#3": "stale", "app#5": "in review", "app#4": "ready", "app#8": "blocked", "app#9": "open"} {
		if !strings.Contains(rowOf(v, ref), want) {
			t.Errorf("%s should show %q:\n%s", ref, want, rowOf(v, ref))
		}
	}
	if !strings.Contains(v, "STATUS") {
		t.Errorf("no STATUS header:\n%s", v)
	}
	// detail: status line right after the claim holder
	m = press(m, "x") // closes app#9 (queued): not what we want here; undo below
	m = press(m, "x")
	m = send(m, keyMsg("g"))
	for m.cur().Ref != "app#6" {
		m = press(m, "j")
	}
	d := strings.Join(m.detailLines(60), "\n")
	if !strings.Contains(d, "claim: sonnet-17 on box, 11m ago\n") && !strings.Contains(d, "claim: sonnet-17 on box, 11m ago") {
		t.Errorf("claim holder:\n%s", d)
	}
	if !strings.Contains(d, "status: working: sonnet-17 11m") {
		t.Errorf("status line:\n%s", d)
	}
	if i, j := strings.Index(d, "claim:"), strings.Index(d, "status:"); i < 0 || j < i {
		t.Errorf("status must follow claim:\n%s", d)
	}
}

func TestWorkingToggle(t *testing.T) {
	m := statusMirror(t).model(120, 30)
	all := refs(m)
	m = press(m, "w")
	eq(t, "working only", refs(m), "app#6")
	if !strings.Contains(m.View(), "status:working") {
		t.Errorf("footer should say the filter is on:\n%s", m.View())
	}
	m = press(m, "w")
	eq(t, "all again", refs(m), all)
	// closing the working issue takes it out of the working list at once (queued locally, claim released)
	m = press(m, "w")
	m = press(m, "x")
	eq(t, "closed issue is not working", refs(m), "")
}

func TestClaimsTabMarksStaleAndClosed(t *testing.T) {
	mi := statusMirror(t)
	rows := mi.sess.Claims()
	byRef := map[string]issues.ClaimRow{}
	for _, r := range rows {
		byRef[r.Ref] = r
	}
	if !byRef["app#7"].IssueClosed || byRef["app#7"].Work.Name != issues.StDone || byRef["app#6"].IssueClosed || !byRef["app#3"].Stale {
		t.Fatalf("%+v", rows)
	}
	v := sdo(mi.shell(110, 20), "3", "j").View() // cursor on app#6, so the marks of the other two show
	for ref, want := range map[string]string{"app#6": "> app#6 working", "app#3": "! app#3 stale", "app#7": "! app#7 done (stale claim)"} {
		if !strings.Contains(rowOf(v, ref), want) {
			t.Errorf("%s: want %q in %q", ref, want, rowOf(v, ref))
		}
	}
	if !strings.Contains(v, "3 claims, 1 stale, 1 on closed issues") {
		t.Errorf("summary:\n%s", v)
	}
}

func TestLabelsKeyCyclesAndDefaultsByWidth(t *testing.T) {
	for w, want := range map[int]string{140: "full", 110: "full", 109: "short", 80: "short", 70: "short", 69: "hidden", 50: "hidden"} {
		m := statusMirror(t).model(w, 24)
		if !strings.Contains(m.View(), "labels:"+want) {
			t.Errorf("width %d: footer should say labels:%s:\n%s", w, want, strings.Split(m.View(), "\n")[22])
		}
	}
	m := statusMirror(t).model(120, 30)
	hdr := func() string { return strings.Split(m.View(), "\n")[0] }
	if !strings.Contains(rowOf(m.View(), "app#6"), "kind:bug,area:auth") {
		t.Fatalf("full mode shows stored names:\n%s", m.View())
	}
	m = press(m, "l")
	if !strings.Contains(m.View(), "labels:short") || !strings.Contains(rowOf(m.View(), "app#6"), "bug,auth") || strings.Contains(rowOf(m.View(), "app#6"), "kind:") {
		t.Errorf("short mode:\n%s", m.View())
	}
	m = press(m, "l")
	if !strings.Contains(m.View(), "labels:hidden") || strings.Contains(hdr(), "LABELS") {
		t.Errorf("hidden mode:\n%s", m.View())
	}
	m = press(m, "l")
	if !strings.Contains(m.View(), "labels:full") || !strings.Contains(hdr(), "LABELS") {
		t.Errorf("back to full:\n%s", m.View())
	}
}

func TestLabelsColumnNeverCutsAName(t *testing.T) {
	for _, w := range []int{160, 120, 110} {
		m := statusMirror(t).model(w, 24)
		row := rowOf(m.View(), "app#9")
		_, listW, _, _, _, _ := m.geometry()
		cap := listW * 45 / 100
		want := issues.FitLabels(strings.Split(longLbl, ","), issues.LabelsFull, cap)
		if !strings.Contains(row, want) {
			t.Errorf("w=%d: row should hold %q (cap %d):\n%s", w, want, cap, row)
		}
		if strings.Contains(strings.SplitN(row, "checkout", 2)[0], "…") {
			t.Errorf("w=%d: a label was cut with an ellipsis: %q", w, row)
		}
		if !strings.Contains(want, " +") {
			t.Errorf("w=%d: fixture should overflow the cap", w)
		}
	}
}

func TestDetailListsEveryLabel(t *testing.T) {
	for _, w := range []int{120, 80, 50, 40} {
		m := statusMirror(t).model(w, 30)
		m = press(m, "enter") // full-screen detail of app#9
		var lines []string
		for _, l := range m.detailLines(w) {
			lines = append(lines, ansiStrip(l))
		}
		d := strings.Join(lines, "\n")
		for _, l := range strings.Split(longLbl, ",") {
			if !strings.Contains(strings.ReplaceAll(d, "\n       ", " "), l) {
				t.Errorf("w=%d: label %q cut or missing:\n%s", w, l, d)
			}
		}
		for _, l := range lines {
			if strings.HasPrefix(l, "labels:") || strings.HasPrefix(l, "       ") {
				if strings.Contains(l, "…") || len([]rune(l)) > w {
					t.Errorf("w=%d: label line cut or too wide: %q", w, l)
				}
			}
		}
	}
}

func TestQueuedCountsOperations(t *testing.T) {
	mi := statusMirror(t)
	m := mi.model(120, 30)
	m = typed(m, "m") // comment prompt on app#9
	m = typed(m, "first")
	m = press(m, "enter")
	if !strings.Contains(m.View(), "queued: 1 op") || strings.Contains(m.View(), "queued: 1 ops") {
		t.Fatalf("one op:\n%s", m.View())
	}
	m = typed(m, "m")
	m = typed(m, "second") // a second operation on the same issue
	m = press(m, "enter")
	if mi.outbox() != 2 || !strings.Contains(m.View(), "queued: 2 ops") {
		t.Fatalf("two ops on one issue must read 2, outbox %d:\n%s", mi.outbox(), m.View())
	}
	m = press(m, "j", "x") // a close on another issue
	if mi.outbox() != 3 || !strings.Contains(m.View(), "queued: 3 ops") {
		t.Fatalf("three ops across two issues:\n%s", m.View())
	}
	_ = fmt.Sprint
}

func TestGoldenStatusViews(t *testing.T) {
	mi := statusMirror(t)
	for _, c := range []struct {
		name string
		w, h int
	}{{"status_120", 120, 24}, {"status_80", 80, 24}, {"status_50", 50, 20}} {
		m := mi.model(c.w, c.h)
		golden(t, c.name, m.View())
	}
	m := press(mi.model(120, 24), "j", "j") // app#6, the working one: claim holder and status in the detail pane
	golden(t, "status_detail_120", m.View())
	m = press(mi.model(120, 24), "w")
	golden(t, "status_working_120", m.View())
	golden(t, "status_claims_110", sdo(mi.shell(110, 14), "3").View())
}
