package issues

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is the read-mostly view of a mirror used by the terminal UI (internal/tui).
// Reads parse the same files `ls` and `show` read. Writes go through the same command
// handlers as the CLI, so they land in the outbox and claims directory identically.
// Nothing here talks to GitHub.

// ErrHeld is returned when a claim or release is refused because someone else holds the claim.
var ErrHeld = errors.New("held by someone else")

// ClaimInfo is a claim on one issue.
type ClaimInfo struct {
	Agent  string
	Host   string
	AgeSec int
	Stale  bool
}

// Item is one row of the issue list, with queued local writes already applied.
type Item struct {
	Ref       string // alias#123, or alias#L-... for a local issue not pushed yet
	Alias     string
	Num       int // 0 for a local issue
	State     string
	Milestone string
	Labels    []string
	Updated   string // ISO timestamp, may be empty
	Created   string // ISO timestamp, empty when the issue file is no longer kept
	Title     string
	Claim     *ClaimInfo
	Pending   int    // queued local ops on this issue
	Work      Status // derived work status (working, in review, stale, ...)
}

// Snapshot is the whole list plus mirror health.
type Snapshot struct {
	Aliases     []string
	Items       []Item
	Queued      int                          // operations in the outbox (not issues: two ops on one issue count 2)
	LabelColors map[string]map[string]string // alias -> label name -> GitHub colour hex, from labels.tsv
	PullAgeSec  int                          // oldest last pull across aliases; -1 when never pulled
	Missing     bool
}

// Detail is one issue as shown in the detail pane.
type Detail struct {
	Ref, Title, State, Milestone, Author string
	Labels                               []string
	Body                                 string // markdown after the front matter, comments included
	Claim                                *ClaimInfo
	Work                                 Status
	Pending                              []string // queued ops, described
	NotKept                              bool     // closed long ago, file pruned
}

// Session reads and writes one mirror.
type Session struct {
	app     *App
	created map[string]string
	pal     Palette
}

// NewSession finds and loads the config exactly as `moon issues` does. dir overrides the mirror directory.
func NewSession(cfgPath, dir string, e Env) (*Session, error) {
	p, err := FindConfig(cfgPath, e.Cwd, e.Getenv)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		return nil, err
	}
	home := cfg.MirrorDir
	if dir != "" {
		home, _ = filepath.Abs(dir)
	}
	host, _ := os.Hostname()
	now := e.Now
	if now == nil {
		now = time.Now
	}
	a := &App{Home: home, GH: ExecGH{}, Out: io.Discard, Err: io.Discard, Stdin: strings.NewReader(""), Now: now, Host: host,
		Environ: e.Getenv, Repos: cfg.Repos}
	return &Session{app: a, created: map[string]string{}, pal: NewPalette(DetectTheme(e.Getenv, cfg.Colors.Theme), cfg.Colors.Overrides)}, nil
}

// Palette is the colour palette of the config's theme, for the terminal UI.
func (s *Session) Palette() Palette { return s.pal }

// MirrorDir is where the mirror lives.
func (s *Session) MirrorDir() string { return s.app.Home }

func (s *Session) claimInfo(c *Claim) *ClaimInfo {
	return &ClaimInfo{Agent: c.Agent, Host: c.Host, AgeSec: int(s.app.nowEpoch() - c.TS), Stale: s.app.claimIsStale(c)}
}

// QueryMatch is the match `ls -q` uses: case-insensitive substring of the title or of the comma-joined labels.
func QueryMatch(title, labelsCSV, q string) bool {
	q = strings.ToLower(q)
	return q == "" || strings.Contains(strings.ToLower(title), q) || strings.Contains(strings.ToLower(labelsCSV), q)
}

// createdAt reads created_at from the first bytes of an issue file.
func (s *Session) createdAt(short, num string) string {
	key := short + "#" + num
	if v, ok := s.created[key]; ok {
		return v
	}
	v := ""
	if f, err := os.Open(filepath.Join(s.app.Home, short, num+".md")); err == nil {
		buf := make([]byte, 2048)
		n, _ := f.Read(buf)
		f.Close()
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			if k, val, ok := strings.Cut(line, ": "); ok && k == "created_at" {
				v = strings.Trim(val, `"`)
				break
			}
		}
	}
	s.created[key] = v
	return v
}

