package issues

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type comment struct {
	DatabaseID int64  `json:"databaseId"`
	CreatedAt  string `json:"createdAt"`
	Body       string `json:"body"`
	Author     *struct {
		Login string `json:"login"`
	} `json:"author"`
}
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}
type issueNode struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	State       string  `json:"state"`
	StateReason *string `json:"stateReason"`
	URL         string  `json:"url"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	ID          string  `json:"id"`
	Author      *struct {
		Login string `json:"login"`
	} `json:"author"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Assignees struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
	} `json:"assignees"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		PageInfo pageInfo  `json:"pageInfo"`
		Nodes    []comment `json:"nodes"`
	} `json:"comments"`
}

type prNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	CreatedAt   string `json:"createdAt"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	UpdatedAt               string `json:"updatedAt"`
	URL                     string `json:"url"`
	ClosingIssuesReferences struct {
		Nodes []struct {
			Number int `json:"number"`
		} `json:"nodes"`
	} `json:"closingIssuesReferences"`
}

func (a *App) mirrorIssueFiles(short string) []string {
	d := filepath.Join(a.Home, short)
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".md") && !e.IsDir() {
			out = append(out, filepath.Join(d, e.Name()))
		}
	}
	return out
}

func login(a *struct {
	Login string `json:"login"`
}) string {
	if a == nil || a.Login == "" {
		return "ghost"
	}
	return a.Login
}

func normBody(s string) string { return pyRstrip(strings.ReplaceAll(s, "\r\n", "\n")) }

func renderIssue(n *issueNode) string {
	labels := []string{}
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	assg := []string{}
	for _, l := range n.Assignees.Nodes {
		assg = append(assg, l.Login)
	}
	var ms, sr any
	if n.Milestone != nil {
		ms = n.Milestone.Title
	}
	if n.StateReason != nil && *n.StateReason != "" {
		sr = strings.ToLower(*n.StateReason)
	}
	fm := Obj{{"number", n.Number}, {"title", n.Title}, {"state", strings.ToLower(n.State)}, {"state_reason", sr},
		{"labels", labels}, {"milestone", ms}, {"assignees", assg}, {"author", login(n.Author)},
		{"created_at", n.CreatedAt}, {"updated_at", n.UpdatedAt}, {"url", n.URL}}
	var b strings.Builder
	b.WriteString("---\n")
	for _, kv := range fm {
		fmt.Fprintf(&b, "%s: %s\n", kv.K, dumps(kv.V, false))
	}
	b.WriteString("---\n\n")
	b.WriteString(normBody(n.Body) + "\n\n## Comments\n")
	for _, c := range n.Comments.Nodes {
		fmt.Fprintf(&b, "\n### %s · %s · id %d\n\n%s\n", login(c.Author), c.CreatedAt, c.DatabaseID, normBody(c.Body))
	}
	return b.String()
}

// readHeader parses the front matter of a mirror file. nil if the file has none.
func readHeader(path string) map[string]any {
	s, err := readText(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || pyStrip(lines[0]) != "---" {
		return nil
	}
	fm := map[string]any{}
	for _, line := range lines[1:] {
		if pyStrip(line) == "---" {
			break
		}
		k, v, _ := strings.Cut(line, ": ")
		var x any
		if err := json.Unmarshal([]byte(v), &x); err != nil {
			continue
		}
		fm[k] = x
	}
	return fm
}

func hStr(h map[string]any, k string) string {
	s, _ := h[k].(string)
	return s
}

func hList(h map[string]any, k string) []string {
	l, _ := h[k].([]any)
	out := []string{}
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) keepDays() float64 {
	if v := a.Environ("ISSUES_KEEP_DAYS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 30
}

func isoEpoch(ts string) (float64, bool) {
	t, err := time.Parse("2006-01-02T15:04:05Z", ts)
	if err != nil {
		return 0, false
	}
	return float64(t.Unix()), true
}

// agedOut: a closed/merged item whose last event (first non-empty stamp) is older than keep days.
func (a *App) agedOut(state string, stamps ...string) bool {
	if state == "open" {
		return false
	}
	for _, s := range stamps {
		if s != "" {
			ts, ok := isoEpoch(s)
			if !ok {
				return false
			}
			return a.nowEpoch()-ts > a.keepDays()*86400 // M_age
		}
	}
	return false
}

func (a *App) guardedRefs() map[string]bool {
	refs := map[string]bool{}
	for _, o := range a.loadOutbox() {
		refs[o.Ref] = true
	}
	if ents, err := os.ReadDir(a.claimsDir()); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".json") {
				short, ident, _ := strings.Cut(strings.TrimSuffix(e.Name(), ".json"), "-")
				refs[short+"#"+ident] = true
			}
		}
	}
	return refs
}

