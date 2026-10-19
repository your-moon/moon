package issues

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// hub is the milestone side of the in-memory GitHub: tests seed milestones and read back what was sent, in order.
type hub struct{ m *memGH }

const hubRepo = "example/app"

func newHub(e *env) *hub { return &hub{m: e.useMem()} }

func (h *hub) seed(title, due, desc string) RemoteMilestone {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	r := h.m.repo(hubRepo)
	ms := RemoteMilestone{Number: len(r.milestones) + 1, Title: title, State: "open", DueOn: due, Description: desc}
	r.milestones = append(r.milestones, ms)
	return ms
}

func (h *hub) ms() []RemoteMilestone {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	return append([]RemoteMilestone(nil), h.m.repo(hubRepo).milestones...)
}

func (h *hub) byTitle(t string) *RemoteMilestone {
	for _, m := range h.ms() {
		if m.Title == t {
			return &m
		}
	}
	return nil
}

// log is what reached GitHub: milestone creates and patches, and issue creates with their milestone, in order.
func (h *hub) log() []string {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	var out []string
	k := 0
	for _, l := range h.m.log {
		if l == "create" {
			out = append(out, "issue create milestone="+h.m.created[k].Milestone)
			k++
		} else if strings.Contains(l, " milestone ") {
			out = append(out, l)
		}
	}
	return out
}

