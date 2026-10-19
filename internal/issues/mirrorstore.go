package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// mirrorStore owns the on-disk mirror format, which a Python tool shares, so nothing here may change shape:
// per-repo index.tsv, <n>.md, pr/<n>.md, prs.tsv, milestones.tsv, labels.tsv, lastpull and the cursors,
// local-seq / local-mseq counters, plus claims/*.json, localmap.tsv and outbox/*.json under the mirror home.
// Every other file in this package reaches those files through it (mirrorstore_guard_test.go checks that).
// The autosync marker files belong to the sync engine, not here.
type mirrorStore struct{ home string }

func (a *App) mirror() mirrorStore { return mirrorStore{a.Home} }

// ---------- paths ----------

func (m mirrorStore) repoDir(short string) string { return filepath.Join(m.home, short) }
func (m mirrorStore) outboxDir() string           { return filepath.Join(m.home, "outbox") }
func (m mirrorStore) claimsDir() string           { return filepath.Join(m.home, "claims") }
func (m mirrorStore) localmapPath() string        { return filepath.Join(m.home, "localmap.tsv") }
func (m mirrorStore) indexPath(short string) string {
	return filepath.Join(m.home, short, "index.tsv")
}
func (m mirrorStore) prsPath(short string) string { return filepath.Join(m.home, short, "prs.tsv") }
func (m mirrorStore) msPath(short string) string {
	return filepath.Join(m.home, short, "milestones.tsv")
}
func (m mirrorStore) labelsPath(short string) string {
	return filepath.Join(m.home, short, "labels.tsv")
}
func (m mirrorStore) lastpullPath(short string) string {
	return filepath.Join(m.home, short, "lastpull")
}
func (m mirrorStore) cursorPath(short string) string { return filepath.Join(m.home, short, "cursor") }
func (m mirrorStore) prCursorPath(short string) string {
	return filepath.Join(m.home, short, "prcursor")
}
func (m mirrorStore) prDir(short string) string { return filepath.Join(m.home, short, "pr") }
func (m mirrorStore) issueFile(short string, n int) string {
	return filepath.Join(m.home, short, fmt.Sprintf("%d.md", n))
}
func (m mirrorStore) issueFileIdent(short, ident string) string {
	return filepath.Join(m.home, short, ident+".md")
}
func (m mirrorStore) prFile(short, ident string) string {
	return filepath.Join(m.home, short, "pr", ident+".md")
}
func (m mirrorStore) claimFile(short, ident string) string {
	return filepath.Join(m.claimsDir(), short+"-"+ident+".json")
}

// ---------- low-level file helpers (the only os file calls on mirror data) ----------

func atomicWrite(path, text string, fsync bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if fsync {
		f.Sync()
	}
	f.Close()
	return os.Rename(tmp, path)
}

// readText reads a file with Python's universal-newline translation.
func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	if strings.Contains(s, "\r") {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	}
	return s, nil
}

func createExcl(path, body string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(body)
	f.Close()
	return err
}

// readUserText reads a file the person named on the command line (not mirror data).
func readUserText(path string) (string, error) { return readText(path) }

func (m mirrorStore) remove(path string) error { return os.Remove(path) }
func (m mirrorStore) exists(path string) bool  { _, err := os.Stat(path); return err == nil }

// writeIfChanged writes text unless the file already holds it; changed says whether it wrote.
func (m mirrorStore) writeIfChanged(path, text string) (bool, error) {
	if old, err := readText(path); err == nil && old == text {
		return false, nil
	}
	return true, atomicWrite(path, text, false)
}

// ---------- locks ----------