// pruneFiles removes files of aged-out closed issues/PRs; index rows stay. Caller holds .pull.lock.
func (a *App) pruneFiles(short string) int {
	guard := a.guardedRefs()
	removed := 0
	for _, p := range a.mirrorIssueFiles(short) {
		num := strings.TrimSuffix(filepath.Base(p), ".md")
		h := readHeader(p)
		if h != nil && a.agedOut(hStr(h, "state"), hStr(h, "updated_at")) && !guard[short+"#"+num] { // M_guard
			os.Remove(p)
			removed++
		}
	}
	rows := a.readPRs(short)
	pd := filepath.Join(a.Home, short, "pr")
	ents, _ := os.ReadDir(pd)
	for _, e := range ents {
		n := e.Name()
		num := ""
		if len(n) > 3 {
			num = n[:len(n)-3]
		}
		r, ok := rows[num]
		if strings.HasSuffix(n, ".md") && ok && a.agedOut(r.State, r.MergedAt, r.UpdatedAt) && !guard[short+"#"+num] { // M_guard
			os.Remove(filepath.Join(pd, n))
			removed++
		}
	}
	return removed
}

// readIndex is the mirror's index with the queued writes laid over it (see overlay.go).
func (a *App) readIndex(short string) [][]string { return a.overlayIndex(short, a.readIndexRaw(short)) }

// readIndexRaw is the index file as pull wrote it, with no queued writes applied.
func (a *App) readIndexRaw(short string) [][]string {
	s, err := readText(filepath.Join(a.Home, short, "index.tsv"))
	if err != nil {
		return nil
	}
	var rows [][]string
	for _, line := range strings.Split(s, "\n") {
		p := strings.Split(line, "\t")
		if len(p) == 6 {
			rows = append(rows, p)
		}
	}
	return rows
}

func clean(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\t", " "), "\n", " ")
}

// rebuildIndex: one row per file plus the old rows of pruned items. On a full pull, seen drops rows GitHub no longer has.
//
// only (a delta pull) limits the header reads to those files and to files the index has no row for; nil reads every file.
func (a *App) rebuildIndex(short string, seen map[int]bool, only map[int]bool) error {
	rows := map[int][]string{}
	for _, r := range a.readIndexRaw(short) {
		n, err := strconv.Atoi(r[0])
		if err != nil {
			continue
		}
		if seen == nil || seen[n] {
			rows[n] = r
		}
	}
	for _, p := range a.mirrorIssueFiles(short) {
		if only != nil {
			if fn, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".md")); err == nil && !only[fn] && rows[fn] != nil {
				continue
			}
		}
		h := readHeader(p)
		if h == nil {
			continue
		}
		nf, _ := h["number"].(float64)
		n := int(nf)
		ms := ""
		if s, ok := h["milestone"].(string); ok {
			ms = s
		}
		rows[n] = []string{strconv.Itoa(n), hStr(h, "state"), clean(ms), strings.Join(hList(h, "labels"), ","), hStr(h, "updated_at"), clean(hStr(h, "title"))}
	}
	nums := make([]int, 0, len(rows))
	for n := range rows {
		nums = append(nums, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(nums)))
	var b strings.Builder
	for _, n := range nums {
		b.WriteString(strings.Join(rows[n], "\t") + "\n")
	}
	_, err := writeIfChanged(filepath.Join(a.Home, short, "index.tsv"), b.String())
	return err
}

// ---------- PR mirror ----------

type PRRow struct {
	Number, State, Draft, HeadRef, HeadOid, BaseRef, CreatedAt, MergedAt, MergeCommit, Author, UpdatedAt, Closes, Title string
}

const prColCount = 13

func (r PRRow) tsv() string {
	return strings.Join([]string{r.Number, r.State, r.Draft, r.HeadRef, r.HeadOid, r.BaseRef, r.CreatedAt, r.MergedAt, r.MergeCommit, r.Author, r.UpdatedAt, r.Closes, r.Title}, "\t") + "\n"
}