func (h *hub) count(prefix string) int {
	n := 0
	for _, l := range h.log() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (h *hub) index(prefix string) int {
	for i, l := range h.log() {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

func msEnv(t *testing.T) (*env, *hub) {
	e := newEnv(t)
	e.tick = true
	return e, newHub(e)
}

func must0(t *testing.T, name string, code int, errs string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("%s: exit %d: %s", name, code, errs)
	}
}

func TestMsCreateThenSyncOrder(t *testing.T) {
	e, h := msEnv(t)
	out, errs, code := e.run("milestones", "new", "Beta", "-d", "2026-12-31", "-D", "first public release", "-R", "app")
	must0(t, "new", code, errs)
	contains0(t, "new", out, "milestone app#M-1 \"Beta\"")
	_, errs, code = e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-m", "M-1")
	must0(t, "issue", code, errs)
	out, errs, code = e.run("sync", "-R", "app")
	must0(t, "sync", code, errs)
	contains0(t, "sync", out, "pushed=2 failed=0")
	if h.index("POST milestone Beta") < 0 || h.index("issue create milestone=Beta") < h.index("POST milestone Beta") {
		t.Fatalf("milestone must be created before the issue: %v", h.log())
	}
	m := h.byTitle("Beta")
	if m == nil || m.DueOn != "2026-12-31" || m.Description != "first public release" {
		t.Fatalf("remote milestone: %+v", m)
	}
	contains0(t, "localmap", e.localmap(), "app\tM-1\t1\n")
	eq(t, "milestones.tsv", e.getFile("app", fileMilestones), "1\tM-1\tBeta\topen\t2026-12-31\tfirst public release\t2026-03-01T00:00:00Z\n")
	out, _, _ = e.run("milestones", "-R", "app", "--format", "tsv")
	eq(t, "milestones", out, "app\tBeta\topen\t0\t1\t2026-12-31\n") // the issue created with it counts
}

// An issue op that was queued before the milestone op must still wait for the milestone.
func TestMsIssueQueuedFirstWaitsForMilestone(t *testing.T) {
	e, h := msEnv(t)
	a := e.app()
	if _, _, err := a.enqueue("new", "app#L-1", map[string]any{"title": "t", "body": "b", "labels": []any{}, "local_id": "L-1", "milestone": "Beta"}); err != nil {
		t.Fatal(err)
	}
	_, errs, code := e.run("milestones", "new", "Beta", "-R", "app")
	must0(t, "new", code, errs)
	_, errs, code = e.run("sync", "-R", "app")
	must0(t, "sync", code, errs)
	i, j := h.index("POST milestone Beta"), h.index("issue create milestone=Beta")
	if i < 0 || j < 0 || j < i {
		t.Fatalf("milestone must come first: %v", h.log())
	}
}

// If the milestone cannot be created, no issue is pointed at it; the next sync does both.
func TestMsFailedCreateHoldsBackIssues(t *testing.T) {
	e, h := msEnv(t)
	e.run("milestones", "new", "Beta", "-R", "app")
	e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-m", "Beta")
	h.m.msFailCreate = true
	out, _, code := e.run("sync", "-R", "app")
	if code != 1 || !strings.Contains(out, "failed=1") {
		t.Fatalf("want failure: %d %q", code, out)
	}
	if h.count("issue create") != 0 {
		t.Fatalf("issue sent without its milestone: %v", h.log())
	}
	h.m.msFailCreate = false
	_, errs, code := e.run("sync", "-R", "app")
	must0(t, "retry", code, errs)
	if h.count("POST milestone") != 2 || h.count("issue create milestone=Beta") != 1 || h.index("issue create") < h.index("POST milestone Beta") {
		t.Fatalf("retry: %v", h.log())
	}
}

// crashRecord makes the step after the create fail (the localmap cannot be written), then lets it work.
func crashRecord(e *env) func() { return e.blockLocalmap() }

func TestMsIdempotentRetryAfterCrash(t *testing.T) {
	e, h := msEnv(t)
	e.run("milestones", "new", "Beta", "-d", "2026-06-01", "-R", "app")
	fix := crashRecord(e) // GitHub made it; we died before writing it down
	_, _, code := e.run("sync", "-R", "app")
	if code != 1 || len(h.ms()) != 1 {
		t.Fatalf("first sync should fail after the create, got %d, %d milestones", code, len(h.ms()))
	}
	fix()
	_, errs, code := e.run("sync", "-R", "app")
	must0(t, "retry", code, errs)
	if len(h.ms()) != 1 || h.count("POST milestone") != 1 {
		t.Fatalf("milestone created twice: %v", h.log())
	}
	eq(t, "localmap", e.localmap(), "app\tM-1\t1\n")
	e.run("sync", "-R", "app") // a recorded create is never repeated
	if h.count("POST milestone") != 1 {
		t.Fatalf("repeat: %v", h.log())
	}
}

// The answer to the create is lost: the same run looks the title up again and adopts it.
func TestMsLostAnswerIsAdopted(t *testing.T) {
	e, h := msEnv(t)
	e.run("milestones", "new", "Beta", "-R", "app")
	_, errs, code := e.run("sync", "-R", "app")
	must0(t, "sync", code, errs)
	if len(h.ms()) != 1 || h.count("POST milestone") != 1 {
		t.Fatalf("%v", h.log())
	}
	eq(t, "localmap", e.localmap(), "app\tM-1\t1\n")
}

func TestMsAdoptsExistingRemoteTitle(t *testing.T) {
	e, h := msEnv(t)
	h.seed("Beta", "", "set on GitHub")
	h.seed("Other", "", "")
	e.run("milestones", "new", "Beta", "-d", "2026-06-01", "-D", "mine", "-R", "app")
	e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-m", "M-1")
	_, errs, code := e.run("sync", "-R", "app")
	must0(t, "sync", code, errs)
	if h.count("POST milestone") != 0 || len(h.ms()) != 2 {
		t.Fatalf("a title that exists must be adopted: %v", h.log())
	}
	m := h.byTitle("Beta")
	if m.Description != "set on GitHub" || m.DueOn != "2026-06-01" {
		t.Fatalf("adopt fills only what GitHub lacks: %+v", m)
	}
	contains0(t, "localmap", e.localmap(), "app\tM-1\t1\n")
	if h.count("issue create milestone=Beta") != 1 {
		t.Fatal(h.log())
	}
}

func TestMsCaseOnlyTitleGetsOpMarker(t *testing.T) {
	e, h := msEnv(t)
	h.m.msCaseTolerant = true // GitHub here tolerates a title that differs only in case
	h.seed("beta", "", "")
	e.run("milestones", "new", "Beta", "-D", "d", "-R", "app")
	fix := crashRecord(e)
	e.run("sync", "-R", "app")
	fix()
	_, errs, code := e.run("sync", "-R", "app")
	must0(t, "retry", code, errs)
	if h.count("POST milestone") != 1 || len(h.ms()) != 2 {
		t.Fatalf("the marker must let the retry find its own milestone: %v", h.log())
	}
	if !strings.Contains(h.ms()[1].Description, "<!-- moon-op: ") {
		t.Fatalf("marker missing: %q", h.ms()[1].Description)
	}
	if got := e.getFile("app", fileMilestones); strings.Contains(got, "moon-op") {
		t.Fatalf("marker leaked into the mirror: %q", got)
	}
}

func TestMsDuplicateTitleRefused(t *testing.T) {
	e, _ := msEnv(t)
	_, errs, code := e.run("milestones", "new", "Beta", "-R", "app")
	must0(t, "first", code, errs)
	for _, title := range []string{"Beta", "beta", "  Beta "} {
		_, errs, code = e.run("milestones", "new", title, "-R", "app")
		if code != 2 || !strings.Contains(errs, "already exists in app") || !strings.Contains(errs, "M-1") {
			t.Fatalf("%q: exit %d: %q", title, code, errs)
		}
	}
	// a title the issues already use counts too, with a hint
	e.putFile("app", fileIndex, "1\topen\tOnlyIssues\t\t2026-01-01T00:00:00Z\tx\n")
	_, errs, code = e.run("milestones", "new", "OnlyIssues", "-R", "app")
	if code != 2 || !strings.Contains(errs, "milestones edit") {
		t.Fatalf("derived: %d %q", code, errs)
	}
	// renaming onto an existing title
	e.run("milestones", "new", "Gamma", "-R", "app")
	_, errs, code = e.run("milestones", "edit", "Gamma", "--title", "beta", "-R", "app")
	if code != 2 || !strings.Contains(errs, "already exists") {
		t.Fatalf("rename: %d %q", code, errs)
	}
	if n := len(e.app().loadOutbox()); n != 2 {
		t.Fatalf("refused commands must queue nothing, outbox has %d", n)
	}
}

func TestMsDueValidation(t *testing.T) {
	e, _ := msEnv(t)
	for _, bad := range []string{"2026-13-01", "tomorrow", "2026-2-3", "2026-02-30", "", "2026-01-01T00:00:00Z", "none"} {
		_, errs, code := e.run("milestones", "new", "X", "-d", bad, "-R", "app")
		if code != 2 || !strings.Contains(errs, "YYYY-MM-DD") {
			t.Errorf("new -d %q: exit %d: %q", bad, code, errs)
		}
	}
	if len(e.app().loadOutbox()) != 0 {
		t.Fatal("a bad date must queue nothing")
	}
	e.run("milestones", "new", "X", "-d", "2026-02-28", "-R", "app")
	_, errs, code := e.run("milestones", "edit", "X", "-d", "2026-02-29", "-R", "app")
	if code != 2 || !strings.Contains(errs, "none to clear") {
		t.Fatalf("edit: %d %q", code, errs)
	}
	_, errs, code = e.run("milestones", "edit", "X", "-d", "none", "-R", "app")
	must0(t, "none", code, errs)
	out, _, _ := e.run("milestones", "-R", "app", "-j")
	contains0(t, "cleared due", out, `"due_on": null`)
}

func TestMsEditCloseReopenSync(t *testing.T) {
	e, h := msEnv(t)
	e.run("milestones", "new", "Beta", "-d", "2026-06-01", "-R", "app")
	// edit and close a milestone that is still local, through its M-n
	_, errs, code := e.run("milestones", "edit", "M-1", "-d", "2026-07-01", "-D", "later", "-R", "app")
	must0(t, "edit M-1", code, errs)
	out, _, _ := e.run("milestones", "-R", "app", "-S", "all", "--format", "tsv")
	eq(t, "pending view", out, "app\tBeta\topen\t0\t0\t2026-07-01\n")
	_, errs, code = e.run("sync", "-R", "app")
	must0(t, "sync 1", code, errs)
	m := h.byTitle("Beta")
	if m == nil || m.DueOn != "2026-07-01" || m.Description != "later" {
		t.Fatalf("after first sync: %+v", m)
	}
	// now a milestone GitHub knows by number
	for _, c := range [][]string{
		{"edit", "Beta", "--title", "Gamma", "-d", "none"},
		{"close", "Gamma"},
		{"reopen", "Gamma"},
		{"close", "Gamma"},
	} {
		_, errs, code := e.run(append(append([]string{"milestones"}, c...), "-R", "app")...)
		must0(t, strings.Join(c, " "), code, errs)
	}
	out, _, _ = e.run("milestones", "-R", "app", "-S", "all", "--format", "tsv")
	eq(t, "pending view", out, "app\tGamma\tclosed\t0\t0\t\n")
	out, errs, code = e.run("sync", "-R", "app")
	must0(t, "sync 2", code, errs)
	contains0(t, "sync", out, "pushed=4 failed=0")
	m = h.byTitle("Gamma")
	if m == nil || m.State != "closed" || m.DueOn != "" || m.Number != 1 {
		t.Fatalf("after second sync: %+v", m)
	}
	out, _, _ = e.run("milestones", "-R", "app", "-S", "all", "--format", "tsv")
	eq(t, "mirror", out, "app\tGamma\tclosed\t0\t0\t\n")
	// closing what is already closed is a no-op, not an op
	out, _, code = e.run("milestones", "close", "Gamma", "-R", "app")
	contains0(t, "already", out, "already closed")
	if len(e.app().loadOutbox()) != 0 {
		t.Fatal("no-op queued something")
	}
}

func TestMsRenameFollowsIssues(t *testing.T) {
	e, h := msEnv(t)
	h.seed("Beta", "", "")
	e.putFile("app", fileIndex, "4\topen\tBeta\t\t2026-01-01T00:00:00Z\tt\n")
	e.putIssue("app", "4", "---\nnumber: 4\ntitle: \"t\"\nstate: \"open\"\nstate_reason: null\nlabels: []\nmilestone: \"Beta\"\nassignees: []\nauthor: \"x\"\ncreated_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\nurl: \"u\"\n---\n\nbody\n\n## Comments\n")
	e.run("milestones", "edit", "Beta", "--title", "Final", "-R", "app")
	out, _, _ := e.run("milestones", "-R", "app", "--format", "tsv")
	eq(t, "overlay", out, "app\tFinal\topen\t0\t1\t\n")
	_, errs, code := e.run("push", "--repo", "app")
	must0(t, "push", code, errs)
	eq(t, "index after push", e.getFile("app", fileIndex), "4\topen\tFinal\t\t2026-01-01T00:00:00Z\tt\n")
	if h.byTitle("Final") == nil {
		t.Fatal(h.log())
	}
}

func TestMsLocalOnly(t *testing.T) {
	e, h := msEnv(t)
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.notes]\nlocal = true\n"), 0o644)
	h.m.hook = func(call, repo, ident string) error {
		t.Errorf("a local-only repo must not reach GitHub: %s %s", call, repo)
		return &GhError{Msg: "must not be called"}
	}
	out, errs, code := e.run("milestones", "new", "Q1", "-d", "2026-03-31", "-D", "plan\nsecond\tline")
	must0(t, "new", code, errs)
	eq(t, "out", out, "milestone notes \"Q1\"  (#1)\n")
	out, _, _ = e.run("milestones", "new", "Q2")
	eq(t, "out", out, "milestone notes \"Q2\"  (#2)\n")
	eq(t, "file", e.getFile("notes", fileMilestones), "1\t\tQ1\topen\t2026-03-31\tplan\\nsecond\\tline\t2026-03-01T12:00:00Z\n2\t\tQ2\topen\t\t\t2026-03-01T12:00:00Z\n")
	e.run("new", "--repo", "notes", "--title", "a", "--body", "b", "-m", "Q1")
	e.run("new", "--repo", "notes", "--title", "b", "--body", "b", "--milestone", "Q1")
	out, _, _ = e.run("milestones", "--format", "tsv")
	eq(t, "counts", out, "notes\tQ1\topen\t0\t2\t2026-03-31\nnotes\tQ2\topen\t0\t0\t\n")
	_, errs, code = e.run("milestones", "edit", "Q1", "--title", "Quarter 1", "-d", "2026-04-15")
	must0(t, "edit", code, errs)
	out, _, _ = e.run("ls", "--repo", "notes", "-m", "Quarter 1", "--format", "tsv")
	if strings.Count(out, "Quarter 1") != 2 {
		t.Fatalf("issues must follow the rename:\n%s", out)
	}
	e.run("milestones", "close", "Q2")
	out, _, _ = e.run("milestones", "-S", "closed", "--format", "tsv")
	eq(t, "closed", out, "notes\tQ2\tclosed\t0\t0\t\n")
	// the issue-derived title materializes on first edit and gets the next number
	e.run("new", "--repo", "notes", "--title", "c", "--body", "b", "-m", "Free")
	_, errs, code = e.run("milestones", "edit", "Free", "-d", "2026-09-09")
	must0(t, "derived edit", code, errs)
	contains0(t, "numbered", e.getFile("notes", fileMilestones), "3\t\tFree\topen\t2026-09-09\t")
	out, _, _ = e.run("milestones", "show", "Quarter 1", "--format", "table")
	contains0(t, "show", out, "due 2026-04-15")
}

func TestMsNumbersNeverReused(t *testing.T) {
	e, _ := msEnv(t)
	newN := func(title string) string {
		out, errs, code := e.run("milestones", "new", title, "-R", "app")
		must0(t, title, code, errs)
		return strings.Fields(out)[1]
	}
	eq(t, "1", newN("a"), "app#M-1")
	eq(t, "2", newN("b"), "app#M-2")
	if _, errs, code := e.run("sync", "-R", "app"); code != 0 {
		t.Fatal(errs)
	}
	if n := len(e.app().loadOutbox()); n != 0 {
		t.Fatalf("outbox not drained: %d", n)
	}
	eq(t, "3 after the outbox drained", newN("c"), "app#M-3")
	// a lost counter is rebuilt from localmap, outbox and the file; never restarted
	e.rawRemove("app/local-mseq")
	eq(t, "4 after losing the counter", newN("d"), "app#M-4")
	// M-n resolves to the real number after the sync
	e.run("sync", "-R", "app")
	out, _, _ := e.run("milestones", "edit", "M-1", "-D", "x", "-R", "app")
	contains0(t, "edit by M-n after sync", out, "milestone app \"a\"")
	// unknown ones say so
	_, errs, code := e.run("milestones", "close", "M-9", "-R", "app")
	if code != 2 || !strings.Contains(errs, "M-9") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestMsConcurrentNewFromProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "moon")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/moon").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	e := newEnv(t)
	const procs, per = 4, 8
	results := make(chan string, procs*per)
	var wg sync.WaitGroup
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				cmd := exec.Command(bin, "issues", "--config", e.cfg, "milestones", "new", fmt.Sprintf("m-%d-%d", p, i), "-R", "app")
				cmd.Env = append(os.Environ(), "ISSUES_NO_AUTOSYNC=1")
				out, err := cmd.Output()
				if err != nil {
					t.Errorf("new: %v", err)
					return
				}
				results <- string(out)
			}
		}(p)
	}
	wg.Wait()
	close(results)
	var got []int
	for out := range results {
		f := strings.Fields(out)
		n, err := strconv.Atoi(strings.TrimPrefix(f[1], "app#M-"))
		if err != nil {
			t.Fatalf("bad output %q", out)
		}
		got = append(got, n)
	}
	sort.Ints(got)
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("ids are not distinct and contiguous: %v", got)
		}
	}
	if len(got) != procs*per {
		t.Fatalf("got %d results", len(got))
	}
}

