package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/your-moon/moon/internal/issues"
)

// ClaimsStore is what the Claims tab reads. *issues.Session implements it.
type ClaimsStore interface {
	Claims() []issues.ClaimRow
}

// claimsTab is the read-only list of claims, the same data as `moon issues claims`.
type claimsTab struct {
	st      ClaimsStore
	rows    []issues.ClaimRow
	cur     int
	top     int
	w, h    int
	quitKey bool
}

// NewClaimsTab lists claims with age and TTL; stale ones are dimmed.
func NewClaimsTab(st ClaimsStore) Tab {
	return &claimsTab{st: st, rows: st.Claims()}
}

func (t *claimsTab) Title() string   { return "Claims" }
func (t *claimsTab) Init() tea.Cmd   { return nil }
func (t *claimsTab) Keys() string    { return "j/k move  r reload  q quit" }
func (t *claimsTab) Capturing() bool { return false }

func (t *claimsTab) vis() int {
	if v := t.h - footerLines - 1; v > 1 {
		return v
	}
	return 1
}

func (t *claimsTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	n := *t
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		n.w, n.h = msg.Width, msg.Height
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown {
			n.cur += 3
		} else if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp {
			n.cur -= 3
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			n.cur++
		case "k", "up":
			n.cur--
		case "g", "home":
			n.cur = 0
		case "G", "end":
			n.cur = len(n.rows) - 1
		case "r":
			n.rows = n.st.Claims()
		case "q", "ctrl+c":
			return &n, tea.Quit
		}
	}
	if n.cur >= len(n.rows) {
		n.cur = len(n.rows) - 1
	}
	if n.cur < 0 {
		n.cur = 0
	}
	if n.cur < n.top {
		n.top = n.cur
	}
	if n.cur >= n.top+n.vis() {
		n.top = n.cur - n.vis() + 1
	}
	return &n, nil
}

func (t *claimsTab) View() string {
	if t.w <= 0 || t.h <= 0 {
		return ""
	}
	refW, agW, hoW := 3, 5, 4
	for _, r := range t.rows {
		refW = max(refW, len(r.Ref))
		agW = max(agW, len(oneLine(r.Agent)))
		hoW = max(hoW, len(oneLine(r.Host)))
	}
	agW, hoW = min(agW, 20), min(hoW, 20)
	line := func(mark, ref, st, ag, ho, pid, ageS, ttl string) string {
		return fmt.Sprintf("%s %s %s %s %s %s %s %s", mark, fit(ref, refW), fit(st, 5), fit(ag, agW), fit(ho, hoW), fit(pid, 7), fit(ageS, 6), fit(ttl, 6))
	}
	lines := []string{stHead.Render(fit(line(" ", "REF", "STATE", "AGENT", "HOST", "PID", "AGE", "TTL"), t.w))}
	if len(t.rows) == 0 {
		lines = append(lines, "", "  No claims. Claim an issue with c in the Issues tab.")
	}
	stale := 0
	for i := t.top; i < len(t.rows) && i < t.top+t.vis(); i++ {
		r := t.rows[i]
		pid, st := "-", "fresh"
		if r.Pid != 0 {
			pid = fmt.Sprint(r.Pid)
		}
		if r.Stale {
			st = "STALE"
		}
		mark := " "
		if i == t.cur {
			mark = ">"
		}
		s := fit(line(mark, r.Ref, st, oneLine(r.Agent), oneLine(r.Host), pid, age(r.AgeSec), age(r.TTLSec)), t.w)
		switch {
		case i == t.cur:
			s = stSel.Render(s)
		case r.Stale:
			s = stDim.Render(s)
		}
		lines = append(lines, s)
	}
	for _, r := range t.rows {
		if r.Stale {
			stale++
		}
	}
	body := t.h - footerLines
	for len(lines) < body {
		lines = append(lines, "")
	}
	lines = lines[:max(body, 1)]
	lines = append(lines, stDim.Render(fit(t.Keys(), t.w)), fit(fmt.Sprintf("%d claims, %d stale", len(t.rows), stale), t.w))
	for i, l := range lines {
		if lipgloss.Width(l) > t.w {
			l = ansi.Truncate(l, t.w, "")
		}
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}
