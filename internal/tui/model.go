// Package tui is an interactive terminal browser for the local issue mirror.
//
// The model is one struct with an explicit Update; View is a pure function of the model.
// Writes only queue local operations through the same code path as the CLI (outbox and
// claims). Nothing here pushes, pulls or syncs.
package tui

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/completion"
	"github.com/your-moon/moon/internal/issues"
)

// Store is what the model needs from the mirror. *issues.Session implements it.
type Store interface {
	Load() issues.Snapshot
	Detail(ref string) (issues.Detail, error)
	Milestones() []issues.MilestoneRow
	// Do runs an issue action (internal/issues/actions.go) as agent and returns the footer message.
	Do(action, ref, agent string, args []string) (string, error)
}

// Options configure a model.
type Options struct {
	Agent     string // claim holder name
	Editor    string // $EDITOR; empty falls back to a one-line prompt
	MirrorDir string // shown when the mirror is missing
	// Scope is the project scope: a single repo preselects its tab (a toggles all repos) and the footer names it.
	// The zero value means no scope.
	Scope issues.Scope
}

type sortKey int

const (
	sortNumber sortKey = iota
	sortUpdated
	sortCreated
	sortTitle
	sortStatus
)

var sortNames = [...]string{"number", "updated", "created", "title", "status"}

type mode int

const (
	modeList mode = iota
	modeSearch
	modePrompt
	modeMenu
	modeHelp
	modePick // milestone picker
	modeCmd  // the : command line
)

// noMilestone is the filter value for issues without a milestone.
const noMilestone = "\x00none"

type menuKind int

const (
	menuTop menuKind = iota
	menuLabel
	menuMilestone
)

const (
	wideCols   = 100 // at or above: list and detail side by side
	narrowCols = 60  // below: list only, enter opens the detail
)

type entry struct {
	it       issues.Item
	lowTitle string
	lowLabel string // comma-joined, like the index column
}

// Model is the whole UI state.
type Model struct {
	st   Store
	opt  Options
	snap issues.Snapshot
	all  []entry
	rows []*entry // visible, ordered

	cursor, top int
	selRef      string

	mode    mode
	query   string
	input   textinput.Model
	sortBy  sortKey
	reverse bool

	fState, fLabel, fMilestone, fAlias string
	fWorking                           bool             // w: only issues with status working
	labelMode                          string           // "" = by terminal width; else issues.LabelsFull, LabelsShort or LabelsNone
	pq                                 completion.Query // the search box split into filter tokens

	comp    compState // open Tab candidate list
	hist    []string  // : command history, this session
	histPos int

	menuTyped  string // type-ahead in the label and milestone lists
	menuTyping bool

	prevState string // state filter before a milestone drill-in
	drilled   bool

	pickOpts []string // milestone picker: titles, "" first for none
	pickCur  int

	menu     menuKind
	menuCur  int
	menuOpts []string

	full bool // detail shown full screen
	det  issues.Detail
	derr string
	dtop int

	w, h int
	msg  string
	now  func() time.Time
}

