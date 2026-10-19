package issues

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeQL is GitHub's GraphQL side for push: it resolves issues, labels and milestones to ids, and applies
// aliased mutations in document order, logging "field issue#N" per applied mutation. Issues in failIssue fail
// their mutations with an error that names the alias, the way GitHub does.
type fakeQL struct {
	mu        sync.Mutex
	delay     time.Duration
	failIssue map[string]string // node id -> error message
	reqs      int               // graphql requests
	queries   int
	muts      int
	log       []string
	rest      []string // non-graphql calls
	nextNew   int
}

var (
	issueAliasRe = regexp.MustCompile(`i(\d+):issue\(number:(\d+)\)`)
	labelVarRe   = regexp.MustCompile(`(l\d+):label\(name:\$(l\d+)\)`)
	mutRe        = regexp.MustCompile(`(m\d+\w):(\w+)\(input:\$(m\d+\w)\)`)
)

func (f *fakeQL) handle(args []string, stdin string) (string, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if args[0] == "api" && args[1] == "graphql" {
		f.reqs++
		var req struct {
			Query     string
			Variables map[string]any
		}
		json.Unmarshal([]byte(stdin), &req)
		if strings.HasPrefix(req.Query, "query") {
			f.queries++
			repo := map[string]any{}
			for _, m := range issueAliasRe.FindAllStringSubmatch(req.Query, -1) {
				repo["i"+m[1]] = map[string]any{"id": "ID" + m[2]}
			}
			for _, m := range labelVarRe.FindAllStringSubmatch(req.Query, -1) {
				if name, _ := req.Variables[m[2]].(string); name != "nolabel" {
					repo[m[1]] = map[string]any{"id": "LB-" + name}
				} else {
					repo[m[1]] = nil
				}
			}
			if strings.Contains(req.Query, "milestones(") {
				repo["milestones"] = map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{map[string]any{"id": "MS-v1", "title": "v1"}}}
			}
			return mustJSON(map[string]any{"data": map[string]any{"repository": repo}}), nil
		}
		f.muts += len(mutRe.FindAllString(req.Query, -1))
		data := map[string]any{}
		var errs []any
		for _, m := range mutRe.FindAllStringSubmatch(req.Query, -1) {
			in, _ := req.Variables[m[3]].(map[string]any)
			id := ""
			for _, k := range []string{"subjectId", "issueId", "labelableId", "id"} {
				if s, ok := in[k].(string); ok {
					id = s
				}
			}
			if msg, bad := f.failIssue[id]; bad {
				data[m[1]] = nil
				errs = append(errs, map[string]any{"message": msg, "path": []any{m[1]}})
				continue
			}
			data[m[1]] = map[string]any{"clientMutationId": nil}
			f.log = append(f.log, m[2]+" "+id)
		}
		out := map[string]any{"data": data}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return mustJSON(out), nil
	}
	f.rest = append(f.rest, strings.Join(args[:2], " "))
	switch {
	case args[0] == "issue" && args[1] == "create":
		f.nextNew++
		f.log = append(f.log, "create")
		return fmt.Sprintf("https://github.com/example/app/issues/%d\n", 100+f.nextNew), nil
	case args[0] == "issue" && args[1] == "list":
		return "[]", nil
	case args[0] == "api":
		return "", nil
	case args[0] == "issue" && len(args) > 2:
		f.log = append(f.log, "rest-"+args[1]+" "+args[2])
		return "", nil
	}
	return "", &GhError{Msg: "fakeQL: unexpected " + strings.Join(args, " ")}
}

func TestPushBatchesIndependentWrites(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := &fakeQL{}
	e.gh.handler = f.handle
	e.gh.parallel = true
	for i := 1; i <= 5; i++ {
		e.run("close", fmt.Sprintf("app#%d", i), "--comment", "done")
	}
	for i := 6; i <= 9; i++ {
		e.run("comment", fmt.Sprintf("app#%d", i), "--body", "hello")
	}
	e.run("label", "app#10", "--add", "bug", "--remove", "old")
	e.run("edit", "app#11", "--title", "new title", "--body", "new body")
	e.run("reopen", "app#12")
	e.run("milestone", "app#13", "v1")
	e.run("milestone", "app#14", "none")
	out, _, code := e.run("push")
	if code != 0 || out != "PUSH pushed=14 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	if f.reqs != 2 || f.queries != 1 || len(f.rest) != 0 {
		t.Fatalf("graphql requests %d (queries %d), REST calls %v; want 1 resolve + 1 mutate, no REST", f.reqs, f.queries, f.rest)
	}
	got := strings.Join(f.log, ",")
	for _, want := range []string{"closeIssue ID1", "addComment ID5", "addComment ID6", "addLabelsToLabelable ID10", "removeLabelsFromLabelable ID10",
		"updateIssue ID11", "reopenIssue ID12", "updateIssue ID13", "updateIssue ID14"} {
		if !strings.Contains(got, want) {
			t.Fatalf("mutation %q missing from %s", want, got)
		}
	}
	if n, _ := os.ReadDir(e.home + "/.outbox"); len(n) != 0 {
		t.Logf("outbox dir has %d entries", len(n))
	}
	if left := e.app().loadOutbox(); len(left) != 0 {
		t.Fatalf("%d ops left queued", len(left))
	}
}

