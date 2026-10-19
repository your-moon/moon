package issues

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// These are the only tests that look at gh command lines, REST paths and GraphQL text: they drive cliGH over an
// argv fake and check what the adapter sends and how it reads the answer. Every other test talks to memGH.

type argvCall struct {
	args  []string
	stdin string
}

func (c argvCall) line() string { return strings.Join(c.args, " ") }

// argvGH records each gh command line and answers it with handler.
type argvGH struct {
	calls   []argvCall
	handler func(args []string, stdin string) (string, error)
}

func (f *argvGH) Run(args []string, stdin string, _ time.Duration) (string, error) {
	f.calls = append(f.calls, argvCall{append([]string{}, args...), stdin})
	return f.handler(args, stdin)
}

func (f *argvGH) gh() cliGH { return cliGH{run: f.Run} }

func (f *argvGH) lines() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.line())
	}
	return out
}

// reply answers every call with out.
func reply(out string) func([]string, string) (string, error) {
	return func([]string, string) (string, error) { return out, nil }
}

// gqlBody decodes the request body of a `gh api graphql --input -` call.
func gqlBody(t *testing.T, c argvCall) (query string, vars map[string]any) {
	t.Helper()
	if strings.Join(c.args, " ") != "api graphql --input -" {
		t.Fatalf("not a graphql call: %v", c.args)
	}
	var b struct {
		Query     string
		Variables map[string]any
	}
	if err := json.Unmarshal([]byte(c.stdin), &b); err != nil {
		t.Fatal(err)
	}
	return b.Query, b.Variables
}

func TestCLIFindByMarkerRESTHit(t *testing.T) {
	f := &argvGH{handler: reply(`{"number":4,"body":"other"}` + "\n" + `{"number":9,"body":"text <!-- local-id: L-3 -->"}` + "\n")}
	n, err := f.gh().FindIssueByMarker("example/app", "L-3", "2026-03-01T00:00:00Z")
	if err != nil || n != 9 {
		t.Fatalf("%d %v", n, err)
	}
	eq(t, "calls", strings.Join(f.lines(), "\n"),
		"api repos/example/app/issues?state=all&since=2026-03-01T00:00:00Z&per_page=100 --paginate --jq .[] | {number: .number, body: .body}")
}

// The marker is not among the issues changed since the create began (the clock, or a page limit): the body search finds it.
func TestCLIFindByMarkerFallsBackToSearch(t *testing.T) {
	f := &argvGH{handler: func(args []string, stdin string) (string, error) {
		if args[0] == "api" {
			return `{"number":4,"body":"other"}` + "\n\n", nil
		}
		return `[{"number":5,"body":"no marker"},{"number":12,"body":"x <!-- local-id: L-3 -->"}]`, nil
	}}
	n, err := f.gh().FindIssueByMarker("example/app", "L-3", "S")
	if err != nil || n != 12 {
		t.Fatalf("%d %v", n, err)
	}
	lines := f.lines()
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "api repos/example/app/issues?state=all&since=S&per_page=100 --paginate") ||
		lines[1] != "issue list -R example/app --search L-3 in:body --state all --json number,body --limit 5" {
		t.Fatalf("calls: %q", lines)
	}
}

// A search hit whose body lacks the exact marker (L-3 inside L-31) is not the issue.
func TestCLIFindByMarkerNotFound(t *testing.T) {
	for name, search := range map[string]string{"empty output": "", "no match": `[{"number":5,"body":"<!-- local-id: L-31 -->"}]`, "empty list": "[]"} {
		f := &argvGH{handler: func(args []string, stdin string) (string, error) {
			if args[0] == "api" {
				return "", nil
			}
			return search, nil
		}}
		if n, err := f.gh().FindIssueByMarker("example/app", "L-3", "S"); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", name, n, err)
		}
		if len(f.calls) != 2 {
			t.Fatalf("%s: want REST then search, got %q", name, f.lines())
		}
	}
}