func TestMsUsableAsTitleEverywhere(t *testing.T) {
	e, _ := msEnv(t)
	e.run("milestones", "new", "Beta", "-R", "app")
	out, errs, code := e.run("new", "--repo", "app", "--title", "t1", "--body", "b", "--milestone", "M-1")
	must0(t, "new", code, errs)
	ref := strings.Fields(out)[3]
	out, _, _ = e.run("ls", "--repo", "app", "-m", "M-1", "--format", "tsv")
	contains0(t, "ls -m M-1", out, "t1")
	out, _, _ = e.run("ls", "--repo", "app", "-m", "Beta", "--format", "tsv")
	contains0(t, "ls -m Beta", out, "t1")
	e.run("new", "--repo", "app", "--title", "t2", "--body", "b")
	_, errs, code = e.run("milestone", "app#L-2", "M-1")
	must0(t, "milestone", code, errs)
	out, _, _ = e.run("milestones", "-R", "app", "--format", "tsv")
	eq(t, "counts", out, "app\tBeta\topen\t0\t2\t\n")
	out, _, _ = e.run("milestones", "show", "M-1", "-R", "app", "--format", "table")
	contains0(t, "show", out, "M-1 (queued, not on GitHub yet)")
	_ = ref
	// a repo prefix is not needed when the milestone is unambiguous
	_, errs, code = e.run("milestones", "close", "bet")
	must0(t, "close by prefix across repos", code, errs)
}

