package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/your-moon/moon/internal/issues"
)

func eq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s:\n got: %q\nwant: %q", what, got, want)
	}
}

func TestSearchNarrowsLikeLsQ(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	eq(t, "initial (open, number desc)", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = typed(press(m, "/"), "auth")
	eq(t, "title and label match", refs(m), "app#7 app#2")
	m = press(m, "enter")
	eq(t, "enter keeps the filter", refs(m), "app#7 app#2")
	m = press(m, "esc")
	eq(t, "esc clears it", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = typed(press(m, "/"), "DOCS") // case-insensitive, matches the label too
	eq(t, "case and label", refs(m), "app#4")
	// parity with the shared matcher, for every fixture row and several queries
	for _, q := range []string{"auth", "bug", "area::", "ünï", "日本", "zzz", "BUG,area", ""} {
		m2 := typed(press(m, "esc", "/"), q)
		for _, e := range m2.all {
			if e.it.State != "open" {
				continue
			}
			want := issues.QueryMatch(e.it.Title, strings.Join(e.it.Labels, ","), q)
			got := strings.Contains(refs(m2)+" ", e.it.Ref+" ")
			if got != want {
				t.Errorf("q=%q ref=%s: tui=%v QueryMatch=%v", q, e.it.Ref, got, want)
			}
		}
	}
}

func TestSortCyclesAndReverses(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	m = press(m, "s")
	eq(t, "updated", refs(m), "app#5 app#4 app#2 app#7 app#3")
	m = press(m, "s")
	eq(t, "created", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = press(m, "s")
	eq(t, "title", refs(m), "app#7 app#2 app#4 app#3 app#5")
	m = press(m, "s") // no claims in this mirror: every issue is "open", ties keep the number order
	eq(t, "status", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = press(m, "s")
	eq(t, "number again", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = press(m, "r")
	eq(t, "number reversed", refs(m), "app#2 app#3 app#4 app#5 app#7")
	m = press(m, "s", "s", "s") // updated, created, title; reverse persists across keys: title Z to A
	eq(t, "title reversed", refs(m), "app#5 app#3 app#4 app#2 app#7")
}

func TestFilterMenu(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	// f, down to label, enter, pick "area::auth" (options: any, area::auth, bug, docs, feature)
	m = press(m, "f", "down", "enter", "down", "enter")
	eq(t, "label filter", refs(m), "app#7 app#2")
	m = press(m, "f", "down", "down", "enter", "down", "down", "enter") // milestone: any, v1, v2 -> v2
	eq(t, "label and milestone", refs(m), "")
	m = press(m, "f", "down", "down", "down", "enter") // clear
	eq(t, "cleared", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = press(m, "f", "enter", "esc") // state: open -> closed
	eq(t, "closed", refs(m), "app#6 app#1")
	m = press(m, "f", "enter", "esc") // -> all
	eq(t, "all", refs(m), "app#7 app#6 app#5 app#4 app#3 app#2 app#1")
}

func TestSelectionSurvivesRefilter(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	m = press(m, "j", "j", "j") // app#3
	eq(t, "selected", m.selRef, "app#3")
	m = typed(press(m, "/"), "e") // matches app#3 (export), app#2? title "token refresh" has e; keep checking selection only
	eq(t, "selection kept after search", m.cur().Ref, "app#3")
	m = press(m, "esc", "s") // different order
	eq(t, "selection kept after sort", m.cur().Ref, "app#3")
	m = press(m, "r")
	eq(t, "selection kept after reverse", m.cur().Ref, "app#3")
	// a filter that hides the selection falls back to a neighbour, not a crash
	m = typed(press(m, "/"), "docs")
	if m.cur() == nil || m.cur().Ref != "app#4" {
		t.Fatalf("want app#4, got %+v", m.cur())
	}
}

func TestClaimHeldByAnotherAgentIsRefused(t *testing.T) {
	mi := newMirror(t, base)
	mi.claim("app#7", "robin", 60, 7200)
	m := mi.model(120, 36)
	if m.cur().Ref != "app#7" {
		t.Fatal(m.cur().Ref)
	}
	m = press(m, "c")
	eq(t, "claim refusal", m.msg, "not claimed: app#7 is held by robin (60s)") // the model's own check, before the store is asked
	if got := mi.sess.Claims(); len(got) != 1 || got[0].Agent != "robin" {
		t.Fatalf("claim changed: %+v", got)
	}
	m = press(m, "u")
	eq(t, "release refusal", m.msg, "not released: app#7 is held by robin (never forced)")
	if len(mi.sess.Claims()) != 1 {
		t.Fatal("release must not force")
	}
	// the store refuses on its own as well
	if _, err := mi.sess.Claim("app#7", "me"); err != issues.ErrHeld {
		t.Fatalf("store claim err = %v", err)
	}
	// my own claim on another issue works, shows in the list, and releases
	m = press(m, "j", "c")
	if !strings.HasPrefix(m.msg, "claimed app#5 for me") || m.cur().Claim == nil || m.cur().Claim.Agent != "me" {
		t.Fatalf("claim: %q %+v", m.msg, m.cur().Claim)
	}
	m = press(m, "u")
	if m.cur().Claim != nil || len(mi.sess.Claims()) != 1 {
		t.Fatalf("release: %q", m.msg)
	}
}

func TestStaleClaimCanBeTaken(t *testing.T) {
	mi := newMirror(t, base)
	mi.claim("app#7", "robin", 9000, 60) // ttl long expired
	m := press(mi.model(120, 36), "c")
	if m.cur().Claim == nil || m.cur().Claim.Agent != "me" {
		t.Fatalf("stale claim not taken: %q", m.msg)
	}
}

func TestQueuedWritesGoThroughOutboxOnce(t *testing.T) {
	mi := newMirror(t, base)
	m := mi.model(120, 36)
	m = press(m, "x") // close app#7
	if mi.outbox() != 1 {
		t.Fatalf("outbox = %d after one close", mi.outbox())
	}
	if !strings.Contains(m.msg, "queued close app#7") || !strings.Contains(m.View(), "queued: 1") {
		t.Fatalf("msg %q\n%s", m.msg, m.View())
	}
	if refs(m) != "app#5 app#4 app#3 app#2" { // the closed one leaves the open list
		t.Fatalf("rows %s", refs(m))
	}
	// comment through the one-line prompt (no $EDITOR)
	m = press(m, "m")
	if m.mode != modePrompt {
		t.Fatal("no prompt")
	}
	m = press(typed(m, "on it"), "enter")
	if mi.outbox() != 2 || !strings.Contains(m.View(), "queued: 2") {
		t.Fatalf("outbox = %d", mi.outbox())
	}
	// an empty comment queues nothing; esc queues nothing
	m = press(press(m, "m"), "enter")
	m = press(press(m, "m"), "esc")
	if mi.outbox() != 2 {
		t.Fatalf("outbox = %d after empty comments", mi.outbox())
	}
	// the shown detail has the queued comment
	d, _ := mi.sess.Detail("app#5")
	if len(d.Pending) != 1 || !strings.Contains(d.Pending[0], "on it") {
		t.Fatalf("pending: %v", d.Pending)
	}
	// closed issue can be reopened from the closed filter
	m = press(m, "f", "enter", "esc") // closed list
	eq(t, "closed list shows the queued close", refs(m), "app#7 app#6 app#1")
	m = press(m, "x")
	if mi.outbox() != 3 || !strings.Contains(m.msg, "queued reopen") {
		t.Fatalf("outbox %d %q", mi.outbox(), m.msg)
	}
}

func TestEditorDoneQueuesComment(t *testing.T) {
	mi := newMirror(t, base)
	m := mi.model(120, 36)
	f := t.TempDir() + "/c.md"
	if err := writeFile(f, "from the editor\n"); err != nil {
		t.Fatal(err)
	}
	m = send(m, editorDoneMsg{ref: "app#7", path: f})
	if mi.outbox() != 1 {
		t.Fatalf("outbox = %d", mi.outbox())
	}
	m = send(m, editorDoneMsg{ref: "app#7", path: f}) // file is gone now: nothing new queued
	if mi.outbox() != 1 || !strings.Contains(m.msg, "canceled") {
		t.Fatalf("outbox %d msg %q", mi.outbox(), m.msg)
	}
}

func TestEmptyAndMissingMirror(t *testing.T) {
	// missing: config but nothing pulled
	mi := newMirror(t, nil)
	m := mi.model(80, 24)
	v := m.View()
	for _, want := range []string{"No issue mirror found", "moon issues pull"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing mirror view lacks %q:\n%s", want, v)
		}
	}
	m = press(m, "j", "k", "g", "G", "enter", "c", "x", "s", "r", "tab", "/", "esc", "?", "esc") // no panic
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Error("q must quit")
	}
	// empty: index exists with zero rows
	mi2 := newMirror(t, []fx{})
	mi2.addIssues("app", nil)
	m2 := mi2.model(80, 24)
	v = m2.View()
	if !strings.Contains(v, "no issues yet") || strings.Contains(v, "panic") {
		t.Errorf("empty view:\n%s", v)
	}
	m2 = press(m2, "j", "G", "enter", "c", "u", "x", "m", "esc", "s", "r", "f", "esc")
	_ = m2.View()
	golden(t, "empty_80", v)
}

func TestHostileTextNeverBreaksTheView(t *testing.T) {
	long := strings.Repeat("x", 5000)
	cjk := strings.Repeat("日本語のテキスト", 200)
	items := []fx{{1, "open", "v1", "bug", "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z", "esc \x1b[31mred\x1b[0m 日本語 \t tab " + long, long + "\n" + cjk + "\n\x1b]0;pwned\x07\n" + "e\u0301" + strings.Repeat("🙂", 300)}}
	m0 := newMirror(t, items).model(120, 36)
	for _, w := range []int{120, 99, 80, 59, 50, 30, 12, 5} {
		for _, h := range []int{40, 12, 5, 2, 1} {
			m := send(m0, tea.WindowSizeMsg{Width: w, Height: h})
			for _, step := range []Model{m, press(m, "enter"), press(m, "f"), press(m, "?"), press(m, "/")} {
				v := step.View()
				if strings.ContainsAny(v, "\x1b\x07") && lipgloss.ColorProfile() == termenv.Ascii {
					t.Fatalf("w=%d h=%d: escape sequence leaked:\n%q", w, h, v)
				}
				for i, l := range strings.Split(v, "\n") {
					if lipgloss.Width(l) > w {
						t.Fatalf("w=%d h=%d line %d is %d wide: %q", w, h, i, lipgloss.Width(l), l)
					}
				}
			}
		}
	}
}

func TestResizeAndLayouts(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	for _, c := range []struct {
		w      int
		layout string
	}{{120, "side"}, {100, "side"}, {99, "stack"}, {60, "stack"}, {59, "list"}, {20, "list"}} {
		m = send(m, tea.WindowSizeMsg{Width: c.w, Height: 30})
		if l, _, _, _, _, _ := m.geometry(); l != c.layout {
			t.Errorf("w=%d layout %s, want %s", c.w, l, c.layout)
		}
	}
	// under 60 columns enter opens the detail full screen, esc returns
	m = send(m, tea.WindowSizeMsg{Width: 50, Height: 20})
	if strings.Contains(m.View(), "milestone:") {
		t.Fatal("detail visible in the list-only layout")
	}
	m = press(m, "enter")
	if !strings.Contains(m.View(), "milestone:") {
		t.Fatalf("no detail:\n%s", m.View())
	}
	m = press(m, "esc")
	if m.full {
		t.Fatal("esc must go back")
	}
}

func TestMouse(t *testing.T) {
	m := newMirror(t, base).model(120, 36)
	wheel := func(m Model, b tea.MouseButton, x, y int) Model {
		return send(m, tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
	}
	m = wheel(m, tea.MouseButtonWheelDown, 5, 5)
	eq(t, "wheel down in the list", m.cur().Ref, "app#3") // 3 rows: 7 -> 3
	m = wheel(m, tea.MouseButtonWheelUp, 5, 5)
	eq(t, "wheel up", m.cur().Ref, "app#7")
	m = wheel(m, tea.MouseButtonLeft, 5, 3) // row 3 is the 3rd item (header is row 0)
	eq(t, "click selects", m.cur().Ref, "app#4")
	m = wheel(m, tea.MouseButtonLeft, 5, 0) // header row: nothing
	eq(t, "header click ignored", m.cur().Ref, "app#4")
	// wheel over the detail pane scrolls the detail, not the list
	long := []fx{{1, "open", "", "", "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z", "t", strings.Repeat("line\n\n", 100)}}
	m = newMirror(t, long).model(120, 20)
	m = wheel(m, tea.MouseButtonWheelDown, 100, 5)
	if m.dtop != 3 || m.cursor != 0 {
		t.Fatalf("dtop=%d cursor=%d", m.dtop, m.cursor)
	}
}

func TestHelpMentionsNoSync(t *testing.T) {
	m := press(newMirror(t, base).model(100, 40), "?")
	v := m.View()
	for _, want := range []string{"no push, pull or sync", "moon issues sync", "j k", "never forced"} {
		if !strings.Contains(v, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	m = press(m, "x")
	if m.mode != modeList {
		t.Fatal("any key closes help")
	}
}

func TestMultiRepoTab(t *testing.T) {
	m := newMirror(t, base, "app", "lib").model(120, 36)
	eq(t, "all aliases", refs(m), "app#7 lib#7 app#5 lib#5 app#4 lib#4 app#3 lib#3 app#2 lib#2")
	m = press(m, "tab")
	eq(t, "app only", refs(m), "app#7 app#5 app#4 app#3 app#2")
	m = press(m, "tab")
	eq(t, "lib only", refs(m), "lib#7 lib#5 lib#4 lib#3 lib#2")
	m = press(m, "tab")
	eq(t, "back to all", strings.Fields(refs(m))[1], "lib#7")
}

// ---------- goldens ----------

func TestGoldenViews(t *testing.T) {
	mi := newMirror(t, base)
	mi.claim("app#2", "robin", 600, 7200)
	for _, c := range []struct {
		name string
		w, h int
	}{{"list_120", 120, 30}, {"list_80", 80, 30}, {"list_50", 50, 20}} {
		m := mi.model(c.w, c.h)
		m = press(m, "j") // select app#5, the one with wide characters
		golden(t, c.name, m.View())
	}
	m := press(mi.model(50, 20), "j", "j", "j", "enter") // app#2: has a claim and comments
	golden(t, "detail_50", m.View())
	m = press(mi.model(120, 40), "?")
	golden(t, "help_120", m.View())
}

// ---------- performance ----------

type bigStore struct{ snap issues.Snapshot }

func (b bigStore) Load() issues.Snapshot { return b.snap }
func (b bigStore) Detail(ref string) (issues.Detail, error) {
	return issues.Detail{Ref: ref, Title: "t", State: "open", Body: strings.Repeat("body line\n", 40)}, nil
}
func (bigStore) Claim(string, string) (string, error)   { return "", nil }
func (bigStore) Release(string, string) (string, error) { return "", nil }
func (bigStore) Close(string, string) error             { return nil }
func (bigStore) Reopen(string) error                    { return nil }
func (bigStore) Comment(string, string) error           { return nil }
func (bigStore) Milestones() []issues.MilestoneRow      { return nil }
func (bigStore) SetMilestone(string, string) error      { return nil }
func (bigStore) Label(string, []string, []string) error { return nil }

func TestThousandIssuesStayInstant(t *testing.T) {
	var snap issues.Snapshot
	snap.Aliases = []string{"app"}
	for i := 1; i <= 1000; i++ {
		st := "open"
		if i%4 == 0 {
			st = "closed"
		}
		snap.Items = append(snap.Items, issues.Item{Ref: fmt.Sprintf("app#%d", i), Alias: "app", Num: i, State: st, Milestone: fmt.Sprintf("v%d", i%5),
			Labels: []string{fmt.Sprintf("area::m%d", i%17), "kind::bug"}, Updated: fmt.Sprintf("2026-02-%02dT%02d:00:00Z", 1+i%28, i%24),
			Created: fmt.Sprintf("2026-01-%02dT00:00:00Z", 1+i%28), Title: fmt.Sprintf("module %d: something is wrong with the thing number %d", i%37, i)})
	}
	m := send(New(bigStore{snap}, Options{Agent: "me"}), tea.WindowSizeMsg{Width: 120, Height: 36})
	var worst time.Duration
	step := func(name string, fn func(Model) Model) Model {
		t0 := time.Now()
		m = fn(m)
		_ = m.View()
		if d := time.Since(t0); d > worst {
			worst = d
		}
		_ = name
		return m
	}
	for _, k := range []string{"/", "w", "i", "d", "g", "e", "t", " ", "1", "2", "esc", "s", "s", "s", "r", "G", "g", "f"} {
		k := k
		step(k, func(m Model) Model { return press(m, k) })
	}
	t.Logf("worst keystroke (update + view), 1000 issues: %v", worst)
	if worst > 50*time.Millisecond {
		t.Fatalf("a keystroke took %v, want < 50ms", worst)
	}
}