func (r PRRow) closesList() []int {
	out := []int{}
	for _, x := range strings.Split(r.Closes, ",") {
		if x != "" {
			if n, err := strconv.Atoi(x); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func prRowOf(n *prNode) PRRow {
	oid := ""
	if n.MergeCommit != nil {
		oid = n.MergeCommit.Oid
	}
	draft := "0"
	if n.IsDraft {
		draft = "1"
	}
	cl := []string{}
	for _, x := range n.ClosingIssuesReferences.Nodes {
		cl = append(cl, strconv.Itoa(x.Number))
	}
	return PRRow{strconv.Itoa(n.Number), strings.ToLower(n.State), draft, clean(n.HeadRefName), clean(n.HeadRefOid), clean(n.BaseRefName),
		clean(n.CreatedAt), clean(n.MergedAt), clean(oid), clean(login(n.Author)), clean(n.UpdatedAt), strings.Join(cl, ","), clean(n.Title)}
}

func renderPR(n *prNode, r PRRow) string {
	nz := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	fm := Obj{{"number", n.Number}, {"title", n.Title}, {"state", r.State}, {"draft", r.Draft == "1"}, {"head_ref", r.HeadRef},
		{"head_oid", r.HeadOid}, {"base_ref", r.BaseRef}, {"author", r.Author}, {"created_at", r.CreatedAt}, {"updated_at", r.UpdatedAt},
		{"merged_at", nz(r.MergedAt)}, {"merge_commit", nz(r.MergeCommit)}, {"closes", r.closesList()}, {"url", n.URL}}
	var b strings.Builder
	b.WriteString("---\n")
	for _, kv := range fm {
		fmt.Fprintf(&b, "%s: %s\n", kv.K, dumps(kv.V, false))
	}
	b.WriteString("---\n\n" + normBody(n.Body) + "\n")
	return b.String()
}

func (a *App) readPRs(short string) map[string]PRRow {
	rows := map[string]PRRow{}
	s, err := readText(filepath.Join(a.Home, short, "prs.tsv"))
	if err != nil {
		return rows
	}
	for _, line := range strings.Split(s, "\n") {
		p := strings.Split(line, "\t")
		if len(p) == prColCount {
			rows[p[0]] = PRRow{p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8], p[9], p[10], p[11], p[12]}
		}
	}
	return rows
}

func writeIfChanged(p, text string) (bool, error) {
	if old, err := readText(p); err == nil && old == text {
		return false, nil
	}
	return true, atomicWrite(p, text, false)
}

type prPage struct {
	PageInfo pageInfo  `json:"pageInfo"`
	Nodes    []*prNode `json:"nodes"`
}

// pullPRs mirrors pull requests. first is page one when the issues query already fetched it (nil: ask for it).
func (a *App) pullPRs(short string, full bool, first *prPage) (int, error) {
	d := filepath.Join(a.Home, short)
	if err := os.MkdirAll(filepath.Join(d, "pr"), 0o755); err != nil {
		return 0, err
	}
	cpath := filepath.Join(d, "prcursor")
	cursor := ""
	if s, err := readText(cpath); err == nil {
		cursor = pyStrip(s)
	}
	if cursor == "" {
		full = true
	}
	rows := a.readPRs(short)
	var after string
	maxu, changed, stop := cursor, 0, false
	for !stop {
		var page prPage
		if first != nil {
			page, first = *first, nil
		} else {
			var err error
			if page, err = a.GH.PullPRs(a.repoFull(short), after); err != nil {
				return changed, err
			}
		}
		var batch []*prNode
		for _, n := range page.Nodes {
			if !full && n.UpdatedAt < cursor { // M_prcursor
				stop = true
				break
			}
			batch = append(batch, n)
		}
		err := a.withLock(".pull.lock", func() error {
			for _, n := range batch {
				row := prRowOf(n)
				ch, err := writeIfChanged(filepath.Join(d, "pr", fmt.Sprintf("%d.md", n.Number)), renderPR(n, row))
				if err != nil {
					return err
				}
				if ch {
					changed++
				}
				rows[row.Number] = row
				if maxu == "" || n.UpdatedAt > maxu {
					maxu = n.UpdatedAt
				}
			}
			nums := make([]string, 0, len(rows))
			for k := range rows {
				nums = append(nums, k)
			}
			sort.Slice(nums, func(i, j int) bool {
				x, _ := strconv.Atoi(nums[i])
				y, _ := strconv.Atoi(nums[j])
				return x > y
			})
			var b strings.Builder
			for _, k := range nums {
				b.WriteString(rows[k].tsv())
			}
			if _, err := writeIfChanged(filepath.Join(d, "prs.tsv"), b.String()); err != nil {
				return err
			}
			if maxu != "" {
				_, err := writeIfChanged(cpath, maxu+"\n")
				return err
			}
			return nil
		})
		if err != nil {
			return changed, err
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		after = page.PageInfo.EndCursor
	}
	return changed, nil
}

// fillComments pages through the rest of an issue's comments.
func (a *App) fillComments(n *issueNode) error {
	cm := n.Comments.PageInfo
	for cm.HasNextPage {
		pg, err := a.GH.Comments(n.ID, cm.EndCursor)
		if err != nil {
			return err
		}
		n.Comments.Nodes = append(n.Comments.Nodes, pg.Nodes...)
		cm = pg.PageInfo
	}
	n.Comments.PageInfo = pageInfo{}
	return nil
}

func (a *App) fetchIssueLive(short, num string) (*issueNode, error) {
	n, _ := strconv.Atoi(num)
	issue, err := a.GH.Issue(a.repoFull(short), n)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, &GhError{Msg: fmt.Sprintf("no such issue %s#%s", short, num)}
	}
	if err := a.fillComments(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// pull mirrors one repo. Returns the number of files written.
func (a *App) pull(short string, full bool) (int, error) {
	wantFull := full
	d := filepath.Join(a.Home, short)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return 0, err
	}
	cpath := filepath.Join(d, "cursor")
	cursor := ""
	if s, err := readText(cpath); err == nil {
		cursor = pyStrip(s)
	}
	if _, err := os.Stat(filepath.Join(d, "lastpull")); cursor == "" || err != nil {
		full = true // no earlier pull to be a delta of
	}
	var since string
	if !full {
		since = cursor
	}
	var after string
	first, maxu, changed := true, cursor, 0
	seen := map[int]bool{}
	touched := map[int]bool{} // files this pull wrote: the only index rows a delta has to re-read
	var prFirst *prPage
	for {
		data, err := a.GH.PullIssues(a.repoFull(short), PullQuery{Since: since, After: after, First: first})
		if err != nil {
			return changed, err
		}
		if first && data.Labels != nil {
			var lb strings.Builder // name, colour hex, description: what the table needs to draw a label chip
			for _, l := range data.Labels.Nodes {
				desc := ""
				if l.Description != nil {
					desc = *l.Description
				}
				fmt.Fprintf(&lb, "%s\t%s\t%s\n", clean(l.Name), clean(l.Color), clean(desc))
			}
			if err := a.withLock(".pull.lock", func() error { return atomicWrite(filepath.Join(d, "labels.tsv"), lb.String(), false) }); err != nil {
				return changed, err
			}
		}
		if first {
			var recs []msRec
			ids := a.msLocalIDs(short)
			for _, m := range data.Milestones.Nodes {
				due := ""
				if m.DueOn != nil {
					due = dueDate(*m.DueOn)
				}
				recs = append(recs, msRec{Number: m.Number, LocalID: ids[m.Number], Title: m.Title, State: strings.ToLower(m.State), Due: due,
					Desc: pyStrip(opMarkerRe.ReplaceAllString(m.Description, "")), Updated: m.UpdatedAt})
			}
			if err := a.withLock(".pull.lock", func() error { return a.writeMsFile(short, recs) }); err != nil {
				return changed, err
			}
		}
		if first {
			prFirst = data.PullRequests
		}
		first = false
		page := data.Issues
		type rendered struct {
			num       int
			text, upd string
		}
		var rs []rendered
		for _, n := range page.Nodes {
			if err := a.fillComments(n); err != nil {
				return changed, err
			}
			rs = append(rs, rendered{n.Number, renderIssue(n), n.UpdatedAt})
		}
		err = a.withLock(".pull.lock", func() error {
			for _, r := range rs {
				ch, err := writeIfChanged(filepath.Join(d, fmt.Sprintf("%d.md", r.num)), r.text)
				if err != nil {
					return err
				}
				if ch {
					changed++
					touched[r.num] = true
				}
				seen[r.num] = true
				if maxu == "" || r.upd > maxu {
					maxu = r.upd
				}
			}
			if maxu != "" {
				_, err := writeIfChanged(cpath, maxu+"\n")
				return err
			}
			return nil
		})
		if err != nil {
			return changed, err
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		after = page.PageInfo.EndCursor
	}
	if _, err := a.pullPRs(short, wantFull, prFirst); err != nil {
		return changed, err
	}
	err := a.withLock(".pull.lock", func() error {
		if full {
			for _, p := range a.mirrorIssueFiles(short) {
				n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".md"))
				if err == nil && !seen[n] { // M_full_delete
					os.Remove(p)
				}
			}
		}
		var sn map[int]bool
		if full {
			sn = seen
		}
		var only map[int]bool
		if !full {
			only = touched
		}
		if err := a.rebuildIndex(short, sn, only); err != nil {
			return err
		}
		a.pruneFiles(short)
		return atomicWrite(filepath.Join(d, "lastpull"), fmt.Sprintf("%d\n", int64(math.Floor(float64(a.Now().Unix())))), false)
	})
	return changed, err
}