// Load reads the index, claims and outbox. It never panics on a missing or empty mirror.
func (s *Session) Load() Snapshot { return s.load(true) }

// LoadQuick is Load without the per-issue created_at read (Created stays empty): one index read per
// alias instead of one file open per issue. Shell completion uses it to stay fast on big mirrors.
func (s *Session) LoadQuick() Snapshot { return s.load(false) }

func (s *Session) load(withCreated bool) Snapshot {
	a := s.app
	snap := Snapshot{Aliases: a.Shorts(), Missing: true, PullAgeSec: 0, LabelColors: map[string]map[string]string{}}
	we := a.newWorkEnv()
	ops := a.loadOutbox()
	snap.Queued = len(ops)
	byRef := map[string][]*Op{}
	for _, op := range ops {
		byRef[op.Ref] = append(byRef[op.Ref], op)
	}
	claims := map[string]*Claim{}
	if ents, err := os.ReadDir(a.claimsDir()); err == nil {
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			short, ident, _ := strings.Cut(strings.TrimSuffix(e.Name(), ".json"), "-")
			if c := a.readClaim(filepath.Join(a.claimsDir(), e.Name())); c != nil {
				claims[short+"#"+ident] = c
			}
		}
	}
	for _, short := range snap.Aliases {
		snap.LabelColors[short] = a.labelColors(short)
		if _, err := os.Stat(filepath.Join(a.Home, short, "index.tsv")); err == nil {
			snap.Missing = false
		}
		for _, r := range a.readIndex(short) {
			n, err := strconv.Atoi(r[0])
			if err != nil {
				continue
			}
			ref := short + "#" + r[0]
			e := &eff{title: r[5], state: r[1], milestone: r[2], labels: splitLabels(r[3])}
			var pend []*Op
			for _, op := range byRef[ref] {
				if op.Op != "new" {
					pend = append(pend, op)
				}
			}
			applyOps(e, pend)
			it := Item{Ref: ref, Alias: short, Num: n, State: e.state, Milestone: e.milestone, Labels: e.labels, Updated: r[4],
				Title: e.title, Pending: len(pend)}
			if withCreated {
				it.Created = s.createdAt(short, r[0])
			}
			it.Work = we.status(ref, short, n, e.state, e.labels)
			if c := claims[ref]; c != nil {
				it.Claim = s.claimInfo(c)
			}
			snap.Items = append(snap.Items, it)
		}
		for _, op := range ops {
			if op.Op != "new" || shortOf(op.Ref) != short {
				continue
			}
			t := op.Args
			e := &eff{title: argStr(t, "title"), state: "open", labels: argList(t, "labels"), milestone: argStr(t, "milestone")}
			var rest []*Op
			for _, o := range byRef[op.Ref] {
				if o.Op != "new" {
					rest = append(rest, o)
				}
			}
			applyOps(e, rest)
			it := Item{Ref: op.Ref, Alias: short, State: e.state, Milestone: e.milestone, Labels: e.labels, Updated: op.TS, Created: op.TS,
				Title: e.title, Pending: len(byRef[op.Ref])}
			it.Work = we.status(op.Ref, short, 0, e.state, e.labels)
			if c := claims[op.Ref]; c != nil {
				it.Claim = s.claimInfo(c)
			}
			snap.Items = append(snap.Items, it)
		}
	}
	snap.PullAgeSec = -1
	if age, ok := a.mirrorAge(snap.Aliases); ok && len(snap.Aliases) > 0 {
		snap.PullAgeSec = int(age)
	}
	return snap
}

func splitLabels(csv string) []string {
	if csv == "" {
		return nil
	}
	return strings.Split(csv, ",")
}