func TestCLIFindByMarkerErrors(t *testing.T) {
	f := &argvGH{handler: func([]string, string) (string, error) { return "", &GhError{Msg: "HTTP 502"} }}
	if _, err := f.gh().FindIssueByMarker("example/app", "L-3", "S"); err == nil || err.Error() != "HTTP 502" || len(f.calls) != 1 {
		t.Fatalf("a failed REST lookup must stop there: %v %q", err, f.lines())
	}
	f = &argvGH{handler: func(args []string, stdin string) (string, error) {
		if args[0] == "api" {
			return "", nil
		}
		return "", &GhError{Msg: "search down"}
	}}
	if _, err := f.gh().FindIssueByMarker("example/app", "L-3", "S"); err == nil || err.Error() != "search down" {
		t.Fatalf("a failed search must surface: %v", err)
	}
	f = &argvGH{handler: reply("not json\n")}
	if _, err := f.gh().FindIssueByMarker("example/app", "L-3", "S"); err == nil {
		t.Fatal("garbage from the REST lookup must be an error")
	}
}

func TestCLIIssueWriteArgv(t *testing.T) {
	f := &argvGH{handler: reply("https://github.com/example/app/issues/40\n")}
	g := f.gh()
	n, err := g.CreateIssue("example/app", NewIssue{Title: "thing: broken", Body: "hello\n", Labels: []string{"bug", "x"}, Milestone: "v1"})
	if err != nil || n != 40 {
		t.Fatalf("%d %v", n, err)
	}
	if c := f.calls[0]; c.line() != "issue create -R example/app --title thing: broken --body-file - --label bug --label x --milestone v1" || c.stdin != "hello\n" {
		t.Fatalf("create: %q stdin %q", c.line(), c.stdin)
	}
	f.handler = reply("created!\n")
	if _, err := g.CreateIssue("example/app", NewIssue{Title: "t"}); err == nil || !strings.Contains(err.Error(), "printed no issue url") {
		t.Fatalf("a create that prints no url: %v", err)
	}

	f = &argvGH{handler: reply("")}
	g = f.gh()
	title, body, ms := "T", "B", "v2"
	g.EditIssue("example/app", "7", IssueEdit{Title: &title, Body: &body, Milestone: &ms, AddLabels: []string{"a", "b"}, RemoveLabels: []string{"c"}})
	g.EditIssue("example/app", "7", IssueEdit{RemoveMilestone: true})
	g.CloseIssue("example/app", "7", "not planned", "dup")
	g.CloseIssue("example/app", "7", "completed", "")
	g.ReopenIssue("example/app", "7")
	g.CommentIssue("example/app", "7", "note")
	g.CreateLabel("example/app", LabelSpec{Name: "area::sim", Color: "1d76db", Description: "simulator"})
	g.CreateLabel("example/app", LabelSpec{Name: "plain"})
	eq(t, "argv", strings.Join(f.lines(), "\n"), strings.Join([]string{
		"issue edit 7 -R example/app --title T --add-label a,b --remove-label c --milestone v2 --body-file -",
		"issue edit 7 -R example/app --remove-milestone",
		"issue close 7 -R example/app --reason not planned --comment dup",
		"issue close 7 -R example/app --reason completed",
		"issue reopen 7 -R example/app",
		"issue comment 7 -R example/app --body-file -",
		"label create area::sim -R example/app --force --color 1d76db --description simulator",
		"label create plain -R example/app --force",
	}, "\n"))
	if f.calls[0].stdin != "B" || f.calls[5].stdin != "note" {
		t.Fatalf("bodies go on stdin: %q %q", f.calls[0].stdin, f.calls[5].stdin)
	}
}

func TestCLIHasCommentReadsBodies(t *testing.T) {
	f := &argvGH{handler: reply("first\n\nsecond <!-- op-id: abc -->\n")}
	ok, err := f.gh().HasComment("example/app", "7", "<!-- op-id: abc -->")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := f.gh().HasComment("example/app", "7", "<!-- op-id: zzz -->"); ok {
		t.Fatal("a marker that is not there")
	}
	eq(t, "argv", f.calls[0].line(), "api repos/example/app/issues/7/comments --paginate --jq .[].body")
}

