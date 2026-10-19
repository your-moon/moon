package issues

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// memGH is an in-memory GitHub that answers the typed GitHub interface, with no argv or GraphQL text in sight.
// It keeps issues, labels and milestones per repo ("owner/name"), serves pulls the way GitHub does
// (since is inclusive, oldest update first), and applies mutations in order. Tests seed it and read it back.
type memGH struct {
	mu     sync.Mutex
	delay  time.Duration
	repos  map[string]*memRepo
	calls  []string          // one entry per interface call, e.g. "Mutate", "PullIssues"
	log    []string          // one entry per applied write, e.g. "closeIssue ID2", "create", "rest-comment 101"
	sinces []string          // the Since of every PullIssues
	muts   int               // mutations sent
	fail   map[string]string // issue node id -> why its mutations are refused
	clock  int

	// hook refuses a call when it returns an error. It gets the call name, the repo and the issue number
	// (or label name, milestone number) the call targets.
	hook                func(call, repo, ident string) error
	inflight, maxFlight int                  // calls inside enter at once: the overlap the push pool reaches (needs delay > 0)
	labelSpecs          []LabelSpec          // every label definition sent
	created             []NewIssue           // every accepted create, in order
	canned              map[string]*RepoPull // repo -> a pull page served as is (hand-built issues, labels, milestones, PRs)
	msFailCreate        bool                 // milestone creates fail without storing anything
	msCreateLost        bool                 // the next milestone create is stored, then reports a failure
	msCaseTolerant      bool                 // a title that differs only in case is accepted as a new milestone
}

type memRepo struct {
	issues     map[int]*memIssue
	labels     map[string]string // name -> colour
	milestones []RemoteMilestone
	nextNum    int
}

type memIssue struct {
	num                int
	title, body, state string
	stateReason        string
	labels             []string
	milestone, updated string
	comments           []string
	assignees          []string
}

func newMemGH() *memGH { return &memGH{repos: map[string]*memRepo{}, fail: map[string]string{}} }

func (m *memGH) repo(r string) *memRepo {
	if m.repos[r] == nil {
		m.repos[r] = &memRepo{issues: map[int]*memIssue{}, labels: map[string]string{}, nextNum: 100}
	}
	return m.repos[r]
}

// seed adds open issues 1..n (updated at updated) to repo.
func (m *memGH) seed(repo string, n int, updated string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(repo)
	for i := 1; i <= n; i++ {
		r.issues[i] = &memIssue{num: i, title: fmt.Sprintf("t%d", i), body: "b", state: "OPEN", updated: updated}
	}
}

// put adds (or replaces) an issue.
func (m *memGH) put(repo string, i memIssue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i.updated == "" {
		i.updated = "2026-01-01T00:00:00Z"
	}
	if i.state == "" {
		i.state = "OPEN"
	}
	m.repo(repo).issues[i.num] = &i
}

// cannedPull makes the first pull page of repo exactly v (the repository object GitHub would answer with:
// labels, milestones, issues, pullRequests), decoded into the typed RepoPull.
func (m *memGH) cannedPull(t *testing.T, repo string, v map[string]any) {
	t.Helper()
	b, _ := json.Marshal(v)
	var rp RepoPull
	if err := json.Unmarshal(b, &rp); err != nil {
		t.Fatal(err)
	}
	if m.canned == nil {
		m.canned = map[string]*RepoPull{}
	}
	m.canned[repo] = &rp
}

func (m *memGH) addLabels(repo string, names ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range names {
		m.repo(repo).labels[n] = "ededed"
	}
}

func (m *memGH) addMilestone(repo, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(repo)
	r.milestones = append(r.milestones, RemoteMilestone{Number: len(r.milestones) + 1, Title: title, State: "open"})
}

func (m *memGH) issue(repo string, n int) *memIssue {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repo(repo).issues[n]
}

func (m *memGH) callCount() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.calls) }