// Detail reads one issue from its mirror file (never from the network).
func (s *Session) Detail(ref string) (Detail, error) {
	a := s.app
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Ref: ref}
	var pend []*Op
	var nw *Op
	for _, op := range a.loadOutbox() {
		if op.Ref != ref {
			continue
		}
		if op.Op == "new" {
			nw = op
		} else {
			pend = append(pend, op)
			d.Pending = append(d.Pending, describe(op))
		}
	}
	if st, c := a.claimStatus(ref); st != "none" && c != nil {
		d.Claim = s.claimInfo(c)
	}
	var e *eff
	if isLocalIdent(ident) {
		if nw == nil {
			return d, die1("no such local issue %s", ref)
		}
		t := nw.Args
		e = &eff{title: argStr(t, "title"), state: "open", labels: argList(t, "labels"), milestone: argStr(t, "milestone"), body: argStr(t, "body")}
		d.Body = e.body
	} else {
		p := filepath.Join(a.Home, short, ident+".md")
		text, rerr := readText(p)
		if rerr != nil {
			if !isNotExist(rerr) {
				return d, rerr
			}
			for _, r := range a.readIndex(short) {
				if r[0] == ident {
					e = &eff{title: r[5], state: r[1], milestone: r[2], labels: splitLabels(r[3])}
				}
			}
			if e == nil {
				return d, die1("%s not in the local mirror", ref)
			}
			d.NotKept = true
		} else {
			h := readHeader(p)
			if h == nil {
				h = map[string]any{}
			}
			e = &eff{title: hStr(h, "title"), state: hStr(h, "state"), labels: hList(h, "labels"), milestone: hStr(h, "milestone")}
			d.Author = hStr(h, "author")
			d.Body = strings.TrimLeft(stripFrontMatter(text), "\n")
		}
	}
	applyOps(e, pend)
	d.Title, d.State, d.Milestone, d.Labels = e.title, e.state, e.milestone, e.labels
	num, _ := strconv.Atoi(ident)
	d.Work = a.newWorkEnv().status(ref, short, num, e.state, e.labels)
	return d, nil
}

func stripFrontMatter(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || pyStrip(lines[0]) != "---" {
		return s
	}
	for i, l := range lines[1:] {
		if pyStrip(l) == "---" {
			return strings.Join(lines[i+2:], "\n")
		}
	}
	return s
}

// run executes a command handler and returns what it printed.
func (s *Session) run(fn func(*App) error) (string, error) {
	var buf bytes.Buffer
	s.app.Out = &buf
	defer func() { s.app.Out = io.Discard }()
	err := fn(s.app)
	return strings.TrimSpace(buf.String()), err
}

func newOpts(pos []string, kv ...string) *Opts {
	o := &Opts{S: map[string]string{}, L: map[string][]string{}, B: map[string]bool{}, Seen: map[string]bool{}, Pos: pos}
	for i := 0; i+1 < len(kv); i += 2 {
		o.S[kv[i]] = kv[i+1]
		o.Seen[kv[i]] = true
	}
	return o
}

// Claim takes a claim through the same code as `moon issues claim`. A claim held by another agent returns ErrHeld, never forced.
func (s *Session) Claim(ref, agent string) (string, error) {
	o := newOpts([]string{ref}, "agent", agent, "ttl", "120m", "pid", "0")
	msg, err := s.run(func(a *App) error { return a.cmdClaim(o) })
	var ee *exitError
	if errors.As(err, &ee) && ee.Code == 3 {
		return msg, ErrHeld
	}
	return msg, err
}

// Release drops the agent's own claim; someone else's claim returns ErrHeld. Never forced.
func (s *Session) Release(ref, agent string) (string, error) {
	o := newOpts([]string{ref}, "agent", agent)
	msg, err := s.run(func(a *App) error { return a.cmdRelease(o) })
	var ee *exitError
	if errors.As(err, &ee) && ee.Code == 3 {
		return msg, ErrHeld
	}
	return msg, err
}

// Close queues a close (releasing the agent's own claim, as the CLI does with --agent).
func (s *Session) Close(ref, agent string) error {
	o := newOpts([]string{ref}, "reason", "completed", "agent", agent)
	_, err := s.run(func(a *App) error { return a.cmdSimple("close")(o) })
	return err
}

// Reopen queues a reopen.
func (s *Session) Reopen(ref string) error {
	_, err := s.run(func(a *App) error { return a.cmdSimple("reopen")(newOpts([]string{ref})) })
	return err
}