func TestMsShowAndTableAndEscaping(t *testing.T) {
	e, _ := msEnv(t)
	desc := "line one\nline\ttwo \\ back"
	e.run("milestones", "new", "Beta", "-d", "2026-12-31", "-D", desc, "-R", "app")
	e.putFile("app", fileIndex, "1\topen\tBeta\t\t2026-01-01T00:00:00Z\tx\n2\tclosed\tBeta\t\t2026-01-01T00:00:00Z\ty\n")
	out, _, _ := e.run("milestones", "show", "Beta", "-R", "app", "--format", "table")
	contains0(t, "counts", out, "1/2 closed · 50% · 1 open · due 2026-12-31")
	contains0(t, "description", out, desc)
	out, _, _ = e.run("milestones", "-R", "app", "-j")
	contains0(t, "json", out, `"description": "line one\nline\ttwo \\ back"`)
	// the file keeps one description per line and round-trips
	e.putFile("app", fileMilestones, (&msRec{Number: 3, Title: "Z", State: "open", Desc: desc}).row()+"\n")
	if n := strings.Count(e.getFile("app", fileMilestones), "\n"); n != 1 {
		t.Fatalf("description broke the row")
	}
	recs := e.app().readMsFile("app")
	if len(recs) != 1 || recs[0].Desc != desc || recs[0].Number != 3 {
		t.Fatalf("round trip: %+v", recs)
	}
}