func TestPushBatchBatches20AtATime(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := &fakeQL{}
	e.gh.handler = f.handle
	e.gh.parallel = true
	for i := 1; i <= 45; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	out, _, _ := e.run("push")
	if out != "PUSH pushed=45 failed=0\n" {
		t.Fatalf("%q", out)
	}
	if f.muts != 45 || f.reqs > 6 { // 3 mutation requests of at most 20, each with at most one resolve
		t.Fatalf("%d mutations in %d requests", f.muts, f.reqs)
	}
}

// A write that fails stays queued with its error, the rest of the batch lands, and the failed issue's next write
// is not sent in the same push.
func TestPushBatchFailureIsPerOpAndKeepsIssueOrder(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := &fakeQL{failIssue: map[string]string{"ID2": "Something went wrong while executing your query"}}
	e.gh.handler = f.handle
	e.gh.parallel = true
	e.run("edit", "app#2", "--title", "first")
	for i := 1; i <= 4; i++ {
		if i != 2 {
			e.run("reopen", fmt.Sprintf("app#%d", i))
		}
	}
	e.run("close", "app#2")
	out, _, _ := e.run("push")
	eq(t, "push", out, "PUSH pushed=3 failed=1\n")
	for _, l := range f.log {
		if l == "closeIssue ID2" {
			t.Fatalf("the close ran although the edit before it failed: %v", f.log)
		}
	}
	left := e.app().loadOutbox()
	if len(left) != 2 || left[0].Op != "edit" || left[0].LastError == nil || !strings.Contains(*left[0].LastError, "Something went wrong") || left[1].Op != "close" || left[1].Attempts != 0 {
		t.Fatalf("queue after a failed edit: %d ops %+v", len(left), left)
	}
}

func TestPushBatchPermanentErrorStopsRetrying(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := &fakeQL{failIssue: map[string]string{"ID1": "Could not resolve to a Repository with the name 'example/app'."}}
	e.gh.handler = f.handle
	e.gh.parallel = true
	for i := 1; i <= 3; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	e.run("push")
	left := e.app().loadOutbox()
	if len(left) != 1 || !left[0].Permanent {
		t.Fatalf("want the one failed op marked permanent, got %+v", left)
	}
}

// A new issue is created over REST; the writes queued on it follow in a later round with its real number.
func TestPushBatchNewBeforeItsEdits(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := &fakeQL{}
	e.gh.handler = f.handle
	e.gh.parallel = true
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	ref := refOf(t, out)
	e.run("comment", ref, "--body", "c")
	e.run("label", ref, "--add", "x")
	for i := 1; i <= 3; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	out, _, _ = e.run("push")
	eq(t, "push", out, "PUSH pushed=6 failed=0\n")
	got := strings.Join(f.log, ",")
	ci, co, ce := strings.Index(got, "create"), strings.Index(got, "rest-comment 101"), strings.Index(got, "rest-edit 101")
	if ci < 0 || co < ci || ce < co || strings.Count(got, "reopenIssue") != 3 {
		t.Fatalf("order of effects: %s", got)
	}
}

// MeasurePush: 17 writes, 100 ms per gh call. MOON_MEASURE=1 go test ./internal/issues -run MeasurePush -v
func TestMeasurePush(t *testing.T) {
	if os.Getenv("MOON_MEASURE") == "" {
		t.Skip("set MOON_MEASURE=1")
	}
	for _, mode := range []string{"rest", "batch", "rest-independent", "batch-independent"} {
		e := newEnv(t)
		e.tick = true
		e.gh.parallel = true
		f := &fakeQL{delay: 100 * time.Millisecond}
		e.gh.handler = f.handle
		if strings.HasPrefix(mode, "rest") {
			e.gh.handler = (&latencyGH{}).handler(100*time.Millisecond, nil)
			restPush(t)
		} else {
			pushBatch = 20
		}
		if strings.HasSuffix(mode, "independent") { // 17 writes on 17 issues
			for i := 1; i <= 17; i++ {
				e.run("comment", fmt.Sprintf("app#%d", i), "--body", "c")
			}
		} else { // 12 closes, a new issue with 3 writes behind it, a label behind a close
			for i := 1; i <= 12; i++ {
				e.run("close", fmt.Sprintf("app#%d", i))
			}
			out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
			ref := refOf(t, out)
			e.run("comment", ref, "--body", "c")
			e.run("label", ref, "--add", "x")
			e.run("close", ref)
			e.run("label", "app#1", "--add", "y")
		}
		c0, t0 := e.gh.callCount(), time.Now()
		e.run("push")
		t.Logf("MEASURE push 17 writes (%s): %d gh calls, %v", mode, e.gh.callCount()-c0, time.Since(t0).Round(10*time.Millisecond))
	}
}