func TestCLIPullIssuesQueryAndDecode(t *testing.T) {
	f := &argvGH{handler: reply(`{"data":{"repository":{` +
		`"labels":{"nodes":[{"name":"kind:bug","color":"d73a4a","description":"Something\tis broken"}]},` +
		`"milestones":{"nodes":[{"number":3,"title":"v1","state":"OPEN","dueOn":"2026-04-01T00:00:00Z","description":"first","updatedAt":"2026-02-01T00:00:00Z"}]},` +
		`"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"C1"},"nodes":[{"number":5,"title":"t","body":"b","state":"CLOSED","stateReason":"COMPLETED","url":"u",` +
		`"createdAt":"2026-02-01T00:00:00Z","updatedAt":"2026-02-02T00:00:00Z","id":"I5","author":{"login":"al"},"milestone":{"title":"v1"},` +
		`"assignees":{"nodes":[{"login":"sam"}]},"labels":{"nodes":[{"name":"bug"}]},"comments":{"pageInfo":{"hasNextPage":false},"nodes":[]}}]},` +
		`"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":6,"title":"p","state":"OPEN","isDraft":true,"headRefName":"h","closingIssuesReferences":{"nodes":[{"number":5}]}}]}}}}`)}
	rp, err := f.gh().PullIssues("example/app", PullQuery{Since: "2026-01-01T00:00:00Z", After: "C0", First: true})
	if err != nil {
		t.Fatal(err)
	}
	q, vars := gqlBody(t, f.calls[0])
	for _, want := range []string{"issues(", "labels(", "milestones(", "pullRequests(", "$since", "$after", "$withMs"} {
		contains0(t, "query", q, want)
	}
	eq(t, "vars", mustJSON(vars), `{"after":"C0","name":"app","owner":"example","since":"2026-01-01T00:00:00Z","withMs":true}`)
	if len(rp.Issues.Nodes) != 1 || rp.Issues.Nodes[0].Number != 5 || !rp.Issues.PageInfo.HasNextPage || rp.Issues.PageInfo.EndCursor != "C1" {
		t.Fatalf("issues: %+v", rp.Issues)
	}
	n := rp.Issues.Nodes[0]
	if n.State != "CLOSED" || n.Milestone == nil || n.Milestone.Title != "v1" || len(n.Assignees.Nodes) != 1 || n.Assignees.Nodes[0].Login != "sam" || n.Labels.Nodes[0].Name != "bug" {
		t.Fatalf("issue node: %+v", n)
	}
	if rp.Labels == nil || rp.Labels.Nodes[0].Color != "d73a4a" || len(rp.Milestones.Nodes) != 1 || rp.Milestones.Nodes[0].Number != 3 ||
		rp.PullRequests == nil || len(rp.PullRequests.Nodes) != 1 || !rp.PullRequests.Nodes[0].IsDraft {
		t.Fatalf("first page extras: %+v", rp)
	}
	// a later page does not ask for the labels and milestones; nulls go out for the unset cursors
	f.calls = nil
	f.gh().PullIssues("example/app", PullQuery{})
	_, vars = gqlBody(t, f.calls[0])
	eq(t, "vars", mustJSON(vars), `{"after":null,"name":"app","owner":"example","since":null,"withMs":false}`)
}

func TestCLIGraphQLErrorsAreErrors(t *testing.T) {
	f := &argvGH{handler: reply(`{"errors":[{"message":"rate limited"}],"data":null}`)}
	_, err := f.gh().PullIssues("example/app", PullQuery{})
	if err == nil || !strings.HasPrefix(err.Error(), "graphql: ") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("%v", err)
	}
	f = &argvGH{handler: func([]string, string) (string, error) { return "", &GhError{Msg: "offline"} }}
	if _, err := f.gh().Issue("example/app", 3); err == nil || err.Error() != "offline" {
		t.Fatalf("%v", err)
	}
}