// New builds a model and loads the snapshot.
func New(st Store, opt Options) Model {
	in := textinput.New()
	in.CharLimit = 0
	m := Model{st: st, opt: opt, input: in, fState: "open", now: time.Now}
	m.fAlias = m.scopeAlias()
	m.reload()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

type editorDoneMsg struct {
	ref, path string
	err       error
}

// ---------- data ----------

func (m *Model) reload() {
	m.snap = m.st.Load()
	m.all = make([]entry, 0, len(m.snap.Items))
	for _, it := range m.snap.Items {
		m.all = append(m.all, entry{it: it, lowTitle: strings.ToLower(it.Title), lowLabel: strings.ToLower(strings.Join(it.Labels, ","))})
	}
	m.refilter()
}

func (m *Model) matches(e *entry) bool {
	it := &e.it
	if m.fState != "all" && it.State != m.fState {
		return false
	}
	if m.fAlias != "" && it.Alias != m.fAlias {
		return false
	}
	if m.fWorking && it.Work.Name != issues.StWorking {
		return false
	}
	if m.fMilestone == noMilestone {
		if it.Milestone != "" {
			return false
		}
	} else if m.fMilestone != "" && it.Milestone != m.fMilestone {
		return false
	}
	if m.fLabel != "" {
		ok := false
		for _, l := range it.Labels {
			if l == m.fLabel {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	text := m.query
	if m.pq.HasFilters {
		if !m.pq.Match(it) {
			return false
		}
		text = m.pq.Text
	}
	if q := strings.ToLower(text); q != "" && !strings.Contains(e.lowTitle, q) && !strings.Contains(e.lowLabel, q) &&
		!(strings.Contains(q, "#") && !strings.ContainsAny(q, " \t") && strings.HasPrefix(strings.ToLower(it.Ref), q)) { // a ref typed or completed
		return false
	}
	return true
}

func numKey(it *issues.Item) int {
	if it.Num == 0 {
		return 1 << 30 // local, not pushed yet: newest
	}
	return it.Num
}

// before is the natural order of a sort key: newest first, titles A to Z.
func (m *Model) before(a, b *entry) bool {
	x, y := &a.it, &b.it
	var c int
	switch m.sortBy {
	case sortUpdated:
		c = -strings.Compare(x.Updated, y.Updated)
	case sortCreated:
		c = -strings.Compare(x.Created, y.Created)
	case sortTitle:
		c = strings.Compare(a.lowTitle, b.lowTitle)
	case sortStatus:
		c = issues.StatusRank(x.Work.Name) - issues.StatusRank(y.Work.Name)
	}
	if c == 0 {
		switch nx, ny := numKey(x), numKey(y); {
		case nx != ny:
			c = ny - nx
		default:
			c = strings.Compare(x.Ref, y.Ref)
		}
	}
	if m.reverse {
		return c > 0
	}
	return c < 0
}

func (m *Model) refilter() {
	prev := m.cursor
	m.pq = completion.ParseQuery(m.query)
	m.rows = make([]*entry, 0, len(m.all))
	for i := range m.all {
		if m.matches(&m.all[i]) {
			m.rows = append(m.rows, &m.all[i])
		}
	}
	sort.SliceStable(m.rows, func(i, j int) bool { return m.before(m.rows[i], m.rows[j]) })
	m.cursor = -1
	for i, e := range m.rows {
		if e.it.Ref == m.selRef {
			m.cursor = i
			break
		}
	}
	if m.cursor < 0 {
		m.cursor = prev
	}
	m.clamp()
	m.syncSel()
}

func (m *Model) clamp() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	_, _, listH, _, _, _ := m.geometry()
	vis := listH - 1
	if vis < 1 {
		vis = 1
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+vis {
		m.top = m.cursor - vis + 1
	}
	if max := len(m.rows) - vis; m.top > max {
		m.top = max
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *Model) cur() *issues.Item {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor].it
}

// syncSel remembers the selected ref and loads its detail when it changed.
func (m *Model) syncSel() {
	it := m.cur()
	if it == nil {
		m.selRef, m.det, m.derr, m.dtop = "", issues.Detail{}, "", 0
		return
	}
	if it.Ref != m.det.Ref || it.Ref != m.selRef {
		m.dtop = 0
	}
	m.selRef = it.Ref
	d, err := m.st.Detail(it.Ref)
	m.det, m.derr = d, ""
	if err != nil {
		m.derr = err.Error()
	}
}

func (m *Model) move(delta int) {
	m.cursor += delta
	m.clamp()
	m.syncSel()
}

func (m *Model) moveTo(i int) {
	m.cursor = i
	m.clamp()
	m.syncSel()
}

// ---------- geometry ----------

const footerLines = 2

// geometry returns the layout: "list", "stack" or "side", the pane sizes, and the first
// row of the detail pane (stack) or its first column (side).
func (m *Model) geometry() (layout string, listW, listH, detW, detH, detOrigin int) {
	body := m.h - footerLines
	if body < 1 {
		body = 1
	}
	switch {
	case m.w < narrowCols:
		return "list", m.w, body, m.w, body, 0
	case m.w < wideCols:
		lh := (body - 1) * 2 / 5
		if lh < 3 {
			lh = 3
		}
		if lh > body-2 {
			lh = body - 2
		}
		if lh < 1 {
			lh = 1
		}
		dh := body - 1 - lh
		if dh < 0 {
			dh = 0
		}
		return "stack", m.w, lh, m.w, dh, lh + 1
	}
	lw := m.w * 60 / 100
	return "side", lw, body, m.w - lw - 1, body, lw + 1
}

// ---------- update ----------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clamp()
		return m, nil
	case tea.MouseMsg:
		return m.mouse(msg), nil
	case editorDoneMsg:
		return m.editorDone(msg), nil
	case drillMsg:
		return m.drill(msg), nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeSearch:
		return m.keySearch(k), nil
	case modePrompt:
		return m.keyPrompt(k), nil
	case modeCmd:
		return m.keyCmd(k)
	case modeMenu:
		return m.keyMenu(s), nil
	case modePick:
		return m.keyPick(k), nil
	case modeHelp:
		m.mode = modeList
		return m, nil
	}
	m.msg = ""
	_, _, listH, _, detH, _ := m.geometry()
	page := listH - 2
	if page < 1 {
		page = 1
	}
	if m.full { // full-screen detail: movement scrolls the text
		switch s {
		case "esc", "backspace", "h", "left":
			m.full = false
		case "j", "down":
			m.dtop++
		case "k", "up":
			m.dtop--
		case "g", "home":
			m.dtop = 0
		case "G", "end":
			m.dtop = 1 << 30
		case "pgdown", "ctrl+d", " ":
			m.dtop += detH/2 + 1
		case "pgup", "ctrl+u":
			m.dtop -= detH/2 + 1
		default:
			return m.common(s)
		}
		m.clampDetail()
		return m, nil
	}
	switch s {
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.moveTo(0)
	case "G", "end":
		m.moveTo(len(m.rows) - 1)
	case "pgdown":
		m.move(page)
	case "pgup":
		m.move(-page)
	case "ctrl+d":
		m.dtop += detH/2 + 1
		m.clampDetail()
	case "ctrl+u":
		m.dtop -= detH/2 + 1
		m.clampDetail()
	case "enter", "right":
		if m.cur() != nil {
			m.full, m.dtop = true, 0
		}
	case "esc":
		switch {
		case m.query != "":
			m.query = ""
			m.input.SetValue("")
		case m.drilled:
			m.fMilestone, m.fState, m.drilled = "", m.prevState, false
		case m.fMilestone != "" || m.fLabel != "":
			m.fMilestone, m.fLabel = "", ""
		default:
			return m, nil
		}
		m.refilter()
	default:
		return m.common(s)
	}
	return m, nil
}

// common handles the keys that work in the list and in the full-screen detail.
func (m Model) common(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "q":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
	case "/":
		m.mode = modeSearch
		m.comp = compState{}
		m.full = false
		m.input.Prompt = ""
		m.input.SetValue(m.query)
		m.input.CursorEnd()
		m.input.Focus()
	case ":":
		m.startCmd()
	case "s":
		m.sortBy = (m.sortBy + 1) % sortKey(len(sortNames))
		m.refilter()
	case "r":
		m.reverse = !m.reverse
		m.refilter()
	case "w":
		m.fWorking = !m.fWorking
		m.refilter()
	case "l":
		m.labelMode = nextLabelMode(m.effLabelMode())
	case "f":
		m.mode, m.menu, m.menuCur = modeMenu, menuTop, 0
	case "a":
		if m.fAlias != "" {
			m.fAlias = ""
		} else {
			m.fAlias = m.scopeAlias()
		}
		m.refilter()
	case "tab":
		if len(m.snap.Aliases) > 1 {
			m.fAlias = nextAlias(m.snap.Aliases, m.fAlias)
			m.refilter()
		}
	case "c":
		m.claim()
	case "u":
		m.release()
	case "x":
		m.toggleClosed()
	case "m":
		return m.startComment()
	case "M":
		m.startPick()
	}
	return m, nil
}

// drill pre-filters the list to one milestone, as the f menu would, and remembers the old state filter.
func (m Model) drill(d drillMsg) Model {
	if !m.drilled {
		m.prevState = m.fState
	}
	m.drilled, m.fState, m.query = true, "all", ""
	m.input.SetValue("")
	m.fMilestone, m.fLabel, m.mode, m.full = d.Milestone, "", modeList, false
	if d.None {
		m.fMilestone = noMilestone
	}
	if len(m.snap.Aliases) > 1 {
		m.fAlias = d.Alias
	}
	m.refilter()
	return m
}

func (m *Model) startPick() {
	it := m.cur()
	if it == nil {
		return
	}
	m.pickOpts = []string{""}
	for _, c := range completion.MilestoneTitles(m.src(), []string{it.Alias}) { // the engine's list, as the shell offers it
		m.pickOpts = append(m.pickOpts, c.Value)
	}
	m.comp = compState{}
	m.mode, m.pickCur = modePick, 0
	m.input.Prompt = ""
	m.input.SetValue("")
	m.input.Focus()
}

// pickMatches are the picker options that match what was typed. "-" matches the no-milestone row.
func (m *Model) pickMatches() []string {
	q := m.input.Value()
	var out []string
	if q == "" || q == "-" {
		out = append(out, "")
	}
	if q == "-" {
		return out
	}
	for _, c := range completion.Narrow(pickCands(m.pickOpts), q) {
		out = append(out, c.Value)
	}
	return out
}

func pickCands(opts []string) []completion.Candidate {
	var out []completion.Candidate
	for _, o := range opts {
		if o != "" {
			out = append(out, completion.Candidate{Value: o})
		}
	}
	return out
}

// pickTab is Tab in the milestone picker: one prefix match completes the text, several complete to the
// longest common prefix, and the next Tab (or Shift-Tab) cycles the highlighted row.
func (m Model) pickTab(back bool) Model {
	matches := m.pickMatches()
	if m.comp.open && len(matches) > 0 {
		if back {
			m.pickCur = (m.pickCur + len(matches) - 1) % len(matches)
		} else {
			m.pickCur = (m.pickCur + 1) % len(matches)
		}
		return m
	}
	typed := m.input.Value()
	var vals []string
	for _, c := range completion.Narrow(pickCands(m.pickOpts), typed) {
		if strings.HasPrefix(strings.ToLower(c.Value), strings.ToLower(typed)) {
			vals = append(vals, c.Value)
		}
	}
	switch len(vals) {
	case 0:
		return m
	case 1:
		m.input.SetValue(vals[0])
		m.input.CursorEnd()
		m.pickCur = 0
		return m
	}
	if p := completion.CommonPrefix(vals); len(p) > len(typed) {
		m.input.SetValue(p)
		m.input.CursorEnd()
	}
	m.pickCur = 0
	m.comp = compState{open: true}
	return m
}

func (m Model) keyPick(k tea.KeyMsg) Model {
	switch s := k.String(); s {
	case "tab", "shift+tab":
		return m.pickTab(s == "shift+tab")
	case "esc":
		m.comp = compState{}
		m.mode = modeList
		m.input.Blur()
		m.msg = "milestone unchanged"
		return m
	case "up", "ctrl+p":
		m.pickCur--
	case "down", "ctrl+n":
		m.pickCur++
	case "enter":
		opts := m.pickMatches()
		m.mode = modeList
		m.input.Blur()
		it := m.cur()
		if it == nil || m.pickCur < 0 || m.pickCur >= len(opts) {
			m.msg = "milestone unchanged"
			return m
		}
		title := opts[m.pickCur]
		arg := title
		if title == "" {
			arg = "none"
		}
		if m.act("milestone", it.Ref, arg) && title == "" {
			m.msg = "queued milestone (none) on " + it.Ref
		}
		return m
	default:
		m.comp = compState{}
		m.input, _ = m.input.Update(k)
		m.pickCur = 0
	}
	if n := len(m.pickMatches()); m.pickCur >= n {
		m.pickCur = n - 1
	}
	if m.pickCur < 0 {
		m.pickCur = 0
	}
	return m
}

// scopeAlias is the one repo the dashboard opens on ("" when the scope is every repo or several).
func (m Model) scopeAlias() string {
	if s := m.opt.Scope; (s.Kind == issues.KindProject || s.Kind == issues.KindExplicit) && len(s.Repos) == 1 {
		return s.Repos[0]
	}
	return ""
}

// scopeText is the footer part that says what the list covers; empty when no scope was given.
func (m Model) scopeText() string {
	s := m.opt.Scope
	if s.Kind == "" {
		return ""
	}
	switch want := m.scopeAlias(); {
	case m.fAlias == "" && want != "":
		return "scope: all repos (a: back to " + want + ")"
	case m.fAlias == "":
		return "scope: " + s.Label() + " (" + s.Reason + ")"
	case m.fAlias == want:
		return "scope: " + want + " (" + s.Reason + ")"
	}
	return "scope: " + m.fAlias + " (tab)"
}

func nextAlias(aliases []string, cur string) string {
	if cur == "" {
		return aliases[0]
	}
	for i, a := range aliases {
		if a == cur {
			if i+1 < len(aliases) {
				return aliases[i+1]
			}
			return ""
		}
	}
	return ""
}

func (m *Model) clampDetail() {
	_, _, _, detW, detH, _ := m.geometry()
	if m.full {
		detW, detH = m.w, m.h-footerLines
	}
	n := len(m.detailLines(detW))
	if max := n - detH; m.dtop > max {
		m.dtop = max
	}
	if m.dtop < 0 {
		m.dtop = 0
	}
}

func (m Model) keySearch(k tea.KeyMsg) Model {
	switch s := k.String(); s {
	case "tab", "shift+tab":
		m = m.tabComplete(s == "shift+tab", m.searchCompleter())
		m.query = m.input.Value()
		m.refilter()
		return m
	case "esc":
		if m.comp.open {
			m.comp = compState{}
			return m
		}
		m.query = ""
		m.mode = modeList
		m.input.Blur()
		m.input.SetValue("")
	case "enter":
		m.mode = modeList
		m.input.Blur()
	case "up":
		m.comp = compState{}
		m.move(-1)
		return m
	case "down":
		m.comp = compState{}
		m.move(1)
		return m
	default:
		m.comp = compState{}
		m.input, _ = m.input.Update(k)
		m.query = m.input.Value()
	}
	m.refilter()
	return m
}

// menuList is the label or milestone list narrowed by the type-ahead (the engine's Narrow).
func (m Model) menuList() []string {
	if m.menuTyped == "" {
		return m.menuOpts
	}
	var out []string
	for _, c := range completion.Narrow(pickCands(m.menuOpts), m.menuTyped) {
		out = append(out, c.Value)
	}
	return out
}

// menuTab is Tab in the label and milestone lists: complete the typed text like the other prompts do (one
// prefix match fills it in, several fill the common prefix), and once there is nothing more to add, step
// through the rows (Shift-Tab goes back).
func (m Model) menuTab(back bool) Model {
	list := m.menuList()
	var vals []string
	for _, c := range completion.Narrow(pickCands(m.menuOpts), m.menuTyped) {
		if strings.HasPrefix(strings.ToLower(c.Value), strings.ToLower(m.menuTyped)) {
			vals = append(vals, c.Value)
		}
	}
	if !back && m.menuTyping && len(vals) > 0 {
		if p := completion.CommonPrefix(vals); len(p) > len(m.menuTyped) {
			m.menuTyped, m.menuCur = p, 0
			return m
		}
	}
	if len(list) == 0 {
		return m
	}
	if back {
		m.menuCur = (m.menuCur + len(list) - 1) % len(list)
	} else {
		m.menuCur = (m.menuCur + 1) % len(list)
	}
	return m
}

func (m Model) keyMenuTyping(s string) Model {
	switch {
	case s == "esc":
		m.menuTyping, m.menuTyped, m.menuCur = false, "", 0
	case s == "tab" || s == "shift+tab":
		return m.menuTab(s == "shift+tab")
	case s == "enter":
		return m.menuPick()
	case s == "down" || s == "ctrl+n":
		if m.menuCur+1 < len(m.menuList()) {
			m.menuCur++
		}
	case s == "up" || s == "ctrl+p":
		if m.menuCur > 0 {
			m.menuCur--
		}
	case s == "backspace":
		if r := []rune(m.menuTyped); len(r) > 0 {
			m.menuTyped = string(r[:len(r)-1])
		}
		m.menuCur = 0
	case utf8.RuneCountInString(s) == 1:
		m.menuTyped += s
		m.menuCur = 0
	}
	return m
}

func (m Model) keyMenu(s string) Model {
	if m.menu != menuTop && m.menuTyping {
		return m.keyMenuTyping(s)
	}
	n := len(m.menuList())
	if m.menu == menuTop {
		n = 4
	}
	switch s {
	case "/":
		if m.menu != menuTop {
			m.menuTyping, m.menuTyped, m.menuCur = true, "", 0
		}
	case "tab", "shift+tab":
		if m.menu != menuTop {
			return m.menuTab(s == "shift+tab")
		}
	case "esc", "q", "f":
		if m.menu == menuTop {
			m.mode = modeList
		} else {
			m.menu, m.menuCur, m.menuTyped, m.menuTyping = menuTop, 0, "", false
		}
	case "j", "down":
		if m.menuCur+1 < n {
			m.menuCur++
		}
	case "k", "up":
		if m.menuCur > 0 {
			m.menuCur--
		}
	case "enter", " ":
		return m.menuPick()
	}
	return m
}

var stateCycle = []string{"open", "closed", "all"}

func (m Model) menuPick() Model {
	switch m.menu {
	case menuTop:
		switch m.menuCur {
		case 0:
			for i, s := range stateCycle {
				if s == m.fState {
					m.fState = stateCycle[(i+1)%len(stateCycle)]
					break
				}
			}
			m.refilter()
		case 1:
			m.menu, m.menuCur, m.menuTyped, m.menuTyping = menuLabel, 0, "", false
			m.menuOpts = append([]string{""}, issues.SortedLabels(m.snap.Items)...)
		case 2:
			m.menu, m.menuCur, m.menuTyped, m.menuTyping = menuMilestone, 0, "", false
			m.menuOpts = append([]string{""}, issues.SortedMilestones(m.snap.Items)...)
		case 3:
			m.fState, m.fLabel, m.fMilestone, m.fAlias, m.fWorking = "open", "", "", "", false
			m.mode = modeList
			m.refilter()
		}
	case menuLabel, menuMilestone:
		v := ""
		if l := m.menuList(); m.menuCur < len(l) {
			v = l[m.menuCur]
		}
		if m.menu == menuLabel {
			m.fLabel = v
		} else {
			m.fMilestone = v
		}
		m.menu, m.menuCur, m.mode = menuTop, 0, modeList
		m.menuTyped, m.menuTyping = "", false
		m.refilter()
	}
	return m
}

func (m Model) mouse(e tea.MouseMsg) Model {
	if m.mode != modeList {
		return m
	}
	layout, listW, listH, _, detH, origin := m.geometry()
	inList, inDet := false, false
	switch {
	case m.full:
		inDet = e.Y < m.h-footerLines
	case layout == "list":
		inList = e.Y < listH
	case layout == "stack":
		inList, inDet = e.Y < listH, e.Y >= origin && e.Y < origin+detH
	default:
		inList, inDet = e.X < listW && e.Y < listH, e.X >= origin && e.Y < m.h-footerLines
	}
	switch {
	case e.Button == tea.MouseButtonWheelDown && e.Action == tea.MouseActionPress:
		if inDet {
			m.dtop += 3
			m.clampDetail()
		} else if inList {
			m.move(3)
		}
	case e.Button == tea.MouseButtonWheelUp && e.Action == tea.MouseActionPress:
		if inDet {
			m.dtop -= 3
			m.clampDetail()
		} else if inList {
			m.move(-3)
		}
	case e.Button == tea.MouseButtonLeft && e.Action == tea.MouseActionPress && inList:
		if i := m.top + e.Y - 1; e.Y >= 1 && i < len(m.rows) {
			m.moveTo(i)
		}
	}
	return m
}

// ---------- writes (queued locally, never pushed) ----------

func (m *Model) claim() {
	if it := m.cur(); it != nil {
		m.act("claim", it.Ref)
	}
}

func (m *Model) release() {
	if it := m.cur(); it != nil {
		m.act("release", it.Ref)
	}
}

// act runs one issue action on ref as this model's agent: the action's own Allows decides (the same rule
// completion offers by), a refusal or failure becomes the footer message, and the list reloads.
func (m *Model) act(name, ref string, args ...string) bool {
	it := m.itemByRef(ref)
	if it == nil {
		m.msg = "unknown ref " + ref
		return false
	}
	a, _ := issues.ActionNamed(name)
	agent := m.opt.Agent
	if !a.Allows(*it, agent) || (name == "release" && it.Claim.Agent != agent) {
		m.msg = refusal(name, it)
		return false
	}
	out, err := m.st.Do(name, ref, agent, args)
	m.reload()
	if err != nil {
		prefix := map[string]string{"claim": "not claimed: ", "release": "not released: "}[name]
		if prefix == "" {
			prefix = "not queued: "
		}
		m.msg = prefix + firstLine(out, err)
		return false
	}
	m.msg = firstLine(out, nil)
	return true
}

// refusal words why action name does not apply to it.
func refusal(name string, it *issues.Item) string {
	switch c := it.Claim; {
	case name == "claim" && c != nil && !c.Stale:
		return "not claimed: " + it.Ref + " is held by " + c.Agent + " (" + age(c.AgeSec) + ")"
	case name == "claim":
		return "not claimed: " + it.Ref + " is closed"
	case name == "release" && c == nil:
		return it.Ref + " has no claim"
	case name == "release":
		return "not released: " + it.Ref + " is held by " + c.Agent + " (never forced)"
	case name == "close":
		return it.Ref + " is already closed"
	case name == "reopen":
		return it.Ref + " is not closed"
	}
	return "not queued: " + name + " does not apply to " + it.Ref
}

func (m *Model) toggleClosed() {
	it := m.cur()
	if it == nil {
		return
	}
	if it.State == "open" {
		m.act("close", it.Ref)
	} else {
		m.act("reopen", it.Ref)
	}
}

func (m Model) startComment() (tea.Model, tea.Cmd) {
	it := m.cur()
	if it == nil {
		return m, nil
	}
	if m.opt.Editor == "" {
		m.mode = modePrompt
		m.input.Prompt = ""
		m.input.SetValue("")
		m.input.Focus()
		return m, nil
	}
	f, err := os.CreateTemp("", "moon-comment-*.md")
	if err != nil {
		m.msg = "not queued: " + err.Error()
		return m, nil
	}
	f.Close()
	c := exec.Command("sh", "-c", m.opt.Editor+` "$1"`, "sh", f.Name())
	ref, path := it.Ref, f.Name()
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{ref, path, err} })
}