func (m *memGH) count(call string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (m *memGH) enter(call string) func() {
	m.mu.Lock()
	m.inflight++
	m.maxFlight = max(m.maxFlight, m.inflight)
	m.mu.Unlock()
	time.Sleep(m.delay)
	m.mu.Lock()
	m.calls = append(m.calls, call)
	return func() { m.inflight--; m.mu.Unlock() }
}

func (m *memGH) refused(call, repo, ident string) error {
	if m.hook == nil {
		return nil
	}
	return m.hook(call, repo, ident)
}

func (m *memGH) touch(i *memIssue) {
	m.clock++
	i.updated = fmt.Sprintf("2027-01-01T00:00:%02dZ", m.clock%60)
}

func nodeID(n int) string { return "ID" + strconv.Itoa(n) }

func (i *memIssue) node() *issueNode {
	var labels, comments, assignees []any
	assignees = []any{}
	for _, a := range i.assignees {
		assignees = append(assignees, map[string]any{"login": a})
	}
	for _, l := range i.labels {
		labels = append(labels, map[string]any{"name": l})
	}
	for k, c := range i.comments {
		comments = append(comments, map[string]any{"databaseId": k + 1, "createdAt": "2026-01-01T00:00:00Z", "body": c})
	}
	var ms, reason any
	if i.milestone != "" {
		ms = map[string]any{"title": i.milestone}
	}
	if i.stateReason != "" {
		reason = i.stateReason
	}
	raw, _ := json.Marshal(map[string]any{"number": i.num, "title": i.title, "body": i.body, "state": i.state, "stateReason": reason,
		"url": fmt.Sprintf("u%d", i.num), "createdAt": "2026-01-01T00:00:00Z", "updatedAt": i.updated, "id": nodeID(i.num),
		"author": nil, "milestone": ms, "assignees": map[string]any{"nodes": assignees}, "labels": map[string]any{"nodes": labels},
		"comments": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": comments}})
	var n issueNode
	json.Unmarshal(raw, &n)
	return &n
}

func (m *memGH) PullIssues(repo string, q PullQuery) (RepoPull, error) {
	defer m.enter("PullIssues")()
	if err := m.refused("PullIssues", repo, ""); err != nil {
		return RepoPull{}, err
	}
	m.sinces = append(m.sinces, q.Since)
	r := m.repo(repo)
	if c := m.canned[repo]; c != nil {
		return *c, nil
	}
	var list []*memIssue
	for _, i := range r.issues {
		if q.Since == "" || i.updated >= q.Since {
			list = append(list, i)
		}
	}
	sort.Slice(list, func(a, b int) bool {
		return list[a].updated < list[b].updated || (list[a].updated == list[b].updated && list[a].num < list[b].num)
	})
	var out RepoPull
	for _, i := range list {
		out.Issues.Nodes = append(out.Issues.Nodes, i.node())
	}
	if q.First {
		out.Labels = &struct {
			Nodes []labelNode `json:"nodes"`
		}{}
		out.PullRequests = &prPage{}
		for _, ms := range r.milestones {
			n := milestoneNode{Number: ms.Number, Title: ms.Title, State: strings.ToUpper(ms.State), Description: ms.Description, UpdatedAt: "2026-03-01T00:00:00Z"}
			if ms.DueOn != "" {
				d := ms.DueOn + "T00:00:00Z"
				n.DueOn = &d
			}
			out.Milestones.Nodes = append(out.Milestones.Nodes, n)
		}
		var names []string
		for n := range r.labels {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out.Labels.Nodes = append(out.Labels.Nodes, labelNode{Name: n, Color: r.labels[n]})
		}
	}
	return out, nil
}

func (m *memGH) PullPRs(string, string) (prPage, error) {
	defer m.enter("PullPRs")()
	return prPage{}, nil
}
func (m *memGH) Comments(string, string) (commentPage, error) {
	defer m.enter("Comments")()
	return commentPage{}, nil
}
func (m *memGH) PullRequest(string, int) (*prNode, error) {
	defer m.enter("PullRequest")()
	return nil, nil
}

func (m *memGH) Issue(repo string, num int) (*issueNode, error) {
	defer m.enter("Issue")()
	if err := m.refused("Issue", repo, strconv.Itoa(num)); err != nil {
		return nil, err
	}
	if i := m.repo(repo).issues[num]; i != nil {
		return i.node(), nil
	}
	return nil, nil
}

func (m *memGH) need(repo, ident string) (*memIssue, error) {
	n, _ := strconv.Atoi(ident)
	i := m.repo(repo).issues[n]
	if i == nil {
		return nil, &GhError{Msg: fmt.Sprintf("GraphQL: Could not resolve to an issue or pull request with the number of %s.", ident)}
	}
	return i, nil
}

func (m *memGH) CreateIssue(repo string, in NewIssue) (int, error) {
	defer m.enter("CreateIssue")()
	if err := m.refused("CreateIssue", repo, ""); err != nil {
		return 0, err
	}
	r := m.repo(repo)
	for _, l := range in.Labels {
		if _, ok := r.labels[l]; !ok {
			return 0, &GhError{Msg: fmt.Sprintf("could not add label: '%s' not found", l)}
		}
	}
	r.nextNum++
	i := &memIssue{num: r.nextNum, title: in.Title, body: in.Body, state: "OPEN", labels: in.Labels, milestone: in.Milestone}
	m.touch(i)
	r.issues[i.num] = i
	m.created = append(m.created, in)
	m.log = append(m.log, "create")
	return i.num, nil
}

func (m *memGH) EditIssue(repo, ident string, e IssueEdit) error {
	defer m.enter("EditIssue")()
	if err := m.refused("EditIssue", repo, ident); err != nil {
		return err
	}
	i, err := m.need(repo, ident)
	if err != nil {
		return err
	}
	if e.Title != nil {
		i.title = *e.Title
	}
	if e.Body != nil {
		i.body = *e.Body
	}
	i.labels = editLabels(i.labels, e.AddLabels, e.RemoveLabels)
	if e.RemoveMilestone {
		i.milestone = ""
	} else if e.Milestone != nil {
		i.milestone = *e.Milestone
	}
	m.touch(i)
	m.log = append(m.log, "rest-edit "+ident)
	return nil
}

func editLabels(have, add, remove []string) []string {
	var out []string
	for _, l := range have {
		keep := true
		for _, r := range remove {
			keep = keep && r != l
		}
		if keep {
			out = append(out, l)
		}
	}
	for _, a := range add {
		dup := false
		for _, l := range out {
			dup = dup || l == a
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

func (m *memGH) CloseIssue(repo, ident, reason, comment string) error {
	defer m.enter("CloseIssue")()
	if err := m.refused("CloseIssue", repo, ident); err != nil {
		return err
	}
	i, err := m.need(repo, ident)
	if err != nil {
		return err
	}
	i.state, i.stateReason = "CLOSED", strings.ToUpper(strings.ReplaceAll(reason, " ", "_"))
	if comment != "" {
		i.comments = append(i.comments, comment)
	}
	m.touch(i)
	m.log = append(m.log, "rest-close "+ident)
	return nil
}

func (m *memGH) ReopenIssue(repo, ident string) error {
	defer m.enter("ReopenIssue")()
	if err := m.refused("ReopenIssue", repo, ident); err != nil {
		return err
	}
	i, err := m.need(repo, ident)
	if err != nil {
		return err
	}
	i.state, i.stateReason = "OPEN", ""
	m.touch(i)
	m.log = append(m.log, "rest-reopen "+ident)
	return nil
}

func (m *memGH) CommentIssue(repo, ident, body string) error {
	defer m.enter("CommentIssue")()
	if err := m.refused("CommentIssue", repo, ident); err != nil {
		return err
	}
	i, err := m.need(repo, ident)
	if err != nil {
		return err
	}
	i.comments = append(i.comments, body)
	m.touch(i)
	m.log = append(m.log, "rest-comment "+ident)
	return nil
}

func (m *memGH) HasComment(repo, ident, marker string) (bool, error) {
	defer m.enter("HasComment")()
	if err := m.refused("HasComment", repo, ident); err != nil {
		return false, err
	}
	i, err := m.need(repo, ident)
	if err != nil {
		return false, err
	}
	for _, c := range i.comments {
		if strings.Contains(c, marker) {
			return true, nil
		}
	}
	return false, nil
}

func (m *memGH) FindIssueByMarker(repo, lid, since string) (int, error) {
	defer m.enter("FindIssueByMarker")()
	if err := m.refused("FindIssueByMarker", repo, ""); err != nil {
		return 0, err
	}
	for _, i := range m.repo(repo).issues {
		if strings.Contains(i.body, "<!-- local-id: "+lid+" -->") {
			return i.num, nil
		}
	}
	return 0, nil
}

func (m *memGH) ResolveIDs(repo string, q IDQuery) (IDs, error) {
	defer m.enter("ResolveIDs")()
	r := m.repo(repo)
	ids := IDs{Issues: map[string]string{}, Labels: map[string]string{}, Milestones: map[string]string{}}
	for _, n := range q.Issues {
		if k, _ := strconv.Atoi(n); r.issues[k] != nil {
			ids.Issues[n] = nodeID(k)
		}
	}
	for _, l := range q.Labels {
		if _, ok := r.labels[l]; ok {
			ids.Labels[l] = "LB-" + l
		}
	}
	if q.Milestones {
		ids.MilestonesAll = true
		for _, ms := range r.milestones {
			ids.Milestones[ms.Title] = "MS-" + ms.Title
		}
	}
	return ids, nil
}

// Mutate applies each mutation in order; the alias carries the repo-less issue id in its input, so the repo
// is found by scanning (ids are unique across the repos a test seeds).
func (m *memGH) Mutate(ms []mutation) ([]string, error) {
	defer m.enter("Mutate")()
	m.muts += len(ms)
	failed := make([]string, len(ms))
	for k, mu := range ms {
		id := ""
		for _, key := range []string{"subjectId", "issueId", "labelableId", "id"} {
			if s, ok := mu.input[key].(string); ok {
				id = s
			}
		}
		if msg, bad := m.fail[id]; bad {
			failed[k] = msg
			continue
		}
		var i *memIssue
		for _, r := range m.repos {
			if n, _ := strconv.Atoi(strings.TrimPrefix(id, "ID")); r.issues[n] != nil {
				i = r.issues[n]
			}
		}
		if i == nil {
			failed[k] = "no issue " + id
			continue
		}
		switch mu.field {
		case "closeIssue":
			i.state, i.stateReason = "CLOSED", fmt.Sprint(mu.input["stateReason"])
		case "reopenIssue":
			i.state, i.stateReason = "OPEN", ""
		case "addComment":
			i.comments = append(i.comments, fmt.Sprint(mu.input["body"]))
		case "addLabelsToLabelable":
			i.labels = editLabels(i.labels, labelNames(mu.input["labelIds"]), nil)
		case "removeLabelsFromLabelable":
			i.labels = editLabels(i.labels, nil, labelNames(mu.input["labelIds"]))
		case "updateIssue":
			if t, ok := mu.input["title"].(string); ok {
				i.title = t
			}
			if b, ok := mu.input["body"].(string); ok {
				i.body = b
			}
			if v, ok := mu.input["milestoneId"]; ok {
				i.milestone = strings.TrimPrefix(fmt.Sprint(v), "MS-")
				if v == nil {
					i.milestone = ""
				}
			}
		}
		m.touch(i)
		m.log = append(m.log, mu.field+" "+id)
	}
	return failed, nil
}

func labelNames(v any) []string {
	var out []string
	switch ids := v.(type) {
	case []string:
		for _, s := range ids {
			out = append(out, strings.TrimPrefix(s, "LB-"))
		}
	case []any:
		for _, s := range ids {
			out = append(out, strings.TrimPrefix(fmt.Sprint(s), "LB-"))
		}
	}
	return out
}

func (m *memGH) CreateLabel(repo string, l LabelSpec) error {
	defer m.enter("CreateLabel")()
	if err := m.refused("CreateLabel", repo, l.Name); err != nil {
		return err
	}
	m.repo(repo).labels[l.Name] = l.Color
	m.log = append(m.log, "create-label "+l.Name)
	m.labelSpecs = append(m.labelSpecs, l)
	return nil
}

func (m *memGH) ListMilestones(repo string) ([]RemoteMilestone, error) {
	defer m.enter("ListMilestones")()
	if err := m.refused("ListMilestones", repo, ""); err != nil {
		return nil, err
	}
	return append([]RemoteMilestone(nil), m.repo(repo).milestones...), nil
}

func (m *memGH) CreateMilestone(repo string, ms RemoteMilestone) (RemoteMilestone, error) {
	defer m.enter("CreateMilestone")()
	if err := m.refused("CreateMilestone", repo, ""); err != nil {
		return RemoteMilestone{}, err
	}
	r := m.repo(repo)
	m.log = append(m.log, "POST milestone "+ms.Title)
	if m.msFailCreate {
		return RemoteMilestone{}, &GhError{Msg: "HTTP 502"}
	}
	for _, have := range r.milestones {
		if have.Title == ms.Title || (!m.msCaseTolerant && strings.EqualFold(have.Title, ms.Title)) {
			return RemoteMilestone{}, &GhError{Msg: "HTTP 422: Validation Failed (already_exists)"}
		}
	}
	ms.Number, ms.State = len(r.milestones)+1, "open"
	r.milestones = append(r.milestones, ms)
	if m.msCreateLost {
		m.msCreateLost = false
		return RemoteMilestone{}, &GhError{Msg: "connection reset"}
	}
	return ms, nil
}

func (m *memGH) UpdateMilestone(repo string, number int, ch MilestoneChange) error {
	defer m.enter("UpdateMilestone")()
	if err := m.refused("UpdateMilestone", repo, strconv.Itoa(number)); err != nil {
		return err
	}
	r := m.repo(repo)
	for k := range r.milestones {
		if r.milestones[k].Number == number {
			m.log = append(m.log, fmt.Sprintf("PATCH milestone %d", number))
			if ch.Title != nil {
				r.milestones[k].Title = *ch.Title
			}
			if ch.Description != nil {
				r.milestones[k].Description = *ch.Description
			}
			if ch.State != nil {
				r.milestones[k].State = *ch.State
			}
			if ch.ClearDue {
				r.milestones[k].DueOn = ""
			} else if ch.Due != nil {
				r.milestones[k].DueOn = *ch.Due
			}
			return nil
		}
	}
	return &GhError{Msg: "Not Found"}
}