// withLock takes a blocking flock on <home>/<name> for the duration of fn.
func (m mirrorStore) withLock(name string, fn func() error) error {
	if err := os.MkdirAll(m.home, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(m.home, name), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

// tryPushLock takes the lock that push holds while it sends the queue. ok is false when another push is
// running; the caller closes the file to release the lock.
func (m mirrorStore) tryPushLock() (f *os.File, ok bool) {
	os.MkdirAll(m.home, 0o755)
	f, err := os.OpenFile(filepath.Join(m.home, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // M_pushlock
		f.Close()
		return nil, false
	}
	return f, true
}

// ---------- issues, PRs, index, labels, cursors ----------

// issueFiles lists <repo>/<n>.md paths in directory order.
func (m mirrorStore) issueFiles(short string) []string {
	d := m.repoDir(short)
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".md") && !e.IsDir() {
			out = append(out, filepath.Join(d, e.Name()))
		}
	}
	return out
}

// prFileNames lists the file names under <repo>/pr.
func (m mirrorStore) prFileNames(short string) []string {
	ents, _ := os.ReadDir(m.prDir(short))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func (m mirrorStore) removePRFile(short, name string) { os.Remove(filepath.Join(m.prDir(short), name)) }
func (m mirrorStore) ensureRepoDir(short string) error {
	return os.MkdirAll(m.repoDir(short), 0o755)
}
func (m mirrorStore) ensurePRDir(short string) error { return os.MkdirAll(m.prDir(short), 0o755) }

func (m mirrorStore) hasIssueFile(short, ident string) bool {
	return m.exists(m.issueFileIdent(short, ident))
}
func (m mirrorStore) hasIndex(short string) bool    { return m.exists(m.indexPath(short)) }
func (m mirrorStore) hasLastpull(short string) bool { return m.exists(m.lastpullPath(short)) }
func (m mirrorStore) readIndexText(short string) (string, error) {
	return readText(m.indexPath(short))
}
func (m mirrorStore) writeIndexText(short, text string) (bool, error) {
	return m.writeIfChanged(m.indexPath(short), text)
}
func (m mirrorStore) readPRsText(short string) (string, error) { return readText(m.prsPath(short)) }
func (m mirrorStore) writePRsText(short, text string) (bool, error) {
	return m.writeIfChanged(m.prsPath(short), text)
}
func (m mirrorStore) readPRText(short, ident string) (string, error) {
	return readText(m.prFile(short, ident))
}
func (m mirrorStore) writePRText(short string, n int, text string) (bool, error) {
	return m.writeIfChanged(filepath.Join(m.prDir(short), fmt.Sprintf("%d.md", n)), text)
}
func (m mirrorStore) readIssueText(short, ident string) (string, error) {
	return readText(m.issueFileIdent(short, ident))
}
func (m mirrorStore) writeIssueText(short string, n int, text string) (bool, error) {
	return m.writeIfChanged(m.issueFile(short, n), text)
}
func (m mirrorStore) writeIssueFile(path, text string) error { return atomicWrite(path, text, false) }
func (m mirrorStore) readLabelsText(short string) (string, error) {
	return readText(m.labelsPath(short))
}
func (m mirrorStore) writeLabelsText(short, text string) error {
	return atomicWrite(m.labelsPath(short), text, false)
}
func (m mirrorStore) readLastpull(short string) (string, error) {
	return readText(m.lastpullPath(short))
}
func (m mirrorStore) writeLastpull(short, text string) error {
	return atomicWrite(m.lastpullPath(short), text, false)
}
func (m mirrorStore) readCursor(short string) (string, error) { return readText(m.cursorPath(short)) }
func (m mirrorStore) writeCursor(short, text string) (bool, error) {
	return m.writeIfChanged(m.cursorPath(short), text)
}
func (m mirrorStore) readPRCursor(short string) (string, error) {
	return readText(m.prCursorPath(short))
}
func (m mirrorStore) writePRCursor(short, text string) (bool, error) {
	return m.writeIfChanged(m.prCursorPath(short), text)
}
func (m mirrorStore) readMsText(short string) (string, error) { return readText(m.msPath(short)) }
func (m mirrorStore) writeMsText(short, text string) error {
	return atomicWrite(m.msPath(short), text, false)
}

// ---------- claims ----------

// claimNames lists claims/*.json file names, sorted.
func (m mirrorStore) claimNames() []string {
	ents, _ := os.ReadDir(m.claimsDir())
	names := []string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (m mirrorStore) claimNamePath(name string) string           { return filepath.Join(m.claimsDir(), name) }
func (m mirrorStore) readClaimBytes(path string) ([]byte, error) { return os.ReadFile(path) }
func (m mirrorStore) ensureClaimsDir()                           { os.MkdirAll(m.claimsDir(), 0o755) }
func (m mirrorStore) writeClaim(path, body string) error         { return atomicWrite(path, body, false) }
func (m mirrorStore) createClaim(path, body string) error        { return createExcl(path, body) }
func (m mirrorStore) renameClaim(from, to string) error          { return os.Rename(from, to) }

// ---------- localmap ----------

func (m mirrorStore) readLocalmap() (string, error) { return readText(m.localmapPath()) }

// appendLocalmap records that local id lid of short became GitHub number n.
func (m mirrorStore) appendLocalmap(short, lid string, n int) error {
	lm, err := os.OpenFile(m.localmapPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lm.Close()
	_, err = fmt.Fprintf(lm, "%s\t%s\t%d\n", short, lid, n)
	return err
}

// ---------- outbox ----------

func (m mirrorStore) opFile(stem string) string { return filepath.Join(m.outboxDir(), stem+".json") }

// opFiles lists outbox/*.json paths, oldest first.
func (m mirrorStore) opFiles() []string {
	ents, err := os.ReadDir(m.outboxDir())
	if err != nil {
		return nil
	}
	names := []string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(m.outboxDir(), n)
	}
	return out
}

func (m mirrorStore) readOpBytes(path string) ([]byte, error) { return os.ReadFile(path) }
func (m mirrorStore) saveOp(o *Op) error                      { return atomicWrite(o.File, o.marshal(), true) }
func (m mirrorStore) removeOp(file string) error              { return os.Remove(file) }

// ---------- local id sequences ----------

func (m mirrorStore) nextIssueSeq(short string, rebuild func() int) (int, error) {
	return m.nextCounter(short, "local-seq", rebuild)
}

func (m mirrorStore) nextMilestoneSeq(short string, rebuild func() int) (int, error) {
	return m.nextCounter(short, "local-mseq", rebuild)
}

// nextCounter hands out the next number of a counter that only goes up. name is the counter file under
// the repo dir ("local-seq", "local-mseq"). It holds an exclusive flock on <name>.lock (the counter itself
// is replaced by rename, so it cannot carry the lock), reads the counter, and writes counter+1 through a
// temp file. rebuild recomputes a lost or corrupt counter from what is on disk.
func (m mirrorStore) nextCounter(short, name string, rebuild func() int) (int, error) {
	dir := m.repoDir(short)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // M_seqlock
		return 0, err
	}
	path := filepath.Join(dir, name)
	last := 0
	if s, err := readText(path); err == nil {
		if n, perr := strconv.Atoi(pyStrip(s)); perr == nil && n >= 0 {
			last = n
		} else {
			last = rebuild() // corrupt counter: rebuild from what is on disk
		}
	} else if isNotExist(err) {
		last = rebuild() // first use, or a counter that predates this file
	} else {
		return 0, err
	}
	n := last + 1
	if err := atomicWrite(path, strconv.Itoa(n)+"\n", true); err != nil {
		return 0, err
	}
	return n, nil
}

// readMirrorFile reads a mirror file by a path the store handed out.
func readMirrorFile(path string) (string, error) { return readText(path) }

// readIssueHead returns up to n bytes from the start of <repo>/<ident>.md.
func (m mirrorStore) readIssueHead(short, ident string, n int) (string, error) {
	f, err := os.Open(m.issueFileIdent(short, ident))
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := f.Read(buf)
	return string(buf[:k]), nil
}