// Comment queues a comment.
func (s *Session) Comment(ref, body string) error {
	_, err := s.run(func(a *App) error { return a.cmdSimple("comment")(newOpts([]string{ref}, "body", body)) })
	return err
}

// SortedLabels and SortedMilestones are the distinct values in items, for the pickers.
func SortedLabels(items []Item) []string {
	seen := map[string]bool{}
	for _, it := range items {
		for _, l := range it.Labels {
			seen[l] = true
		}
	}
	return sortedKeys(seen)
}

func SortedMilestones(items []Item) []string {
	seen := map[string]bool{}
	for _, it := range items {
		if it.Milestone != "" {
			seen[it.Milestone] = true
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ClaimRow is one claim, as `moon issues claims` lists it.
type ClaimRow struct {
	Ref, Agent, Host string
	Pid              int
	AgeSec, TTLSec   int
	Stale            bool
	Work             Status // status of the claimed issue; Work.Name done means the claim is stale cargo
	IssueClosed      bool
}

// Claims lists every claim, sorted by ref file name like `moon issues claims`.
func (s *Session) Claims() []ClaimRow {
	a := s.app
	ents, _ := os.ReadDir(a.claimsDir())
	names := []string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	we := a.newWorkEnv()
	var out []ClaimRow
	for _, n := range names {
		c := a.readClaim(filepath.Join(a.claimsDir(), n))
		if c == nil {
			continue
		}
		ref := strings.Replace(strings.TrimSuffix(n, ".json"), "-", "#", 1)
		w := a.claimWork(we, ref)
		out = append(out, ClaimRow{Ref: ref, Agent: c.Agent, Host: c.Host, Pid: c.Pid,
			AgeSec: int(a.nowEpoch() - c.TS), TTLSec: int(c.TTL), Stale: a.claimIsStale(c), Work: w, IssueClosed: w.Name == StDone})
	}
	return out
}

// MilestoneRow is one line of the mirror's milestones.tsv, as `moon issues milestones` lists it.
type MilestoneRow struct {
	Alias, Title, State string // State is the milestone's own: open or closed
	Closed, Open        int
	Due                 string // ISO timestamp, empty when none
}

// Milestones reads milestones.tsv for every alias. Mirror only, no network.
func (s *Session) Milestones() []MilestoneRow {
	var out []MilestoneRow
	for _, short := range s.app.Shorts() {
		txt, err := readText(filepath.Join(s.app.Home, short, "milestones.tsv"))
		if err != nil {
			continue
		}
		for _, line := range splitLines(txt) {
			p := strings.Split(line, "\t")
			if len(p) < 4 {
				continue
			}
			c, _ := strconv.Atoi(p[2])
			o, _ := strconv.Atoi(p[3])
			r := MilestoneRow{Alias: short, Title: p[0], State: p[1], Closed: c, Open: o}
			if len(p) > 4 {
				r.Due = p[4]
			}
			out = append(out, r)
		}
	}
	return out
}

// SetMilestone queues a milestone change like `moon issues milestone <ref> <title>`; title "none" clears it.
func (s *Session) SetMilestone(ref, title string) error {
	_, err := s.run(func(a *App) error { return a.cmdSimple("milestone")(newOpts([]string{ref, title})) })
	return err
}

// Label queues label changes like `moon issues label <ref> --add A --remove B`.
func (s *Session) Label(ref string, add, remove []string) error {
	o := newOpts([]string{ref})
	o.L["add"], o.L["remove"] = add, remove
	_, err := s.run(func(a *App) error { return a.cmdSimple("label")(o) })
	return err
}

// PRItem is one pull request of the mirror, for completion.
type PRItem struct {
	Ref, Alias, State, Title, Updated string
	Num                               int
}

// PRs reads prs.tsv for every alias. Mirror only, no network.
func (s *Session) PRs() []PRItem {
	var out []PRItem
	for _, short := range s.app.Shorts() {
		for _, r := range s.app.readPRs(short) {
			n, err := strconv.Atoi(r.Number)
			if err != nil {
				continue
			}
			out = append(out, PRItem{Ref: short + "#" + r.Number, Alias: short, Num: n, State: r.State, Title: r.Title, Updated: r.UpdatedAt})
		}
	}
	return out
}
