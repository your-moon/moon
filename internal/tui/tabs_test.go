package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/your-moon/moon/internal/issues"
)

func (mi *mirror) milestones(alias string, rows ...string) {
	mi.write(alias+"/milestones.tsv", strings.Join(rows, "\n")+"\n")
}

func (mi *mirror) shell(w, h int) Shell {
	var s tea.Model = NewShell([]Tab{
		NewIssuesTab(mi.sess, Options{Agent: "me", MirrorDir: "mirror"}),
		NewMilestonesTab(mi.sess),
		NewClaimsTab(mi.sess),
	}, 0)
	s, _ = s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return s.(Shell)
}

// sdo sends a key and runs the returned command once, feeding its message back (drill-in).
func sdo(s Shell, keys ...string) Shell {
	for _, k := range keys {
		nm, cmd := s.Update(keyMsg(k))
		s = nm.(Shell)
		if cmd != nil {
			if msg := cmd(); msg != nil {
				if _, quit := msg.(tea.QuitMsg); !quit {
					nm, _ = s.Update(msg)
					s = nm.(Shell)
				}
			}
		}
	}
	return s
}

func TestTabSwitching(t *testing.T) {
	s := newMirror(t, base).shell(100, 30)
	if s.Active() != 0 {
		t.Fatal("starts on Issues")
	}
	for _, c := range []struct {
		keys string
		want int
	}{{"2", 1}, {"3", 2}, {"1", 0}, {"]", 1}, {"]", 2}, {"]", 0}, {"[", 2}, {"[", 1}, {"9", 1}} {
		s = sdo(s, c.keys)
		if s.Active() != c.want {
			t.Fatalf("after %q active = %d, want %d", c.keys, s.Active(), c.want)
		}
	}
	v := sdo(s, "1").View()
	if !strings.HasPrefix(v, "[1 Issues]  2 Milestones   3 Claims") {
		t.Fatalf("tab bar: %q", strings.SplitN(v, "\n", 2)[0])
	}
	// digits typed into the search box are text, not tab switches
	s = sdo(s, "1", "/", "2", "3")
	if s.Active() != 0 || !strings.Contains(s.View(), "/23_") {
		t.Fatalf("search input was captured by the shell:\n%s", s.View())
	}
	// every tab fits every size
	for _, sz := range [][2]int{{120, 36}, {50, 20}, {20, 6}, {5, 2}} {
		s2, _ := s.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		for _, k := range []string{"esc", "1", "2", "3"} {
			s2, _ = s2.Update(keyMsg(k))
			for _, l := range strings.Split(s2.View(), "\n") {
				if lipgloss.Width(l) > sz[0] {
					t.Fatalf("%v tab %s: %q too wide", sz, k, l)
				}
			}
		}
	}
}

func TestClaimsTab(t *testing.T) {
	mi := newMirror(t, base)
	mi.claim("app#2", "robin", 600, 7200)
	mi.claim("app#7", "sam", 9000, 60) // expired
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	s := mi.shell(100, 20)
	s = sdo(s, "3")
	var fresh, stale string
	for _, l := range strings.Split(s.View(), "\n") {
		if strings.Contains(l, "app#2") {
			fresh = l
		}
		if strings.Contains(l, "app#7") {
			stale = l
		}
	}
	if !strings.Contains(fresh, "robin") || !strings.Contains(fresh, "10m") || !strings.Contains(fresh, "2h") {
		t.Fatalf("fresh row lacks agent, age or ttl: %q", fresh)
	}
	if strings.Contains(fresh, "\x1b[2m") || strings.Contains(fresh, "STALE") {
		t.Fatalf("fresh row must not be dimmed: %q", fresh)
	}
	if !strings.Contains(stale, "STALE") || !strings.Contains(stale, "\x1b[2m") {
		t.Fatalf("stale row must be dimmed and say STALE: %q", stale)
	}
	if !strings.Contains(s.View(), "2 claims, 1 stale") {
		t.Fatalf("summary missing:\n%s", s.View())
	}
	// same data as `moon issues claims`
	if rows := mi.sess.Claims(); len(rows) != 2 || rows[0].Ref != "app#2" || rows[1].Ref != "app#7" || !rows[1].Stale {
		t.Fatalf("%+v", rows)
	}
	// empty
	s = newMirror(t, base).shell(80, 20)
	if v := sdo(s, "3").View(); !strings.Contains(v, "No claims") {
		t.Fatalf("empty claims:\n%s", v)
	}
}

