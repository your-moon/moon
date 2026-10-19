package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// The per-alias counter <mirror>/<alias>/local-seq holds the last number handed out. Numbers are
// never reused: the counter only goes up, whatever happens to the issue afterwards.

func (a *App) seqPath(short string) string { return filepath.Join(a.Home, short, "local-seq") }

// nextLocalSeq returns the next number for alias. It holds an exclusive flock on a sidecar file
// (the counter itself is replaced by rename, so it cannot carry the lock), reads the counter,
// writes counter+1 to a temp file and renames it into place.
func (a *App) nextLocalSeq(short string) (int, error) {
	dir := filepath.Join(a.Home, short)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "local-seq.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // M_seqlock
		return 0, err
	}
	last := 0
	if s, err := readText(a.seqPath(short)); err == nil {
		if n, perr := strconv.Atoi(pyStrip(s)); perr == nil && n >= 0 {
			last = n
		} else {
			last = a.scanMaxSeq(short) // corrupt counter: rebuild from what is on disk
		}
	} else if isNotExist(err) {
		last = a.scanMaxSeq(short) // first use, or a counter that predates this file
	} else {
		return 0, err
	}
	n := last + 1
	if err := atomicWrite(a.seqPath(short), strconv.Itoa(n)+"\n", true); err != nil {
		return 0, err
	}
	return n, nil
}

// scanMaxSeq is the highest number already in use for alias: sequential local ids in the outbox
// and localmap, and (for a local-only alias) issue files and index rows.
func (a *App) scanMaxSeq(short string) int {
	max := 0
	note := func(n int) {
		if n > max {
			max = n
		}
	}
	for _, o := range a.loadOutbox() {
		if s, id, ok := strings.Cut(o.Ref, "#"); ok && s == short {
			if n, ok := seqOf(id); ok {
				note(n)
			}
		}
	}
	if s, err := readText(a.localmap()); err == nil {
		for _, line := range strings.Split(s, "\n") {
			p := strings.Split(line, "\t")
			if len(p) == 3 && p[0] == short {
				if n, ok := seqOf(p[1]); ok {
					note(n)
				}
			}
		}
	}
	if a.localOnly(short) {
		for _, r := range a.readIndex(short) {
			if n, err := strconv.Atoi(r[0]); err == nil {
				note(n)
			}
		}
		for _, p := range a.mirrorIssueFiles(short) {
			if n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".md")); err == nil {
				note(n)
			}
		}
	}
	return max
}

// ---------- local-only repos: issues are canonical in the mirror ----------

func (a *App) stamp() string { return a.Now().UTC().Format("2006-01-02T15:04:05Z") }

func (a *App) issuePath(short string, n int) string {
	return filepath.Join(a.Home, short, fmt.Sprintf("%d.md", n))
}

func (a *App) newLocalOnly(short, title, body string, labels []string, milestone string) (int, error) {
	n, err := a.nextLocalSeq(short)
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
	err = a.withLock(".pull.lock", func() error {
		if err := atomicWrite(a.issuePath(short, n), renderIssue(node), false); err != nil {
			return err
		}
		return a.rebuildIndex(short, nil, nil)
	})
	return n, err
}

const commentsMark = "\n\n## Comments\n"

var commentIDRe = regexp.MustCompile(` · id (\d+)\n`)

// loadLocalIssue parses a mirror file written by renderIssue back into a node plus its raw comment text.
func loadLocalIssue(path string) (*issueNode, string, error) {
	text, err := readText(path)
	if err != nil {
		return nil, "", err
	}
	h := readHeader(path)
	end := strings.Index(text, "\n---\n")
	if h == nil || end < 0 {
		return nil, "", fmt.Errorf("%s: not an issue file", path)
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
	return a.withLock(".pull.lock", func() error {
		path := a.issuePath(short, num)
		node, comments, err := loadLocalIssue(path)
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
		if err := atomicWrite(path, renderIssue(node)+comments, false); err != nil {
			return err
		}
		return a.rebuildIndex(short, nil, nil)
	})
}