func TestMsPullKeepsQueuedAndReadsLegacyRows(t *testing.T) {
	e, h := msEnv(t)
	h.seed("Remote", "2026-05-05", "from GitHub")
	e.run("milestones", "new", "Local", "-R", "app")
	_, errs, code := e.run("pull", "--repo", "app")
	must0(t, "pull", code, errs)
	out, _, _ := e.run("milestones", "-R", "app", "--format", "tsv")
	eq(t, "queued stays visible after a pull", out, "app\tRemote\topen\t0\t0\t2026-05-05\napp\tLocal\topen\t0\t0\t\n")
	e.putFile("lib", fileMilestones, "v1\topen\t3\t4\t2026-04-01T00:00:00Z\n")
	out, _, _ = e.run("milestones", "-R", "lib", "--format", "tsv")
	eq(t, "legacy row", out, "lib\tv1\topen\t3\t4\t2026-04-01T00:00:00Z\n")
}

func TestMsHelpHasTwoExamples(t *testing.T) {
	e, _ := msEnv(t)
	for _, c := range []string{"new", "edit", "close", "reopen"} {
		out, _, code := e.run("milestones", c, "--help")
		if code != 0 || strings.Count(out[strings.Index(out, "examples:"):], "\n  moon issues milestones") != 2 {
			t.Errorf("%s --help: %d\n%s", c, code, out)
		}
		out2, _, _ := e.run("help", "milestones", c)
		eq(t, "help "+c, out2, out)
	}
	out, _, _ := e.run("milestones", "new", "--help")
	for _, w := range []string{"-d, --due", "-D, --description", "--description-file", "-R, --repo"} {
		contains0(t, "flags", out, w)
	}
	out, _, _ = e.run("--help")
	contains0(t, "usage", out, "milestones new|edit|close|reopen")
}