func TestNoConfigShowsTheSearchPlaces(t *testing.T) {
	_, err := issues.NewSession("", "", issues.Env{Getenv: func(string) string { return "" }, Cwd: t.TempDir()})
	if err == nil {
		t.Skip("a moon.toml exists above the temp dir on this machine")
	}
	var s tea.Model = ErrorShell(err.Error())
	s, _ = s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	v := s.View()
	for _, want := range []string{"no moon.toml found", "--config FILE", "$MOON_CONFIG", "every parent directory", "Press q to quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "goroutine") {
		t.Error("stack trace")
	}
	if _, cmd := s.Update(keyMsg("q")); cmd == nil {
		t.Error("q must quit")
	}
}

// ---------- milestones ----------

func TestProgressMath(t *testing.T) {
	for _, c := range []struct{ closed, total, want int }{
		{0, 0, 0}, {0, 7, 0}, {1, 1000, 0}, {5, 10, 50}, {996, 1000, 99}, {999, 1000, 99}, {1000, 1000, 100}, {1, 1, 100}, {2, 3, 66}, {3, 0, 0},
	} {
		if got := percent(c.closed, c.total); got != c.want {
			t.Errorf("percent(%d,%d) = %d, want %d", c.closed, c.total, got, c.want)
		}
	}
	for _, c := range []struct {
		closed, total, w int
		ascii            bool
		want             string
	}{
		{0, 0, 10, true, "[--------]"}, {0, 4, 10, true, "[--------]"}, {2, 4, 10, true, "[####----]"},
		{3999, 4000, 10, true, "[#######-]"}, {4, 4, 10, true, "[########]"},
	} {
		if got := bar(c.closed, c.total, c.w, c.ascii); got != c.want {
			t.Errorf("bar(%d,%d,%d) = %q, want %q", c.closed, c.total, c.w, got, c.want)
		}
	}
	if b := bar(1, 2, 10, false); strings.Count(b, "█") != 5 || strings.Count(b, "░") != 5 {
		t.Errorf("block bar: %q", b)
	}
}

const (
	msV1  = "v1\topen\t6\t4\t2026-03-10T00:00:00Z"
	msV2  = "v2\topen\t1\t3\t2026-03-01T00:00:00Z"
	msV3  = "v3\topen\t0\t0\t"
	msOld = "alpha\tclosed\t9\t0\t2026-01-01T00:00:00Z"
)

func milestoneNames(s Shell) []string {
	var out []string
	for _, l := range strings.Split(s.View(), "\n")[2:] {
		f := strings.Fields(l)
		if len(f) > 1 && f[0] != "REPO" {
			if f[0] == ">" {
				f = f[1:]
			}
			if f[0] == "(no" {
				out = append(out, "(no milestone)")
			} else if !strings.Contains(l, "shown of") && !strings.Contains(l, "j/k") {
				out = append(out, f[0])
			}
		}
	}
	return out
}

