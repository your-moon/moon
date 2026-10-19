package tui

import (
	"strings"
	"testing"
)

// invented data: one repo "erp" so the candidate lists are short and exact
var compItems = []fx{
	{2, "open", "v1", "kind:bug,area::auth", "2026-02-20T00:00:00Z", "2026-01-10T00:00:00Z", "auth: token refresh loops", ""},
	{3, "open", "v1 beta", "kind:feature", "2026-02-10T00:00:00Z", "2026-02-01T00:00:00Z", "export: add CSV download", ""},
	{50, "open", "", "docs", "2026-02-25T00:00:00Z", "2026-02-03T00:00:00Z", "docs: explain config lookup", ""},
	{51, "open", "v1", "kind:bug", "2026-02-27T00:00:00Z", "2026-02-05T00:00:00Z", "widget: renders blank", ""},
	{52, "closed", "", "", "2026-01-30T00:00:00Z", "2026-01-20T00:00:00Z", "cleanup: remove dead flags", ""},
	{510, "open", "", "kind:feature", "2026-02-28T00:00:00Z", "2026-02-06T00:00:00Z", "search: add filters", ""},
}

func compModel(t *testing.T, w, h int) (Model, *mirror) {
	mi := newMirror(t, compItems, "erp")
	mi.milestones("erp", "v1\topen\t2\t1\t", "v1 beta\topen\t0\t1\t")
	mi.claim("erp#2", "robin", 600, 7200)
	return mi.model(w, h), mi
}

func cands(m Model) string {
	var out []string
	for _, c := range m.comp.cands {
		out = append(out, c.Value)
	}
	return strings.Join(out, " ")
}

func TestTabOneMatchCompletesFully(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(m, "/")
	m = typed(m, "label:ar")
	m = press(m, "tab")
	eq(t, "input", m.input.Value(), "label:area::auth")
	eq(t, "query follows", m.query, "label:area::auth")
	if m.comp.open {
		t.Error("one match must not open the list")
	}
	eq(t, "filter applied", refs(m), "erp#2")
	// a ref
	m = press(m, "esc", "/")
	m = typed(m, "erp#3")
	m = press(m, "tab")
	eq(t, "ref", m.input.Value(), "erp#3")
}

func TestTabSeveralMatchesCommonPrefixThenCycles(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = typed(press(m, "/"), "ki")
	m = press(m, "tab")
	eq(t, "common prefix", m.input.Value(), "kind:")
	if !m.comp.open || cands(m) != "kind:bug kind:feature" {
		t.Fatalf("list: open=%v %q", m.comp.open, cands(m))
	}
	m = press(m, "tab")
	eq(t, "first cycle", m.input.Value(), "kind:bug")
	m = press(m, "tab")
	eq(t, "second cycle", m.input.Value(), "kind:feature")
	m = press(m, "tab")
	eq(t, "wraps", m.input.Value(), "kind:bug")
	eq(t, "query tracks the text", m.query, "kind:bug")

	// refs: erp#5 offers erp#5xx, newest first
	m = press(typed(press(m, "esc", "esc", "/"), "erp#5"), "tab")
	eq(t, "no progress possible", m.input.Value(), "erp#5")
	eq(t, "ref candidates, newest first", cands(m), "erp#510 erp#51 erp#50 erp#52")
	m = press(m, "tab")
	eq(t, "cycled into the first", m.input.Value(), "erp#510")
}

func TestTypedRefNarrowsToThatRef(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	eq(t, "prefix", refs(typed(press(m, "/"), "erp#5")), "erp#510 erp#51 erp#50")
	eq(t, "exact", refs(typed(press(m, "/"), "erp#510")), "erp#510")
}

func TestShiftTabGoesBack(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, "/"), "erp#5"), "tab", "tab", "tab")
	eq(t, "two forward", m.input.Value(), "erp#51")
	m = send(m, shiftTab())
	eq(t, "one back", m.input.Value(), "erp#510")
	m = send(m, shiftTab())
	eq(t, "wraps backwards", m.input.Value(), "erp#52")
}

func TestEscClosesListThenPrompt(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, "/"), "erp#5"), "tab", "tab")
	m = press(m, "esc")
	if m.comp.open || m.mode != modeSearch {
		t.Fatalf("esc closes only the list: open=%v mode=%v", m.comp.open, m.mode)
	}
	eq(t, "text kept", m.input.Value(), "erp#510")
	m = press(m, "esc")
	if m.mode != modeList || m.query != "" {
		t.Fatalf("second esc leaves search and clears: mode=%v q=%q", m.mode, m.query)
	}
	// typing closes the list too
	m = press(typed(press(m, "/"), "erp#5"), "tab")
	m = typed(m, "1")
	if m.comp.open {
		t.Error("a typed key closes the list")
	}
}