func TestMsDescriptionFileAndStdin(t *testing.T) {
	e, _ := msEnv(t)
	f := filepath.Join(t.TempDir(), "d.md")
	os.WriteFile(f, []byte("from file\n\nmore\n"), 0o644)
	_, errs, code := e.run("milestones", "new", "A", "--description-file", f, "-R", "app")
	must0(t, "file", code, errs)
	_, _, code = e.runIn("from stdin\n", "milestones", "new", "B", "--description-file", "-", "-R", "app")
	must0(t, "stdin", code, "")
	_, errs, code = e.run("milestones", "new", "C", "-D", "x", "--description-file", f, "-R", "app")
	if code != 2 || !strings.Contains(errs, "not both") {
		t.Fatalf("%d %q", code, errs)
	}
	ops := e.app().loadOutbox()
	if argStr(ops[0].Args, "description") != "from file\n\nmore" || argStr(ops[1].Args, "description") != "from stdin" {
		t.Fatalf("%v %v", ops[0].Args, ops[1].Args)
	}
}

func TestMsSingleRepoDefaultAndNeedsRepo(t *testing.T) {
	e, _ := msEnv(t)
	_, errs, code := e.run("milestones", "new", "A")
	if code != 2 || !strings.Contains(errs, "pass --repo") || !strings.Contains(errs, "app, lib") {
		t.Fatalf("%d %q", code, errs)
	}
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n"), 0o644)
	_, errs, code = e.run("milestones", "new", "A")
	must0(t, "single repo", code, errs)
}
