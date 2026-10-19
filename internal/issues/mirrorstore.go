package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// mirrorStore owns the on-disk mirror format, which a Python tool shares, so nothing here may change shape:
// per-repo index.tsv, <n>.md, pr/<n>.md, prs.tsv, milestones.tsv, labels.tsv, lastpull and the cursors,
// local-seq / local-mseq counters, plus claims/*.json, localmap.tsv and outbox/*.json under the mirror home,
// and the autosync marker files.
//
// It is five typed collections, each addressed by key, never by path: Issues (everything under a repo dir),
// Claims, Outbox, LocalIDs (localmap.tsv and the id counters) and Markers (autosync). Every other file in
// this package reaches the disk through them (mirrorstore_guard_test.go checks that; config.go reads the
// config file and is the one exemption).
type mirrorStore struct {
	home     string
	Issues   Issues
	Claims   Claims
	Outbox   Outbox
	LocalIDs LocalIDs
	Markers  Markers
}

func (a *App) mirror() mirrorStore {
	h := a.Home
	return mirrorStore{h, Issues{h}, Claims{h}, Outbox{h}, LocalIDs{h}, Markers{h}}
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

// appendUserText appends text to a file the person (or a test) named; not mirror data.
func appendUserText(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeIfChanged writes text unless the file already holds it; changed says whether it wrote.
func writeIfChanged(path, text string) (bool, error) {
	if old, err := readText(path); err == nil && old == text {
		return false, nil
	}
	return true, atomicWrite(path, text, false)
}

// listNames lists the files of dir whose name ends in suffix, sorted. A missing dir lists nothing.
func listNames(dir, suffix string) []string {
	ents, _ := os.ReadDir(dir)
	names := []string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), suffix) && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
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

// ---------- Issues: everything under <home>/<repo>/ ----------

// repoFile is one of the per-repo data files. changedOnly files are rewritten only when the text differs.
type repoFile struct {
	name        string
	changedOnly bool
}

var (
	fileIndex      = repoFile{"index.tsv", true}
	filePRs        = repoFile{"prs.tsv", true}
	fileMilestones = repoFile{"milestones.tsv", false}
	fileLabels     = repoFile{"labels.tsv", false}
	fileLastpull   = repoFile{"lastpull", false}
	fileCursor     = repoFile{"cursor", true}
	filePRCursor   = repoFile{"prcursor", true}
)

// Issues holds the per-repo files, the issue files <n>.md (key: repo short + ident) and the PR files pr/<n>.md.
type Issues struct{ home string }

func (c Issues) dir(short string) string   { return filepath.Join(c.home, short) }
func (c Issues) prDir(short string) string { return filepath.Join(c.home, short, "pr") }

// Read, Write and Has address one per-repo data file. Write says whether it wrote.
func (c Issues) Read(short string, f repoFile) (string, error) {
	return readText(filepath.Join(c.dir(short), f.name))
}
func (c Issues) Write(short string, f repoFile, text string) (bool, error) {
	p := filepath.Join(c.dir(short), f.name)
	if f.changedOnly {
		return writeIfChanged(p, text)
	}
	return true, atomicWrite(p, text, false)
}
func (c Issues) Has(short string, f repoFile) bool {
	_, err := os.Stat(filepath.Join(c.dir(short), f.name))
	return err == nil
}

// Idents lists the idents of the issue files of a repo, in directory order.
func (c Issues) Idents(short string) []string {
	var out []string
	for _, n := range listNames(c.dir(short), ".md") {
		out = append(out, strings.TrimSuffix(n, ".md"))
	}
	return out
}

func (c Issues) issueFile(short, ident string) string {
	return filepath.Join(c.dir(short), ident+".md")
}

func (c Issues) Get(short, ident string) (string, error) { return readText(c.issueFile(short, ident)) }
func (c Issues) Exists(short, ident string) bool {
	_, err := os.Stat(c.issueFile(short, ident))
	return err == nil
}
func (c Issues) Put(short, ident, text string) error {
	return atomicWrite(c.issueFile(short, ident), text, false)
}

// PutIfChanged writes issue n unless its file already holds text; changed says whether it wrote.
func (c Issues) PutIfChanged(short string, n int, text string) (bool, error) {
	return writeIfChanged(c.issueFile(short, strconv.Itoa(n)), text)
}
func (c Issues) Remove(short, ident string) error { return os.Remove(c.issueFile(short, ident)) }

// Head returns up to n bytes from the start of an issue file.
func (c Issues) Head(short, ident string, n int) (string, error) {
	f, err := os.Open(c.issueFile(short, ident))
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := f.Read(buf)
	return string(buf[:k]), nil
}

func (c Issues) prFile(short, ident string) string { return filepath.Join(c.prDir(short), ident+".md") }

func (c Issues) PR(short, ident string) (string, error) { return readText(c.prFile(short, ident)) }
func (c Issues) PutPR(short string, n int, text string) (bool, error) {
	return writeIfChanged(c.prFile(short, strconv.Itoa(n)), text)
}
func (c Issues) RemovePR(short, ident string) { os.Remove(c.prFile(short, ident)) }

// PRIdents lists the idents of the PR files of a repo.
func (c Issues) PRIdents(short string) []string {
	var out []string
	for _, n := range listNames(c.prDir(short), ".md") {
		out = append(out, strings.TrimSuffix(n, ".md"))
	}
	return out
}

// Ensure creates the repo dir (and the PR dir when pr is true).
func (c Issues) Ensure(short string, pr bool) error {
	if pr {
		return os.MkdirAll(c.prDir(short), 0o755)
	}
	return os.MkdirAll(c.dir(short), 0o755)
}

// ---------- Claims: claims/<key>, key = <repo>-<ident>.json ----------

type Claims struct{ home string }

func (c Claims) dir() string            { return filepath.Join(c.home, "claims") }
func (c Claims) path(key string) string { return filepath.Join(c.dir(), key) }

// Key is the key of the claim on short#ident.
func (Claims) Key(short, ident string) string { return short + "-" + ident + ".json" }

// List lists the keys of all claims, sorted.
func (c Claims) List() []string { return listNames(c.dir(), ".json") }

func (c Claims) Get(key string) ([]byte, error) { return os.ReadFile(c.path(key)) }
func (c Claims) Put(key, body string) error     { return atomicWrite(c.path(key), body, false) }

// Create writes the claim only if there is none (exclusive create); the error then satisfies os.IsExist.
func (c Claims) Create(key, body string) error {
	os.MkdirAll(c.dir(), 0o755)
	return createExcl(c.path(key), body)
}
func (c Claims) Remove(key string) error      { return os.Remove(c.path(key)) }
func (c Claims) Rename(from, to string) error { return os.Rename(c.path(from), c.path(to)) }

// ---------- Outbox: outbox/<key>.json, key = the op stem ----------

type Outbox struct{ home string }

func (c Outbox) dir() string            { return filepath.Join(c.home, "outbox") }
func (c Outbox) path(key string) string { return filepath.Join(c.dir(), key+".json") }

// List lists the keys of the queued ops, oldest first.
func (c Outbox) List() []string {
	names := listNames(c.dir(), ".json")
	for i, n := range names {
		names[i] = strings.TrimSuffix(n, ".json")
	}
	return names
}
func (c Outbox) Get(key string) ([]byte, error) { return os.ReadFile(c.path(key)) }
func (c Outbox) Put(key, text string) error     { return atomicWrite(c.path(key), text, true) }
func (c Outbox) Remove(key string) error        { return os.Remove(c.path(key)) }

// ---------- LocalIDs: localmap.tsv and the per-family id counters ----------

type LocalIDs struct{ home string }

// LocalRow is one line of localmap.tsv: local id Lid of Short became GitHub number N. OK is false when N
// was not a number.
type LocalRow struct {
	Short, Lid string
	N          int
	OK         bool
}

func (c LocalIDs) path() string { return filepath.Join(c.home, "localmap.tsv") }

// Rows lists the lines of localmap.tsv that have three fields.
func (c LocalIDs) Rows() []LocalRow {
	s, err := readText(c.path())
	if err != nil {
		return nil
	}
	var out []LocalRow
	for _, line := range strings.Split(s, "\n") {
		p := strings.Split(line, "\t")
		if len(p) != 3 {
			continue
		}
		n, err := strconv.Atoi(p[2])
		out = append(out, LocalRow{p[0], p[1], n, err == nil})
	}
	return out
}

// Lookup is the GitHub number local id lid of short became, or 0.
func (c LocalIDs) Lookup(short, lid string) int {
	for _, r := range c.Rows() {
		if r.OK && r.Short == short && r.Lid == lid {
			return r.N
		}
	}
	return 0
}

// Append records that local id lid of short became GitHub number n.
func (c LocalIDs) Append(short, lid string, n int) error {
	lm, err := os.OpenFile(c.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lm.Close()
	_, err = fmt.Fprintf(lm, "%s\t%s\t%d\n", short, lid, n)
	return err
}

// counterName is the counter file of a family: issues count L-<n>, milestones M-<n>.
func counterName(fam opFamily) (string, error) {
	switch fam {
	case famIssue:
		return "local-seq", nil
	case famMilestone:
		return "local-mseq", nil
	}
	return "", fmt.Errorf("no local id counter for op family %d", fam)
}

// Next hands out the next number of the family's counter, which only goes up. It holds an exclusive flock
// on <name>.lock (the counter itself is replaced by rename, so it cannot carry the lock), reads the
// counter, and writes counter+1 through a temp file. rebuild recomputes a lost or corrupt counter from
// what is on disk.
func (c LocalIDs) Next(short string, fam opFamily, rebuild func() int) (int, error) {
	name, err := counterName(fam)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(c.home, short)
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

// ---------- Markers: the autosync files ----------

type Markers struct{ home string }

func (c Markers) path(name string) string { return filepath.Join(c.home, name) }

// Take claims the .autosync marker for this process. A marker older than staleAfter seconds belongs to a
// dead sync and is taken over; a younger one means a sync is running and Take says false.
func (c Markers) Take(staleAfter float64) bool {
	os.MkdirAll(c.home, 0o755)
	path := c.path(".autosync")
	for i := 0; i < 2; i++ {
		if err := createExcl(path, strconv.Itoa(os.Getpid())+"\n"); err == nil { // M_asmarker
			return true
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if time.Since(st.ModTime()).Seconds() <= staleAfter { // M_asstale
			return false
		}
		gone := path + ".stale." + uuid4()
		if os.Rename(path, gone) == nil {
			os.Remove(gone)
		}
	}
	return false
}

// Release drops the .autosync marker.
func (c Markers) Release() { os.Remove(c.path(".autosync")) }

// NoteAgain leaves the note that a write was queued while a sync ran.
func (c Markers) NoteAgain() { os.WriteFile(c.path(".autosync.again"), nil, 0o644) }

// TakeAgain removes the note and says whether there was one.
func (c Markers) TakeAgain() bool { return os.Remove(c.path(".autosync.again")) == nil }

// Log opens autosync.log for appending.
func (c Markers) Log() (*os.File, error) {
	return os.OpenFile(c.path("autosync.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// LastLog reads autosync.log.
func (c Markers) LastLog() (string, error) { return readText(c.path("autosync.log")) }
