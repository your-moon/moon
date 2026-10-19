package issues

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The per-alias counter <mirror>/<alias>/local-seq holds the last number handed out. Numbers are
// never reused: the counter only goes up, whatever happens to the issue afterwards.

// nextLocalID returns the next local number of the family for alias: L-<n> for issues, M-<n> for milestones
// (or, for a local-only alias, the next issue or milestone number). The numbers are never reused: the counter
// only goes up, whatever happens to the issue afterwards. The counter lives in LocalIDs, which locks it and
// calls scanMaxID when a counter is lost.
func (a *App) nextLocalID(short string, fam opFamily) (int, error) {
	return a.mirror().LocalIDs.Next(short, fam, func() int { return a.scanMaxID(short, fam) })
}

// scanMaxID is the highest number of the family already in use for alias: local ids in the outbox and the
// localmap, and for a local-only alias what the files hold (issue files and index rows; milestone records).
func (a *App) scanMaxID(short string, fam opFamily) int {
	max := 0
	note := func(n int) {
		if n > max {
			max = n
		}
	}
	idNum := func(id string) int {
		if fam == famMilestone {
			return msSeqOf(id)
		}
		n, _ := seqOf(id)
		return n
	}
	for _, o := range a.loadOutbox() {
		switch fam {
		case famIssue:
			if s, id, ok := strings.Cut(o.Ref, "#"); ok && s == short {
				note(idNum(id))
			}
		case famMilestone:
			if o.Op == "ms_new" && shortOf(o.Ref) == short {
				note(idNum(argStr(o.Args, "local_id")))
			}
		}
	}
	for _, r := range a.mirror().LocalIDs.Rows() {
		if r.Short == short {
			note(idNum(r.Lid))
		}
	}
	switch fam {
	case famIssue:
		if a.localOnly(short) {
			for _, r := range a.readIndex(short) {
				if n, err := strconv.Atoi(r[0]); err == nil {
					note(n)
				}
			}
			for _, id := range a.mirror().Issues.Idents(short) {
				if n, err := strconv.Atoi(id); err == nil {
					note(n)
				}
			}
		}
	case famMilestone:
		for _, r := range a.readMsFile(short) {
			note(idNum(r.LocalID))
			if a.localOnly(short) {
				note(r.Number)
			}
		}
	}
	return max
}

// ---------- local-only repos: issues are canonical in the mirror ----------

func (a *App) stamp() string { return a.Now().UTC().Format("2006-01-02T15:04:05Z") }

func (a *App) newLocalOnly(short, title, body string, labels []string, milestone string) (int, error) {
	n, err := a.nextLocalID(short, famIssue)
	if err != nil {
		return 0, err
	}
	node := &issueNode{Number: n, Title: title, Body: body, State: "OPEN", CreatedAt: a.stamp(), UpdatedAt: a.stamp()}
	node.Author = &struct {
		Login string `json:"login"`
	}{"local"}
	for _, l := range labels {
		node.Labels.Nodes = append(node.Labels.Nodes, struct {
			Name string `json:"name"`
		}{l})
	}
	if milestone != "" {
		node.Milestone = &struct {
			Title string `json:"title"`
		}{milestone}
	}
	err = a.mirror().withLock(".pull.lock", func() error {
		if err := a.mirror().Issues.Put(short, strconv.Itoa(n), renderIssue(node)); err != nil {
			return err
		}
		return a.rebuildIndex(short, nil, nil)
	})
	return n, err
}

const commentsMark = "\n\n## Comments\n"

var commentIDRe = regexp.MustCompile(` · id (\d+)\n`)

// loadLocalIssue parses a mirror file written by renderIssue back into a node plus its raw comment text.
func (a *App) loadLocalIssue(short, ident string) (*issueNode, string, error) {
	text, err := a.mirror().Issues.Get(short, ident)
	if err != nil {
		return nil, "", err
	}
	h := headerOf(text)
	end := strings.Index(text, "\n---\n")
	if h == nil || end < 0 {
		return nil, "", fmt.Errorf("%s/%s.md: not an issue file", short, ident)
	}
	rest := strings.TrimPrefix(text[end+len("\n---\n"):], "\n")
	body, comments := rest, ""
	if i := strings.Index(rest, commentsMark); i >= 0 {
		body, comments = rest[:i], rest[i+len(commentsMark):]
	}
	nf, _ := h["number"].(float64)
	n := &issueNode{Number: int(nf), Title: hStr(h, "title"), Body: body, State: strings.ToUpper(hStr(h, "state")),
		CreatedAt: hStr(h, "created_at"), UpdatedAt: hStr(h, "updated_at"), URL: hStr(h, "url")}
	if s := hStr(h, "state_reason"); s != "" {
		u := strings.ToUpper(s)
		n.StateReason = &u
	}
	if s := hStr(h, "milestone"); s != "" {
		n.Milestone = &struct {
			Title string `json:"title"`
		}{s}
	}
	n.Author = &struct {
		Login string `json:"login"`
	}{hStr(h, "author")}
	for _, l := range hList(h, "labels") {
		n.Labels.Nodes = append(n.Labels.Nodes, struct {
			Name string `json:"name"`
		}{l})
	}
	for _, l := range hList(h, "assignees") {
		n.Assignees.Nodes = append(n.Assignees.Nodes, struct {
			Login string `json:"login"`
		}{l})
	}
	return n, comments, nil
}

// applyLocalOnly performs one write command directly on the issue file of a local-only repo.
func (a *App) applyLocalOnly(kind, ref string, args map[string]any) error {
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return err
	}
	num, err := strconv.Atoi(ident)
	if err != nil {
		return die1("no such issue %s", ref)
	}
	k, err := kindOf(kind)
	if err != nil {
		return err
	}
	return a.mirror().withLock(".pull.lock", func() error {
		ident := strconv.Itoa(num)
		node, comments, err := a.loadLocalIssue(short, ident)
		if err != nil {
			if isNotExist(err) {
				return die1("no such issue %s", ref)
			}
			return err
		}
		addComment := func(body string) {
			next := 1
			for _, m := range commentIDRe.FindAllStringSubmatch(comments, -1) {
				if id, _ := strconv.Atoi(m[1]); id >= next {
					next = id + 1
				}
			}
			comments += fmt.Sprintf("\n### local · %s · id %d\n\n%s\n", a.stamp(), next, normBody(body))
		}
		if err := k.applyLocal(&localEdit{node: node, add: addComment}, args); err != nil {
			return err
		}
		node.UpdatedAt = a.stamp()
		if err := a.mirror().Issues.Put(short, ident, renderIssue(node)+comments); err != nil {
			return err
		}
		return a.rebuildIndex(short, nil, nil)
	})
}