func TestMilestonesTab(t *testing.T) {
	mi := newMirror(t, base)
	mi.milestones("app", msV1, msV2, msV3, msOld)
	s := sdo(mi.shell(100, 20), "2")
	got := strings.Join(milestoneNames(s), " ")
	eq(t, "sorted by due, then title; no-due and (no milestone) last", got, "alpha v2 v1 v3 (no milestone)")
	v := s.View()
	for _, want := range []string{"6/10", "60%", "0/0", "2026-03-10", "(no milestone)"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// (no milestone): app#4 (open) and app#6 (closed) have none
	if !strings.Contains(v, "1/2") {
		t.Errorf("no-milestone row should be 1/2:\n%s", v)
	}
	s = sdo(s, "s")
	eq(t, "progress", strings.Join(milestoneNames(s), " "), "alpha v1 v2 v3 (no milestone)")
	s = sdo(s, "s")
	eq(t, "title", strings.Join(milestoneNames(s), " "), "alpha v1 v2 v3 (no milestone)")
	s = sdo(s, "s")
	eq(t, "open count", strings.Join(milestoneNames(s), " "), "v1 v2 alpha v3 (no milestone)")
	s = sdo(s, "s")
	eq(t, "back to due", strings.Join(milestoneNames(s), " "), "alpha v2 v1 v3 (no milestone)")
	s = sdo(s, "x")
	eq(t, "x hides fully closed", strings.Join(milestoneNames(s), " "), "v2 v1 v3 (no milestone)")
	s = sdo(s, "x")
	eq(t, "x shows them again", strings.Join(milestoneNames(s), " "), "alpha v2 v1 v3 (no milestone)")
}

func TestMilestoneDrillInFiltersIssues(t *testing.T) {
	mi := newMirror(t, base)
	mi.milestones("app", msV1, msV2)
	s := sdo(mi.shell(120, 30), "2", "j", "enter") // alpha-less list: v2, v1 -> second row is v1
	if s.Active() != 0 {
		t.Fatalf("active = %d, want the Issues tab", s.Active())
	}
	it := s.tabs[0].(*issuesTab).m
	if it.fMilestone != "v1" || it.fState != "all" {
		t.Fatalf("filter milestone=%q state=%q", it.fMilestone, it.fState)
	}
	eq(t, "issues in v1 (open and closed)", refs(it), "app#7 app#2 app#1")
	if v := s.View(); !strings.Contains(v, "[milestone: v1]") {
		t.Fatalf("chip missing:\n%s", v)
	}
	s = sdo(s, "esc")
	it = s.tabs[0].(*issuesTab).m
	if it.fMilestone != "" || it.fState != "open" || refs(it) != "app#7 app#5 app#4 app#3 app#2" {
		t.Fatalf("esc must clear the chip and restore the state: %q %q %s", it.fMilestone, it.fState, refs(it))
	}
	// the (no milestone) row
	s = sdo(s, "2", "G", "enter")
	it = s.tabs[0].(*issuesTab).m
	eq(t, "no milestone", refs(it), "app#6 app#4")
}

func TestMilestonePickerQueuesOneOp(t *testing.T) {
	mi := newMirror(t, base)
	mi.milestones("app", msV1, msV2)
	m := mi.model(120, 36) // app#7 is in v1
	m = press(m, "M")
	if m.mode != modePick || !strings.Contains(m.View(), "current: v1") {
		t.Fatalf("picker:\n%s", m.View())
	}
	m = typed(m, "v2")
	if got := m.pickMatches(); len(got) != 1 || got[0] != "v2" {
		t.Fatalf("filter: %v", got)
	}
	m = press(m, "enter")
	if mi.outbox() != 1 {
		t.Fatalf("outbox = %d, want exactly 1", mi.outbox())
	}
	if !strings.Contains(m.msg, "queued milestone v2 on app#7") || !strings.Contains(m.View(), "queued: 1") {
		t.Fatalf("msg %q\n%s", m.msg, m.View())
	}
	if m.cur().Milestone != "v2" {
		t.Fatalf("milestone now %q", m.cur().Milestone)
	}
	// same write path as the CLI: the op is a milestone op on the ref
	d, _ := mi.sess.Detail("app#7")
	if d.Milestone != "v2" || len(d.Pending) != 1 || !strings.Contains(d.Pending[0], "milestone -> v2") {
		t.Fatalf("%+v", d)
	}
	// "-" clears; esc cancels without queueing
	m = press(typed(press(m, "M"), "-"), "enter")
	if mi.outbox() != 2 || m.cur().Milestone != "" {
		t.Fatalf("outbox %d milestone %q", mi.outbox(), m.cur().Milestone)
	}
	m = press(typed(press(m, "M"), "v"), "esc")
	m = press(typed(press(m, "M"), "nomatch"), "enter")
	if mi.outbox() != 2 {
		t.Fatalf("outbox = %d after cancel and no match", mi.outbox())
	}
	// works from the full-screen detail too
	m = press(m, "enter", "M", "down", "enter")
	if mi.outbox() != 3 {
		t.Fatalf("outbox = %d from detail", mi.outbox())
	}
}

func TestMilestonesEmptyMirror(t *testing.T) {
	mi := newMirror(t, nil)
	s := sdo(mi.shell(80, 20), "2")
	v := s.View()
	if !strings.Contains(v, "No milestones in the mirror") {
		t.Fatalf("empty:\n%s", v)
	}
	s = sdo(s, "j", "k", "G", "enter", "s", "x", "r") // no panic
	mi2 := newMirror(t, []fx{{1, "open", "", "", "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z", "t", ""}})
	s = sdo(mi2.shell(80, 20), "2")
	if v := s.View(); !strings.Contains(v, "(no milestone)") || strings.Contains(v, "No milestones") {
		t.Fatalf("only the no-milestone row expected:\n%s", v)
	}
}

func TestGoldenMilestones(t *testing.T) {
	mi := newMirror(t, base)
	mi.milestones("app", msV1, msV2, msV3, msOld)
	for _, c := range []struct {
		name string
		w, h int
	}{{"ms_120", 120, 14}, {"ms_80", 80, 14}, {"ms_50", 50, 14}} {
		golden(t, c.name, sdo(mi.shell(c.w, c.h), "2").View())
	}
}
