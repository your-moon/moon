package issues

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Dependencies live in the issue body as one line, `Depends on #3, #5`, so they survive a round trip through
// GitHub and read the same in the web UI. Only same-repo numbers count. The line must hold nothing but refs: a
// sentence such as "Depends on the auth service (#4)" is prose and blocks nothing.
var (
	dependsLineRe = regexp.MustCompile(`(?im)^[ \t]*depends on[ \t]+#\d+(?:[ \t]*(?:,|and|&)?[ \t]*#\d+)*[ \t]*\.?[ \t]*(?:\r?\n|$)`)
	depNumRe      = regexp.MustCompile(`#(\d+)`)
	localIDLineRe = regexp.MustCompile(`(?m)^<!-- local-id: [^>]*-->[ \t]*$`)
)

// dependsOn is the issue numbers the body says it depends on, in order, without repeats.
func dependsOn(body string) []int {
	var out []int
	seen := map[int]bool{}
	for _, line := range dependsLineRe.FindAllString(body, -1) {
		for _, m := range depNumRe.FindAllStringSubmatch(line, -1) {
			n, err := strconv.Atoi(m[1])
			if err == nil && n > 0 && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func depsLine(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return "Depends on " + strings.Join(parts, ", ")
}

// withDepends returns body with its Depends-on line set to nums (none removes it). An existing line is replaced
// where it stands; a new one goes above the `local-id` marker of a queued issue, else at the end.
func withDepends(body string, nums []int) string {
	first := dependsLineRe.FindStringIndex(body)
	if first == nil {
		if len(nums) == 0 {
			return body
		}
		if loc := localIDLineRe.FindStringIndex(body); loc != nil {
			return body[:loc[0]] + depsLine(nums) + "\n\n" + body[loc[0]:]
		}
		if pyStrip(body) == "" {
			return depsLine(nums)
		}
		return pyRstrip(body) + "\n\n" + depsLine(nums)
	}
	n := 0
	return dependsLineRe.ReplaceAllStringFunc(body, func(string) string {
		n++
		if n > 1 || len(nums) == 0 {
			return ""
		}
		return depsLine(nums) + "\n"
	})
}

// depNumbers turns the --depends-on values (#3, 3 or alias#3 of the same repo; "none" clears) into numbers.
// self is the issue's own number, 0 for one that has no number yet.
func (a *App) depNumbers(short string, vals []string, self int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "none" {
			if len(vals) > 1 {
				return nil, die("--depends-on none clears the list; do not combine it with refs")
			}
			return nil, nil
		}
		s := v
		if i := strings.Index(v, "#"); i >= 0 {
			if i > 0 && v[:i] != short {
				return nil, die("--depends-on %s: dependencies must be in the same repo (%s)", v, short)
			}
			s = v[i+1:]
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return nil, die("--depends-on %q: want an issue number such as #3 or %s#3", v, short)
		}
		if n == self {
			return nil, die("--depends-on %s: an issue cannot depend on itself", v)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// effectiveBody is the body of ref as show prints it: the mirror (or a queued `new`) with queued edits applied.
func (a *App) effectiveBody(ref, short, ident string) (string, error) {
	ops := a.loadOutbox()
	ref, ident = resolveLanded(a, ops, short, ref, ident)
	n, err := a.showNode(short, ident, ref, ops)
	if err != nil {
		return "", err
	}
	var pending []*Op
	for _, op := range ops {
		if op.Op != "new" && op.Ref == ref {
			pending = append(pending, op)
		}
	}
	e := &eff{body: n.Body}
	if err := applyOps(e, pending); err != nil {
		return "", err
	}
	return e.body, nil
}

// ---------- status: blocked by open dependencies ----------

// openDeps is the open issues among the dependencies in body. A dependency is open when the mirror says so,
// after queued close and reopen writes; one the mirror does not list is not counted, so a stale or mistyped
// number never blocks an issue for good.
func (w *workEnv) openDeps(short, body string) []int {
	var out []int
	for _, n := range dependsOn(body) {
		if w.isOpen(short, n) {
			out = append(out, n)
		}
	}
	return out
}

func (w *workEnv) isOpen(short string, n int) bool {
	if w.depOpen == nil {
		w.depOpen = map[string]map[int]bool{}
	}
	m, ok := w.depOpen[short]
	if !ok {
		m = map[int]bool{}
		for _, r := range w.a.readIndex(short) {
			if num, err := strconv.Atoi(r[0]); err == nil {
				m[num] = r[1] == "open"
			}
		}
		for _, op := range w.outbox() {
			if shortOf(op.Ref) != short || (op.Op != "close" && op.Op != "reopen") {
				continue
			}
			if num, err := strconv.Atoi(op.Ref[len(short)+1:]); err == nil {
				if _, listed := m[num]; listed {
					m[num] = op.Op == "reopen"
				}
			}
		}
		w.depOpen[short] = m
	}
	return m[n]
}

func (w *workEnv) outbox() []*Op {
	if !w.outboxRead {
		w.ops, w.outboxRead = w.a.overlayOps(), true
	}
	return w.ops
}

// mirrorBody is the body of an open mirrored issue, or "" when the file is absent. Most files have no Depends-on
// line, so the raw text is checked first and the file is parsed only when it might.
func (w *workEnv) mirrorBody(short string, num int) string {
	ident := strconv.Itoa(num)
	raw, err := w.a.mirror().Issues.Get(short, ident)
	if err != nil {
		if !isNotExist(err) {
			fmt.Fprintf(w.a.Err, "warning: dependencies of %s#%d not read: %v\n", short, num, err)
		}
		return ""
	}
	if !strings.Contains(strings.ToLower(raw), "depends on") {
		return ""
	}
	n, _, err := w.a.loadLocalIssue(short, ident)
	if err != nil {
		fmt.Fprintf(w.a.Err, "warning: dependencies of %s#%d not read: %v\n", short, num, err)
		return ""
	}
	return n.Body
}

// editDepends applies --depends-on to an edit. The Depends-on line is part of the body, so the body is
// rewritten: the one given with --body, else the current one (mirror plus queued edits).
func (a *App) editDepends(ref, body string, has bool, vals []string) (string, bool, error) {
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return "", false, err
	}
	self, _ := strconv.Atoi(ident)
	nums, err := a.depNumbers(short, vals, self)
	if err != nil {
		return "", false, err
	}
	if !has {
		if body, err = a.effectiveBody(ref, short, ident); err != nil {
			return "", false, err
		}
	}
	return withDepends(body, nums), true, nil
}
