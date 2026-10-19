package issues

import (
	"fmt"
	"strconv"
	"strings"
)

// showNode is the issue behind a ref as a node: the mirror file, else (closed long ago, file pruned) a live
// fetch, else the queued `new` op of an issue that is not on GitHub yet.
func (a *App) showNode(short, ident, ref string, ops []*Op) (*issueNode, error) {
	if isLocalIdent(ident) {
		for _, op := range ops {
			if op.Op == "new" && op.Ref == ref {
				return a.pendingNode(op), nil
			}
		}
		return nil, die1("no such local issue %s", ref)
	}
	n, _, err := a.loadLocalIssue(short, ident)
	if err == nil {
		return n, nil
	}
	if !isNotExist(err) {
		return nil, err
	}
	for _, r := range a.readIndex(short) {
		if r[0] == ident && !a.localOnly(short) {
			n, err = a.fetchIssueLive(short, ident)
			if err != nil {
				return nil, die1("%s is not kept locally (older than %g days) and the live fetch failed: %s", ref, a.keepDays(), err)
			}
			return n, nil
		}
	}
	return nil, die1("no issue %s in the mirror; run moon issues pull", ref)
}

// pendingNode is the issue a queued `new` op will create. It has no number, url or author yet.
func (a *App) pendingNode(op *Op) *issueNode {
	t := op.Args
	n := &issueNode{Title: argStr(t, "title"), Body: argStr(t, "body"), State: "OPEN", UpdatedAt: a.Now().UTC().Format("2006-01-02T15:04:05Z")}
	for _, l := range argList(t, "labels") {
		n.Labels.Nodes = append(n.Labels.Nodes, struct {
			Name string `json:"name"`
		}{l})
	}
	if m := argStr(t, "milestone"); m != "" {
		n.Milestone = &struct {
			Title string `json:"title"`
		}{m}
	}
	return n
}

// showJSON prints one issue as a JSON object: the fields `ls --format json` has, plus the body, assignees,
// author, dates, url and the claim. Queued local writes are applied, as in the human output, and "pending"
// counts them. The body is the issue text only, without the comments.
func (a *App) showJSON(ref, short, ident string) error {
	ops := a.loadOutbox()
	ref, ident = resolveLanded(a, ops, short, ref, ident)
	n, err := a.showNode(short, ident, ref, ops)
	if err != nil {
		return err
	}
	var pending []*Op
	for _, op := range ops {
		if op.Op != "new" && op.Ref == ref && op.LastError == nil {
			pending = append(pending, op)
		}
	}
	e := &eff{title: n.Title, state: strings.ToLower(n.State), body: n.Body}
	for _, l := range n.Labels.Nodes {
		e.labels = append(e.labels, l.Name)
	}
	if n.Milestone != nil {
		e.milestone = n.Milestone.Title
	}
	if err := applyOps(e, pending); err != nil {
		return err
	}
	assignees := []string{}
	for _, u := range n.Assignees.Nodes {
		assignees = append(assignees, u.Login)
	}
	var number any // null for a local issue that is not on GitHub yet
	num, aerr := strconv.Atoi(ident)
	if aerr == nil {
		number = num
	}
	ws := a.newWorkEnv().statusBody(ref, short, num, e.state, e.labels, e.body)
	author := ""
	if n.Author != nil {
		author = n.Author.Login
	}
	a.println(dumps(Obj{{"ref", ref}, {"number", number}, {"state", e.state}, {"status", ws.Name}, {"status_detail", ws.Label()},
		{"milestone", nilIfEmpty(e.milestone)}, {"labels", nonNil(e.labels)}, {"title", e.title}, {"updated_at", n.UpdatedAt},
		{"body", e.body}, {"assignees", assignees}, {"author", nilIfEmpty(author)}, {"created_at", nilIfEmpty(n.CreatedAt)},
		{"url", nilIfEmpty(n.URL)}, {"claim", a.claimJSON(ref)}, {"pending", len(pending)}, {"depends_on", nonNilInts(dependsOn(e.body))}}, false))
	return nil
}

// claimJSON is the claim on ref, or nil.
func (a *App) claimJSON(ref string) any {
	st, c := a.claimStatus(ref)
	if c == nil {
		return nil
	}
	return Obj{{"agent", c.Agent}, {"host", c.Host}, {"pid", c.Pid}, {"age_seconds", int(a.nowEpoch() - c.TS)},
		{"ttl_seconds", int(c.TTL)}, {"stale", st == "stale"}}
}

func nonNilInts(l []int) []int {
	if l == nil {
		return []int{}
	}
	return l
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// resolveLanded follows a local ref (L-3) to the real number once its create has landed and no `new` is
// still queued for it; any other ref comes back unchanged.
func resolveLanded(a *App, ops []*Op, short, ref, ident string) (string, string) {
	if !isLocalIdent(ident) {
		return ref, ident
	}
	for _, op := range ops {
		if op.Op == "new" && op.Ref == ref {
			return ref, ident
		}
	}
	if n := a.localmapLookup(short, ident); n != 0 {
		return fmt.Sprintf("%s#%d", short, n), strconv.Itoa(n)
	}
	return ref, ident
}