func TestFilterTokensInSearch(t *testing.T) {
	m, _ := compModel(t, 120, 40)
	run := func(q string) string {
		m2 := typed(press(m, "/"), q)
		return refs(m2)
	}
	eq(t, "label stored name", run("label:kind:bug"), "erp#51 erp#2")
	eq(t, "label short name", run("label:bug"), "erp#51 erp#2")
	eq(t, "milestone quoted", run(`milestone:"v1 beta"`), "erp#3")
	eq(t, "milestone", run("milestone:v1"), "erp#51 erp#2")
	eq(t, "status working", run("status:working"), "erp#2")
	eq(t, "status open", run("status:open"), "erp#510 erp#51 erp#50 erp#3")
	eq(t, "status list", run("status:working,open"), "erp#510 erp#51 erp#50 erp#3 erp#2")
	eq(t, "status in-review spelling", run("status:in-review"), "")
	eq(t, "status quoted", run(`status:"in review"`), "")
	eq(t, "repo", run("repo:erp"), "erp#510 erp#51 erp#50 erp#3 erp#2")
	eq(t, "repo other", run("repo:web"), "")
	eq(t, "token plus free text", run("label:bug widget"), "erp#51")
	eq(t, "plain text unchanged", run("kind:bug"), "erp#51 erp#2") // not a token: substring of the labels, as ls -q
	// completion of values
	m2 := press(typed(press(m, "/"), `milestone:"v1 b`), "tab")
	eq(t, "quoted milestone completes", m2.input.Value(), `milestone:"v1 beta"`)
	m2 = press(typed(press(m, "/"), "status:wo"), "tab")
	eq(t, "status value", m2.input.Value(), "status:working")
	m2 = press(typed(press(m, "/"), "status:in"), "tab")
	eq(t, "status with a space is quoted", m2.input.Value(), `status:"in review"`)
	m2 = press(typed(press(m, "/"), "sta"), "tab")
	eq(t, "token name", m2.input.Value(), "status:")
	m2 = press(typed(press(m, "/"), "repo:"), "tab")
	eq(t, "alias", m2.input.Value(), "repo:erp")
}

func TestColonClaimOffersOnlyUnclaimed(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, ":"), "claim erp#"), "tab")
	eq(t, "claimable: open and not held, erp#2 is held", cands(m), "erp#510 erp#51 erp#50 erp#3")
	m = press(typed(press(m, "esc", "esc", ":"), "release erp#"), "tab")
	eq(t, "releasable: robin holds erp#2, nothing is mine", m.input.Value(), "release erp#")
	m = press(typed(press(m, "esc", ":"), "reopen "), "tab")
	eq(t, "reopen: only closed", m.input.Value(), "reopen erp#52")
	m = press(typed(press(m, "esc", ":"), "cl"), "tab")
	eq(t, "commands", cands(m), "claim close")
}

func TestColonArgumentsAndCommands(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, ":"), "sort t"), "tab")
	eq(t, "sort value", m.input.Value(), "sort title")
	m = press(typed(press(m, "esc", ":"), "repo e"), "tab")
	eq(t, "repo alias", m.input.Value(), "repo erp")
	m = press(typed(press(m, "esc", ":"), `milestone erp#3 "v1 b`), "tab")
	eq(t, "milestone with a space is quoted", m.input.Value(), `milestone erp#3 "v1 beta"`)
	m = press(typed(press(m, "esc", ":"), "label erp#2 remove ar"), "tab")
	eq(t, "labels of that issue only", m.input.Value(), "label erp#2 remove area::auth")
}

func TestColonRunsThroughTheWritePath(t *testing.T) {
	m, mi := compModel(t, 120, 30)
	m = press(typed(press(m, ":"), "claim erp#51"), "enter")
	if !strings.Contains(m.msg, "erp#51") || m.mode != modeList {
		t.Fatalf("claim: mode=%v msg=%q", m.mode, m.msg)
	}
	m = press(typed(press(m, ":"), "close erp#50"), "enter")
	m = press(typed(press(m, ":"), "milestone erp#3 v1"), "enter")
	m = press(typed(press(m, ":"), "label erp#3 add urgent"), "enter")
	m = press(typed(press(m, ":"), "comment erp#3 on it"), "enter")
	if n := mi.outbox(); n != 4 {
		t.Fatalf("outbox has %d ops, want 4 (close, milestone, label, comment): %q", n, m.msg)
	}
	if !strings.Contains(m.View(), "queued: 4") {
		t.Errorf("footer lacks the queued count:\n%s", m.View())
	}
	eq(t, "closed issue left the open list", refs(m), "erp#510 erp#51 erp#3 erp#2")
}