func TestCLIIssueAndPullRequestFetch(t *testing.T) {
	f := &argvGH{handler: reply(`{"data":{"repository":{"issue":{"number":12,"title":"old","body":"from github","state":"CLOSED","labels":{"nodes":[{"name":"bug"}]},` +
		`"assignees":{"nodes":[{"login":"sam"}]},"comments":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`)}
	n, err := f.gh().Issue("example/app", 12)
	if err != nil || n == nil || n.Body != "from github" || n.Assignees.Nodes[0].Login != "sam" {
		t.Fatalf("%+v %v", n, err)
	}
	q, vars := gqlBody(t, f.calls[0])
	contains0(t, "query", q, "issue(number:$num)")
	eq(t, "vars", mustJSON(vars), `{"name":"app","num":12,"owner":"example"}`)
	f = &argvGH{handler: reply(`{"data":{"repository":{"issue":null}}}`)}
	if n, err := f.gh().Issue("example/app", 12); err != nil || n != nil {
		t.Fatalf("a missing issue is nil, not an error: %+v %v", n, err)
	}
}

func TestCLIResolveIDsQueryAndDecode(t *testing.T) {
	f := &argvGH{handler: reply(`{"data":{"repository":{"i7":{"id":"ID7"},"i9":null,"l0":{"id":"LB1"},"l1":null,` +
		`"milestones":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"MS1","title":"v1"}]}}}}`)}
	ids, err := f.gh().ResolveIDs("example/app", IDQuery{Issues: []string{"7", "9"}, Labels: []string{"bug", "nope"}, Milestones: true})
	if err != nil {
		t.Fatal(err)
	}
	q, vars := gqlBody(t, f.calls[0])
	eq(t, "query", q, "query($owner:String!,$name:String!,$l0:String!,$l1:String!){repository(owner:$owner,name:$name){"+
		"i7:issue(number:7){id} i9:issue(number:9){id} l0:label(name:$l0){id} l1:label(name:$l1){id} milestones(first:100,states:[OPEN,CLOSED]){pageInfo{hasNextPage} nodes{id title}}}}")
	eq(t, "vars", mustJSON(vars), `{"l0":"bug","l1":"nope","name":"app","owner":"example"}`)
	eq(t, "ids", mustJSON(ids), `{"Issues":{"7":"ID7"},"Labels":{"bug":"LB1"},"Milestones":{"v1":"MS1"},"MilestonesAll":true}`)
	// more than one page of milestones: titles stay unknown and the answer does not claim to be complete
	f = &argvGH{handler: reply(`{"data":{"repository":{"milestones":{"pageInfo":{"hasNextPage":true},"nodes":[{"id":"MS1","title":"v1"}]}}}}`)}
	ids, _ = f.gh().ResolveIDs("example/app", IDQuery{Milestones: true})
	if ids.MilestonesAll || len(ids.Milestones) != 0 {
		t.Fatalf("%+v", ids)
	}
	// nothing to look up: no request
	f = &argvGH{handler: reply("")}
	f.gh().ResolveIDs("example/app", IDQuery{})
	if len(f.calls) != 0 {
		t.Fatalf("empty query sent: %q", f.lines())
	}
}

