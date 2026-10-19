package issues

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cliGH is the production GitHub: every gh command line, REST path and GraphQL query lives in this file (and
// the three milestone calls in milestone_gh.go). It runs them through run, which is ExecGH.Run in production.
type cliGH struct{ run ghRun }

const (
	restTimeout = 90 * time.Second
	gqlTimeout  = 120 * time.Second
)

func (g cliGH) gh(args []string, stdin string) (string, error) {
	return g.run(args, stdin, restTimeout)
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ownerName(repo string) (string, string) {
	o, n, _ := strings.Cut(repo, "/")
	return o, n
}

// gql runs one GraphQL query and decodes data into out.
func (g cliGH) gql(query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	s, err := g.run([]string{"api", "graphql", "--input", "-"}, string(body), gqlTimeout)
	if err != nil {
		return err
	}
	var d struct {
		Data   json.RawMessage `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return err
	}
	if len(d.Errors) > 0 && string(d.Errors) != "null" && string(d.Errors) != "[]" {
		return &GhError{Msg: "graphql: " + trunc(string(d.Errors), 400)}
	}
	return json.Unmarshal(d.Data, out)
}

type gqlErr struct {
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// gqlRaw runs a GraphQL request and returns data and errors separately: a partly failed request still has
// data for the parts that worked. A failure with no JSON body comes back as err.
func (g cliGH) gqlRaw(query string, vars map[string]any) (map[string]json.RawMessage, []gqlErr, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	s, err := g.run([]string{"api", "graphql", "--input", "-"}, string(body), gqlTimeout)
	var d struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []gqlErr                   `json:"errors"`
	}
	if jerr := json.Unmarshal([]byte(s), &d); jerr != nil || (d.Data == nil && len(d.Errors) == 0) {
		if err == nil {
			err = &GhError{Msg: "graphql: unreadable response: " + trunc(s, 200)}
		}
		return nil, nil, err
	}
	return d.Data, d.Errors, nil
}

func errsText(es []gqlErr) string {
	var m []string
	for _, e := range es {
		m = append(m, e.Message)
	}
	return trunc(strings.Join(m, "; "), 400)
}

func firstString(p []any) (string, bool) {
	if len(p) == 0 {
		return "", false
	}
	s, ok := p[0].(string)
	return s, ok
}

func (g cliGH) PullIssues(repo string, q PullQuery) (RepoPull, error) {
	owner, name := ownerName(repo)
	var data struct {
		Repository RepoPull `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "since": orNil(q.Since), "after": orNil(q.After), "withMs": q.First}
	err := g.gql(qIssues, vars, &data)
	return data.Repository, err
}

func (g cliGH) PullPRs(repo, after string) (prPage, error) {
	owner, name := ownerName(repo)
	var data struct {
		Repository struct {
			PullRequests prPage `json:"pullRequests"`
		} `json:"repository"`
	}
	err := g.gql(qPRs, map[string]any{"owner": owner, "name": name, "after": orNil(after)}, &data)
	return data.Repository.PullRequests, err
}

func (g cliGH) Comments(nodeID, after string) (commentPage, error) {
	var d struct {
		Node struct {
			Comments commentPage `json:"comments"`
		} `json:"node"`
	}
	err := g.gql(qComments, map[string]any{"id": nodeID, "after": orNil(after)}, &d)
	return d.Node.Comments, err
}

func (g cliGH) Issue(repo string, num int) (*issueNode, error) {
	owner, name := ownerName(repo)
	var d struct {
		Repository struct {
			Issue *issueNode `json:"issue"`
		} `json:"repository"`
	}
	if err := g.gql(qIssue, map[string]any{"owner": owner, "name": name, "num": num}, &d); err != nil {
		return nil, err
	}
	return d.Repository.Issue, nil
}

func (g cliGH) PullRequest(repo string, num int) (*prNode, error) {
	owner, name := ownerName(repo)
	var d struct {
		Repository struct {
			PullRequest *prNode `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := g.gql(qPROne, map[string]any{"owner": owner, "name": name, "num": num}, &d); err != nil {
		return nil, err
	}
	return d.Repository.PullRequest, nil
}

func (g cliGH) CreateIssue(repo string, in NewIssue) (int, error) {
	ga := []string{"issue", "create", "-R", repo, "--title", in.Title, "--body-file", "-"}
	for _, l := range in.Labels {
		ga = append(ga, "--label", l)
	}
	if in.Milestone != "" {
		ga = append(ga, "--milestone", in.Milestone)
	}
	out, err := g.gh(ga, in.Body)
	if err != nil {
		return 0, err
	}
	m := issueURLRe.FindStringSubmatch(out)
	if m == nil {
		return 0, &GhError{Msg: fmt.Sprintf("create printed no issue url: %q", trunc(out, 200))}
	}
	n, _ := strconv.Atoi(m[1])
	return n, nil
}

func (g cliGH) EditIssue(repo, ident string, e IssueEdit) error {
	ga := []string{"issue", "edit", ident, "-R", repo}
	if e.Title != nil {
		ga = append(ga, "--title", *e.Title)
	}
	if len(e.AddLabels) > 0 {
		ga = append(ga, "--add-label", strings.Join(e.AddLabels, ","))
	}
	if len(e.RemoveLabels) > 0 {
		ga = append(ga, "--remove-label", strings.Join(e.RemoveLabels, ","))
	}
	if e.RemoveMilestone {
		ga = append(ga, "--remove-milestone")
	} else if e.Milestone != nil {
		ga = append(ga, "--milestone", *e.Milestone)
	}
	stdin := ""
	if e.Body != nil {
		ga = append(ga, "--body-file", "-")
		stdin = *e.Body
	}
	_, err := g.gh(ga, stdin)
	return err
}

func (g cliGH) CloseIssue(repo, ident, reason, comment string) error {
	ga := []string{"issue", "close", ident, "-R", repo, "--reason", reason}
	if comment != "" {
		ga = append(ga, "--comment", comment)
	}
	_, err := g.gh(ga, "")
	return err
}

func (g cliGH) ReopenIssue(repo, ident string) error {
	_, err := g.gh([]string{"issue", "reopen", ident, "-R", repo}, "")
	return err
}

func (g cliGH) CommentIssue(repo, ident, body string) error {
	_, err := g.gh([]string{"issue", "comment", ident, "-R", repo, "--body-file", "-"}, body)
	return err
}

func (g cliGH) HasComment(repo, ident, marker string) (bool, error) {
	bodies, err := g.gh([]string{"api", fmt.Sprintf("repos/%s/issues/%s/comments", repo, ident), "--paginate", "--jq", ".[].body"}, "")
	if err != nil {
		return false, err
	}
	return strings.Contains(bodies, marker), nil
}

// FindIssueByMarker looks for the issue a crashed create left behind: first among the issues changed since
// `since`, then by a body search for the local id.
func (g cliGH) FindIssueByMarker(repo, lid, since string) (int, error) {
	marker := "<!-- local-id: " + lid + " -->"
	out, err := g.gh([]string{"api", fmt.Sprintf("repos/%s/issues?state=all&since=%s&per_page=100", repo, since), "--paginate",
		"--jq", ".[] | {number: .number, body: .body}"}, "")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if pyStrip(line) == "" {
			continue
		}
		var it struct {
			Number int    `json:"number"`
			Body   string `json:"body"`
		}
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			return 0, err
		}
		if strings.Contains(it.Body, marker) {
			return it.Number, nil
		}
	}
	out, err = g.gh([]string{"issue", "list", "-R", repo, "--search", lid + " in:body", "--state", "all", "--json", "number,body", "--limit", "5"}, "")
	if err != nil {
		return 0, err
	}
	var items []struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if out = pyStrip(out); out != "" {
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			return 0, err
		}
	}
	for _, it := range items {
		if strings.Contains(it.Body, marker) {
			return it.Number, nil
		}
	}
	return 0, nil
}

func (g cliGH) CreateLabel(repo string, l LabelSpec) error {
	ga := []string{"label", "create", l.Name, "-R", repo, "--force"}
	if l.Color != "" {
		ga = append(ga, "--color", l.Color)
	}
	if l.Description != "" {
		ga = append(ga, "--description", l.Description)
	}
	_, err := g.gh(ga, "")
	return err
}

// ResolveIDs fetches node ids in one query: issues by number, labels by name, and (when asked) the first
// hundred milestones.
func (g cliGH) ResolveIDs(repo string, q IDQuery) (IDs, error) {
	owner, name := ownerName(repo)
	vars := map[string]any{"owner": owner, "name": name}
	var sel []string
	decl := []string{"$owner:String!", "$name:String!"}
	labelVar := map[string]string{} // alias -> label name
	for _, n := range q.Issues {
		sel = append(sel, fmt.Sprintf("i%s:issue(number:%s){id}", n, n))
	}
	for i, l := range q.Labels {
		v := fmt.Sprintf("l%d", i)
		labelVar[v] = l
		vars[v] = l
		decl = append(decl, "$"+v+":String!")
		sel = append(sel, fmt.Sprintf("%s:label(name:$%s){id}", v, v))
	}
	if q.Milestones {
		sel = append(sel, "milestones(first:100,states:[OPEN,CLOSED]){pageInfo{hasNextPage} nodes{id title}}")
	}
	ids := IDs{Issues: map[string]string{}, Labels: map[string]string{}, Milestones: map[string]string{}}
	if len(sel) == 0 {
		return ids, nil
	}
	sort.Strings(sel)
	query := "query(" + strings.Join(decl, ",") + "){repository(owner:$owner,name:$name){" + strings.Join(sel, " ") + "}}"
	data, errs, err := g.gqlRaw(query, vars)
	if err != nil {
		return ids, err
	}
	var repoData map[string]json.RawMessage
	json.Unmarshal(data["repository"], &repoData)
	if repoData == nil {
		return ids, &GhError{Msg: "graphql: " + errsText(errs)}
	}
	for k, raw := range repoData {
		var n struct {
			ID    string `json:"id"`
			Nodes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"nodes"`
			PageInfo pageInfo `json:"pageInfo"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		switch {
		case k == "milestones":
			if n.PageInfo.HasNextPage { // more than one page of milestones: unknown titles stay unknown
				continue
			}
			ids.MilestonesAll = true
			for _, m := range n.Nodes {
				ids.Milestones[m.Title] = m.ID
			}
		case labelVar[k] != "":
			if n.ID != "" {
				ids.Labels[labelVar[k]] = n.ID
			}
		case strings.HasPrefix(k, "i") && n.ID != "":
			ids.Issues[k[1:]] = n.ID
		}
	}
	return ids, nil
}

// Mutate sends the mutations as one aliased request. A mutation GitHub refused (or gave no result for) comes
// back as its message; a request that failed as a whole comes back as err.
func (g cliGH) Mutate(ms []mutation) ([]string, error) {
	failedMsg := make([]string, len(ms))
	var decl, body []string
	vars := map[string]any{}
	for _, m := range ms {
		decl = append(decl, fmt.Sprintf("$%s:%s!", m.alias, m.inType))
		body = append(body, fmt.Sprintf("%s:%s(input:$%s){clientMutationId}", m.alias, m.field, m.alias))
		vars[m.alias] = m.input
	}
	if len(ms) == 0 {
		return failedMsg, nil
	}
	data, errs, err := g.gqlRaw("mutation("+strings.Join(decl, ",")+"){"+strings.Join(body, " ")+"}", vars)
	if err != nil {
		return nil, err
	}
	failed := map[string][]string{} // alias -> messages
	var general []gqlErr            // errors that name no alias
	for _, e := range errs {
		if al, ok := firstString(e.Path); ok {
			failed[al] = append(failed[al], e.Message)
		} else {
			general = append(general, e)
		}
	}
	for i, m := range ms {
		al := m.alias
		if f := failed[al]; len(f) > 0 {
			failedMsg[i] = strings.Join(f, "; ")
		} else if raw, ok := data[al]; !ok || string(raw) == "null" {
			failedMsg[i] = "no result for " + al + " " + errsText(general)
		}
	}
	return failedMsg, nil
}

func (g cliGH) ListMilestones(repo string) ([]RemoteMilestone, error) {
	return ghListMilestones(g.run, repo)
}
func (g cliGH) CreateMilestone(repo string, m RemoteMilestone) (RemoteMilestone, error) {
	return ghCreateMilestone(g.run, repo, m)
}
func (g cliGH) UpdateMilestone(repo string, number int, ch MilestoneChange) error {
	return ghUpdateMilestone(g.run, repo, number, ch)
}

// GraphQL queries behind the pull and live-fetch calls.

const issueFields = `number title body state stateReason url createdAt updatedAt id
  author{login} milestone{title}
  assignees(first:20){nodes{login}} labels(first:50){nodes{name}}
  comments(first:100){pageInfo{hasNextPage endCursor} nodes{databaseId createdAt body author{login}}}`

var qIssues = `query($owner:String!,$name:String!,$since:DateTime,$after:String,$withMs:Boolean!){
 repository(owner:$owner,name:$name){
  labels(first:100) @include(if:$withMs){nodes{name color description}}
  milestones(first:100,states:[OPEN,CLOSED]) @include(if:$withMs){nodes{number title state dueOn description updatedAt}}
  pullRequests(first:100,orderBy:{field:UPDATED_AT,direction:DESC}) @include(if:$withMs){
   pageInfo{hasNextPage endCursor}
   nodes{` + prFields + `}}
  issues(first:100,after:$after,orderBy:{field:UPDATED_AT,direction:ASC},filterBy:{since:$since}){
   pageInfo{hasNextPage endCursor}
   nodes{` + issueFields + `}}}}`

const qComments = `query($id:ID!,$after:String){node(id:$id){... on Issue{comments(first:100,after:$after){
  pageInfo{hasNextPage endCursor} nodes{databaseId createdAt body author{login}}}}}}`

var qIssue = `query($owner:String!,$name:String!,$num:Int!){repository(owner:$owner,name:$name){issue(number:$num){` + issueFields + `}}}`

const prFields = `number title body state isDraft headRefName headRefOid baseRefName createdAt mergedAt mergeCommit{oid}
  author{login} updatedAt url closingIssuesReferences(first:20){nodes{number}}`

var qPROne = `query($owner:String!,$name:String!,$num:Int!){repository(owner:$owner,name:$name){pullRequest(number:$num){
  ` + prFields + `}}}`

const qPRs = `query($owner:String!,$name:String!,$after:String){
 repository(owner:$owner,name:$name){
  pullRequests(first:100,after:$after,orderBy:{field:UPDATED_AT,direction:DESC}){
   pageInfo{hasNextPage endCursor}
   nodes{number title body state isDraft headRefName headRefOid baseRefName createdAt mergedAt mergeCommit{oid}
     author{login} updatedAt url closingIssuesReferences(first:20){nodes{number}}}}}}`