func TestColonErrorsShowInFooter(t *testing.T) {
	m, mi := compModel(t, 120, 30)
	for _, c := range []struct{ line, want string }{
		{"claim erp#2", "held by robin"},
		{"claim nope#9", "unknown ref nope#9"},
		{"claim", "needs an issue"},
		{"bogus", "unknown command :bogus"},
		{"release erp#3", "has no claim"},
		{"close erp#52", "already closed"},
		{"reopen erp#3", "not closed"},
		{"sort nonsense", "unknown sort"},
		{"filter state:weird", "state must be"},
		{"repo zzz", "unknown repo zzz"},
		{"label erp#3 add", "usage: :label"},
	} {
		m2 := press(typed(press(m, ":"), c.line), "enter")
		if !strings.Contains(m2.msg, c.want) || m2.mode != modeList {
			t.Errorf(":%s: msg %q mode %v, want %q", c.line, m2.msg, m2.mode, c.want)
		}
		if !strings.Contains(m2.View(), c.want) {
			t.Errorf(":%s: footer does not show %q", c.line, c.want)
		}
	}
	if mi.outbox() != 0 {
		t.Errorf("failed commands wrote %d ops", mi.outbox())
	}
}

func TestColonSortFilterRepoQuit(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, ":"), "sort title"), "enter")
	eq(t, "sorted by title", refs(m), "erp#2 erp#50 erp#3 erp#510 erp#51")
	m = press(typed(press(m, ":"), "sort reverse"), "enter")
	eq(t, "reversed", refs(m), "erp#51 erp#510 erp#3 erp#50 erp#2")
	m = press(typed(press(m, ":"), "filter state:all label:feature"), "enter")
	eq(t, "filter", m.fState+" "+m.fLabel, "all feature")
	m = press(typed(press(m, ":"), "filter clear"), "enter")
	eq(t, "cleared", m.fState+m.fLabel, "open")
	_, cmd := m.Update(keyMsg(":"))
	_ = cmd
	m = typed(press(m, ":"), "q")
	if _, c := m.Update(keyMsg("enter")); c == nil {
		t.Error(":q must quit")
	}
}

func TestColonHistory(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, ":"), "sort title"), "enter")
	m = press(typed(press(m, ":"), "repo erp"), "enter")
	m = press(m, ":", "up")
	eq(t, "newest first", m.input.Value(), "repo erp")
	m = press(m, "up")
	eq(t, "older", m.input.Value(), "sort title")
	m = press(m, "up")
	eq(t, "stops at the oldest", m.input.Value(), "sort title")
	m = press(m, "down")
	eq(t, "forward", m.input.Value(), "repo erp")
	m = press(m, "down")
	eq(t, "past the newest is empty", m.input.Value(), "")
	// history survives leaving the line
	m = press(m, "esc", ":", "up")
	eq(t, "kept across sessions of the line", m.input.Value(), "repo erp")
}

func TestPickerAndMenuUseTheEngine(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(m, "j", "M") // erp#51 is selected; picker lists the engine's milestones
	eq(t, "options", strings.Join(m.pickOpts, "|"), "|v1|v1 beta")
	m = press(typed(m, "v"), "tab")
	eq(t, "common prefix", m.input.Value(), "v1")
	m = press(m, "tab")
	eq(t, "cycles to the second row", strings.Join(m.pickMatches(), "|")+"@"+itoa(m.pickCur), "v1|v1 beta@1")
	m = send(m, shiftTab())
	eq(t, "back", itoa(m.pickCur), "0")
	// the f menu: / types to narrow, tab completes
	m = press(m, "esc", "f", "j", "enter") // label list
	m = press(m, "/")
	m = typed(m, "ki")
	m = press(m, "tab")
	eq(t, "typed text completes", m.menuTyped, "kind:")
	m = press(m, "tab", "enter")
	eq(t, "tab cycles then enter picks", m.fLabel, "kind:feature")
}

func TestCandidateListGoldens(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
	}{{"comp_search_120", 120, 24}, {"comp_search_50", 50, 20}} {
		m, _ := compModel(t, c.w, c.h)
		m = press(typed(press(m, "/"), "erp#5"), "tab", "tab")
		golden(t, c.name, m.View())
	}
	for _, c := range []struct {
		name string
		w, h int
	}{{"comp_line_120", 120, 24}, {"comp_line_50", 50, 20}} {
		m, _ := compModel(t, c.w, c.h)
		m = press(typed(press(m, ":"), "claim "), "tab")
		golden(t, c.name, m.View())
	}
}