func TestCLIMutateAliasesAndPartialFailure(t *testing.T) {
	f := &argvGH{handler: reply(`{"data":{"m0":{"clientMutationId":null},"m1":null,"m2":{"clientMutationId":null}},` +
		`"errors":[{"message":"issue is locked","path":["m1"]}]}`)}
	ms := []mutation{
		{alias: "m0", field: "closeIssue", inType: "CloseIssueInput", input: map[string]any{"issueId": "ID1", "stateReason": "COMPLETED"}},
		{alias: "m1", field: "addComment", inType: "AddCommentInput", input: map[string]any{"subjectId": "ID2", "body": "hi"}},
		{alias: "m2", field: "reopenIssue", inType: "ReopenIssueInput", input: map[string]any{"issueId": "ID3"}},
	}
	failed, err := f.gh().Mutate(ms)
	if err != nil {
		t.Fatal(err)
	}
	q, vars := gqlBody(t, f.calls[0])
	eq(t, "query", q, "mutation($m0:CloseIssueInput!,$m1:AddCommentInput!,$m2:ReopenIssueInput!){"+
		"m0:closeIssue(input:$m0){clientMutationId} m1:addComment(input:$m1){clientMutationId} m2:reopenIssue(input:$m2){clientMutationId}}")
	eq(t, "vars", mustJSON(vars), `{"m0":{"issueId":"ID1","stateReason":"COMPLETED"},"m1":{"body":"hi","subjectId":"ID2"},"m2":{"issueId":"ID3"}}`)
	eq(t, "failed", mustJSON(failed), `["","issue is locked",""]`)
	// an alias with no result and no error of its own is a failure too
	f = &argvGH{handler: reply(`{"data":{"m0":{"clientMutationId":null}}}`)}
	failed, _ = f.gh().Mutate(ms[:2])
	if failed[0] != "" || !strings.HasPrefix(failed[1], "no result for m1") {
		t.Fatalf("%q", failed)
	}
	// nothing to send, no request
	f = &argvGH{handler: reply("")}
	f.gh().Mutate(nil)
	if len(f.calls) != 0 {
		t.Fatal("empty mutate sent a request")
	}
}

func TestCLIMilestoneRequests(t *testing.T) {
	f := &argvGH{handler: reply(`{"number":4,"title":"Beta","state":"open","due_on":"2026-12-31T07:00:00Z","description":"first"}`)}
	g := f.gh()
	ms, err := g.CreateMilestone("example/app", RemoteMilestone{Title: "Beta", DueOn: "2026-12-31", Description: "first"})
	if err != nil || ms.Number != 4 || ms.DueOn != "2026-12-31" || ms.State != "open" {
		t.Fatalf("%+v %v", ms, err)
	}
	c := f.calls[0]
	eq(t, "create argv", c.line(), "api -X POST repos/example/app/milestones --input -")
	// the date goes out at noon UTC, not midnight, which GitHub would read a day early
	eq(t, "create body", c.stdin, `{"description":"first","due_on":"2026-12-31T12:00:00Z","state":"open","title":"Beta"}`)
	g.CreateMilestone("example/app", RemoteMilestone{Title: "NoDue"})
	eq(t, "no due, no due_on", f.calls[1].stdin, `{"description":"","state":"open","title":"NoDue"}`)

	f.calls = nil
	title, due, state := "Gamma", "2027-01-02", "closed"
	g.UpdateMilestone("example/app", 4, MilestoneChange{Title: &title, Due: &due, State: &state})
	g.UpdateMilestone("example/app", 4, MilestoneChange{ClearDue: true})
	eq(t, "patch argv", f.calls[0].line(), "api -X PATCH repos/example/app/milestones/4 --input -")
	eq(t, "patch body", f.calls[0].stdin, `{"due_on":"2027-01-02T12:00:00Z","state":"closed","title":"Gamma"}`)
	eq(t, "clear due", f.calls[1].stdin, `{"due_on":null}`)

	f = &argvGH{handler: reply(`{"number":1,"title":"A","state":"open","due_on":null,"description":null}` + "\n\n" +
		`{"number":2,"title":"B","state":"closed","due_on":"2026-05-06T00:00:00Z","description":"d"}` + "\n")}
	list, err := f.gh().ListMilestones("example/app")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "list", mustJSON(list), `[{"Number":1,"Title":"A","State":"open","DueOn":"","Description":""},{"Number":2,"Title":"B","State":"closed","DueOn":"2026-05-06","Description":"d"}]`)
	if !strings.HasPrefix(f.calls[0].line(), "api repos/example/app/milestones?state=all&per_page=100 --paginate") {
		t.Fatalf("list argv: %q", f.calls[0].line())
	}
	f = &argvGH{handler: reply("garbage\n")}
	if _, err := f.gh().ListMilestones("example/app"); err == nil {
		t.Fatal("bad list output must be an error")
	}
}