func (m Model) editorDone(e editorDoneMsg) Model {
	b, rerr := os.ReadFile(e.path)
	os.Remove(e.path)
	switch {
	case e.err != nil:
		m.msg = "comment canceled: editor failed: " + e.err.Error()
	case rerr != nil:
		m.msg = "comment canceled: " + rerr.Error()
	default:
		m.queueComment(e.ref, string(b))
	}
	return m
}

func (m *Model) queueComment(ref, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		m.msg = "comment canceled: empty"
		return
	}
	m.act("comment", ref, body)
}

func (m Model) keyPrompt(k tea.KeyMsg) Model {
	switch k.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.msg = "comment canceled"
	case "enter":
		m.mode = modeList
		m.input.Blur()
		if it := m.cur(); it != nil {
			m.queueComment(it.Ref, m.input.Value())
		}
		m.input.SetValue("")
	default:
		m.input, _ = m.input.Update(k)
	}
	return m
}

func firstLine(out string, err error) string {
	s := out
	if s == "" && err != nil {
		s = err.Error()
	}
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// effLabelMode is the labels column mode in use: the chosen one, else full from 110 columns, short from 70, hidden below.
func (m *Model) effLabelMode() string {
	if m.labelMode != "" {
		return m.labelMode
	}
	return issues.LabelModeFor(m.w)
}

// nextLabelMode cycles full, short, hidden.
func nextLabelMode(cur string) string {
	switch cur {
	case issues.LabelsFull:
		return issues.LabelsShort
	case issues.LabelsShort:
		return issues.LabelsNone
	}
	return issues.LabelsFull
}
