package issues

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeRepoGH serves the issues query of pull the way GitHub does: filterBy.since is inclusive,
// ASC by updatedAt, and the first page carries labels, milestones and PRs.
type fakeRepoGH struct {
	issues map[int]string // number -> updatedAt
	sinces []string       // the $since of every issues query
	delay  time.Duration
}

func (f *fakeRepoGH) node(n int) map[string]any {
	return map[string]any{"number": n, "title": fmt.Sprintf("t%d", n), "body": "b", "state": "OPEN", "stateReason": nil,
		"url": fmt.Sprintf("u%d", n), "createdAt": "2026-01-01T00:00:00Z", "updatedAt": f.issues[n], "id": fmt.Sprint("I", n),
		"author": nil, "milestone": nil, "assignees": map[string]any{"nodes": []any{}}, "labels": map[string]any{"nodes": []any{}},
		"comments": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}}
}

func (f *fakeRepoGH) handle(args []string, stdin string) (string, error) {
	time.Sleep(f.delay)
	var req struct {
		Query     string
		Variables map[string]any
	}
	json.Unmarshal([]byte(stdin), &req)
	if !strings.Contains(req.Query, "issues(") {
		return `{"data":{"repository":{"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}`, nil
	}
	since, _ := req.Variables["since"].(string)
	f.sinces = append(f.sinces, since)
	var nums []int
	for n, u := range f.issues {
		if since == "" || u >= since {
			nums = append(nums, n)
		}
	}
	sort.Slice(nums, func(i, j int) bool { return f.issues[nums[i]] < f.issues[nums[j]] })
	var nodes []any
	for _, n := range nums {
		nodes = append(nodes, f.node(n))
	}
	repo := map[string]any{"issues": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes}}
	if req.Variables["withMs"] == true {
		repo["labels"] = map[string]any{"nodes": []any{}}
		repo["milestones"] = map[string]any{"nodes": []any{}}
		repo["pullRequests"] = map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}
	}
	return mustJSON(map[string]any{"data": map[string]any{"repository": repo}}), nil
}

func newFakeRepo(n int) *fakeRepoGH {
	f := &fakeRepoGH{issues: map[int]string{}}
	for i := 1; i <= n; i++ {
		f.issues[i] = fmt.Sprintf("2026-02-%02dT00:00:00Z", 1+i%20)
	}
	return f
}

func TestDeltaPullTouchesOnlyChanged(t *testing.T) {
	e := newEnv(t)
	f := newFakeRepo(50)
	e.gh.handler = f.handle
	if out, _, code := e.run("pull", "--repo", "app"); code != 0 || out != "PULL app=50\n" {
		t.Fatalf("first pull: %q %d", out, code)
	}
	if e.gh.callCount() != 1 {
		t.Fatalf("first pull made %d gh calls, want 1 (issues, labels, milestones and PRs together)", e.gh.callCount())
	}
	// an unchanged file edited on disk: a delta must leave it alone, a full pull restores it
	p := filepath.Join(e.home, "app", "7.md")
	orig, _ := os.ReadFile(p)
	os.WriteFile(p, append(orig, "LOCAL\n"...), 0o644)
	f.issues[3], f.issues[4] = "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z"
	e.now = e.now.Add(time.Hour)
	before := e.gh.callCount()
	out, _, code := e.run("pull", "--repo", "app")
	eq(t, "delta pull", out, "PULL app=2\n")
	if code != 0 || e.gh.callCount()-before != 1 {
		t.Fatalf("delta pull: code %d, %d gh calls, want 1", code, e.gh.callCount()-before)
	}
	if f.sinces[len(f.sinces)-1] == "" {
		t.Fatal("delta pull sent no since")
	}
	if b, _ := os.ReadFile(p); !strings.HasSuffix(string(b), "LOCAL\n") {
		t.Fatal("delta pull rewrote an unchanged issue file")
	}
	idx := e.read("app/index.tsv")
	if !strings.Contains(idx, "\n4\topen\t\t\t2026-03-01T00:00:00Z\tt4\n") && !strings.HasPrefix(idx, "4\t") {
		t.Fatalf("index row of a changed issue not updated:\n%s", idx)
	}
	if len(strings.Split(strings.TrimSpace(idx), "\n")) != 50 {
		t.Fatal("index lost rows on a delta pull")
	}
	// --full re-reads everything and drops what GitHub no longer has
	delete(f.issues, 9)
	out, _, _ = e.run("pull", "--repo", "app", "--full")
	if f.sinces[len(f.sinces)-1] != "" || e.exists("app/9.md") {
		t.Fatalf("--full did not fetch everything: %q since=%q", out, f.sinces[len(f.sinces)-1])
	}
	if b, _ := os.ReadFile(p); strings.HasSuffix(string(b), "LOCAL\n") {
		t.Fatal("--full left the tampered file")
	}
}

func TestPullWithoutLastpullIsFull(t *testing.T) {
	e := newEnv(t)
	f := newFakeRepo(5)
	e.gh.handler = f.handle
	e.run("pull", "--repo", "app")
	os.Remove(filepath.Join(e.home, "app", "lastpull"))
	e.run("pull", "--repo", "app")
	if got := f.sinces[len(f.sinces)-1]; got != "" {
		t.Fatalf("pull without lastpull sent since=%q, want a full pull", got)
	}
}

// TestMeasurePull prints gh calls and time for a 50-issue pull with 2 changed (MOON_MEASURE=1).
func TestMeasurePull(t *testing.T) {
	if os.Getenv("MOON_MEASURE") == "" {
		t.Skip("set MOON_MEASURE=1")
	}
	e := newEnv(t)
	f := newFakeRepo(50)
	f.delay = 100 * time.Millisecond
	e.gh.handler = f.handle
	e.run("pull", "--repo", "app")
	f.issues[3], f.issues[4] = "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z"
	e.now = e.now.Add(time.Hour)
	c0, t0 := e.gh.callCount(), time.Now()
	e.run("pull", "--repo", "app")
	t.Logf("MEASURE pull 50 issues, 2 changed: %d gh calls, %v", e.gh.callCount()-c0, time.Since(t0))
}
