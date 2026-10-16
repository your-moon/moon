// Package issues is a local mirror of GitHub issues and PRs with claims and a write queue.
// The on-disk format is shared with the original Python tool, so both can run on one mirror.
package issues

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	autosyncMaxAge      = 600
	autosyncMarkerStale = 120
)

// App carries the mirror location and every side-effecting dependency.
type App struct {
	Home    string
	GH      GitHub
	Out     io.Writer
	Err     io.Writer
	Stdin   io.Reader
	Now     func() time.Time
	Host    string
	Environ func(string) string
	// SpawnSync starts the detached background sync. Tests replace it.
	SpawnSync func(a *App) error
	Repos     []Repo
	// Project scope (scope.go): resolved once in Main. Zero Scope.Kind means "not resolved": every repo.
	Scope        Scope
	Cfg          *Config
	Cwd          string
	ConfigSource string
	GitRemote    GitRemoteFunc
	// TTY: Out is a terminal; Width its columns; Color: ANSI colours allowed. All false/0 in tests and pipes.
	TTY   bool
	Width int
	Color bool
	// St draws colour (palette, depth); Forced: --color always. Both set by Main.
	St     *Styler
	Forced bool

	lblCache map[string]map[string]string // labels.tsv by alias, read once
}

// exitError ends a command with a status code; Msg (if any) goes to stderr.
type exitError struct {
	Code int
	Msg  string
}

func (e *exitError) Error() string { return e.Msg }

func die(format string, args ...any) error { return &exitError{2, fmt.Sprintf(format, args...)} }
func die1(format string, args ...any) error {
	return &exitError{1, fmt.Sprintf(format, args...)}
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.Out, format, args...) }
func (a *App) println(s string)                  { fmt.Fprintln(a.Out, s) }

// styler is the colour drawer when colour is on, else nil (which paints nothing).
func (a *App) styler() *Styler {
	if !a.Color {
		return nil
	}
	return a.St
}

// P paints text in a palette role, or returns it unchanged when colour is off.
func (a *App) P(role, text string) string { return a.styler().P(role, text) }

func (a *App) outbox() string    { return filepath.Join(a.Home, "outbox") }
func (a *App) claimsDir() string { return filepath.Join(a.Home, "claims") }
func (a *App) localmap() string  { return filepath.Join(a.Home, "localmap.tsv") }

func (a *App) nowEpoch() float64 {
	if v := a.Environ("ISSUES_TEST_NOW"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return float64(a.Now().UnixNano()) / 1e9
}

// workCtx is the clock, host and process check that claim staleness and work status depend on.
func (a *App) workCtx() WorkCtx {
	e := a.nowEpoch()
	return WorkCtx{Now: time.Unix(int64(e), int64((e-float64(int64(e)))*1e9)), Host: a.Host, Alive: pidAlive}
}

func (a *App) nowISO() string { return a.Now().Format("2006-01-02T15:04:05") }

// ---------- disk primitives ----------

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

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

// withLock takes a blocking flock on <home>/<name> for the duration of fn.
func (a *App) withLock(name string, fn func() error) error {
	if err := os.MkdirAll(a.Home, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(a.Home, name), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

func pyRstrip(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
	})
}

func pyStrip(s string) string {
	return strings.TrimLeftFunc(pyRstrip(s), unicode.IsSpace)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func uuid4() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ---------- refs ----------

func (a *App) parseRef(ref string) (short, ident string, err error) {
	i := strings.Index(ref, "#")
	if i > 0 {
		short, ident = ref[:i], ref[i+1:]
		if a.hasRepo(short) && validIdent(ident) {
			return short, ident, nil
		}
	}
	if a.TTY {
		if i := strings.Index(ref, "#"); i > 0 && !a.hasRepo(ref[:i]) {
			hint := ""
			if c := closest(ref[:i], a.Shorts()); c != "" {
				hint = " (did you mean " + c + "?)"
			}
			return "", "", die("no repo alias '%s' in '%s'%s; configured aliases: %s", ref[:i], ref, hint, strings.Join(a.Shorts(), ", "))
		}
		return "", "", die("bad ref '%s': want <alias>#123, for example %s#123", ref, firstOr(a.Shorts(), "app"))
	}
	return "", "", die("bad ref '%s' (want <alias>#123 or <alias>#L-<id>)", ref)
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
		}
	}
	if allDigits {
		return true
	}
	// L-<n>: sequential local id
	if rest, ok := strings.CutPrefix(s, "L-"); ok && rest != "" && isDigits(rest) {
		return true
	}
	// legacy L-<14 digits>-<4 hex>, valid forever
	if len(s) != 2+14+1+4 || !strings.HasPrefix(s, "L-") || s[16] != '-' {
		return false
	}
	for _, r := range s[2:16] {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, r := range s[17:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func isLocalIdent(ident string) bool { return strings.HasPrefix(ident, "L-") }

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// cmpLocalIdent orders local ids: legacy timestamp ids first (by text), then L-<n> numerically,
// so L-2 sorts before L-10.
func cmpLocalIdent(x, y string) int {
	nx, ox := seqOf(x)
	ny, oy := seqOf(y)
	switch {
	case !ox && !oy:
		return strings.Compare(x, y)
	case !ox:
		return -1
	case !oy:
		return 1
	case nx < ny:
		return -1
	case nx > ny:
		return 1
	}
	return 0
}

// seqOf returns n for the sequential form L-<n>.
func seqOf(ident string) (int, bool) {
	rest, ok := strings.CutPrefix(ident, "L-")
	if !ok || !isDigits(rest) {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

func shortOf(ref string) string {
	i := strings.Index(ref, "#")
	if i < 0 {
		return ref
	}
	return ref[:i]
}

// ---------- outbox ----------

// Op is one queued write. Field order matches the Python dict so files look the same.
type Op struct {
	OpID      string
	TS        string
	Op        string
	Ref       string
	Args      map[string]any
	Attempts  int
	LastError *string
	Inflight  bool
	File      string
}

var argOrder = []string{"title", "body", "labels", "milestone", "local_id", "reason", "comment", "add", "remove"}

func (o *Op) marshal() string {
	args := Obj{}
	seen := map[string]bool{}
	for _, k := range argOrder {
		if v, ok := o.Args[k]; ok {
			args = append(args, KV{k, v})
			seen[k] = true
		}
	}
	rest := []string{}
	for k := range o.Args {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		args = append(args, KV{k, o.Args[k]})
	}
	var le any
	if o.LastError != nil {
		le = *o.LastError
	}
	ob := Obj{{"op_id", o.OpID}, {"ts", o.TS}, {"op", o.Op}, {"ref", o.Ref}, {"args", args}, {"attempts", o.Attempts}, {"last_error", le}}
	if o.Inflight {
		ob = append(ob, KV{"inflight", true})
	}
	return dumps(ob, false) + "\n"
}

func (a *App) saveOp(o *Op) error { return atomicWrite(o.File, o.marshal(), true) }

func parseOp(data []byte, file string) (*Op, error) {
	var raw struct {
		OpID      string         `json:"op_id"`
		TS        string         `json:"ts"`
		Op        string         `json:"op"`
		Ref       string         `json:"ref"`
		Args      map[string]any `json:"args"`
		Attempts  float64        `json:"attempts"`
		LastError *string        `json:"last_error"`
		Inflight  bool           `json:"inflight"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Args == nil {
		raw.Args = map[string]any{}
	}
	return &Op{raw.OpID, raw.TS, raw.Op, raw.Ref, raw.Args, int(raw.Attempts), raw.LastError, raw.Inflight, file}, nil
}

// loadOutbox returns all pending ops, oldest first. No lock needed.
func (a *App) loadOutbox() []*Op {
	ents, err := os.ReadDir(a.outbox())
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
	var ops []*Op
	for _, n := range names {
		p := filepath.Join(a.outbox(), n)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		o, err := parseOp(b, p)
		if err != nil {
			continue
		}
		ops = append(ops, o)
	}
	return ops
}

func argStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func argList(m map[string]any, k string) []string {
	l, _ := m[k].([]any)
	out := []string{}
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) localmapLookup(short, lid string) int {
	s, err := readText(a.localmap())
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(s, "\n") {
		p := strings.Split(line, "\t")
		if len(p) == 3 && p[0] == short && p[1] == lid {
			if n, err := strconv.Atoi(p[2]); err == nil {
				return n
			}
		}
	}
	return 0
}

func (a *App) hasPendingCreate(ref string) bool {
	for _, o := range a.loadOutbox() {
		if o.Op == "new" && o.Ref == ref {
			return true
		}
	}
	return false
}

// enqueue writes one new file per op. An op on an L-ref is resolved to the real number if its create already landed.
func (a *App) enqueue(op, ref string, args map[string]any) (string, string, error) {
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return "", "", err
	}
	if isLocalIdent(ident) && op != "new" && !a.hasPendingCreate(ref) {
		n := a.localmapLookup(short, ident)
		if n == 0 {
			return "", "", die("unknown local issue %s (not in the outbox, not in localmap)", ref)
		}
		ref = fmt.Sprintf("%s#%d", short, n)
	}
	t := a.Now()
	stem := fmt.Sprintf("%s.%06d-%d-%s", t.Format("20060102T150405"), t.UnixMicro()%1000000, os.Getpid(), uuid4())
	o := &Op{OpID: stem, TS: a.nowISO(), Op: op, Ref: ref, Args: args, File: filepath.Join(a.outbox(), stem+".json")}
	if err := a.saveOp(o); err != nil {
		return "", "", err
	}
	s2, i2, _ := a.parseRef(o.Ref)
	if isLocalIdent(i2) && op != "new" && !a.hasPendingCreate(o.Ref) {
		if n := a.localmapLookup(s2, i2); n != 0 {
			o.Ref = fmt.Sprintf("%s#%d", s2, n)
			if err := a.saveOp(o); err != nil {
				return "", "", err
			}
		}
	}
	return stem, o.Ref, nil
}

// ---------- claims ----------

type Claim struct {
	Agent string
	Pid   int
	Host  string
	TS    float64
	TTL   float64
}

func (c *Claim) marshal() string {
	return dumps(Obj{{"agent", c.Agent}, {"pid", c.Pid}, {"host", c.Host}, {"ts", c.TS}, {"ttl", int(c.TTL)}}, true)
}

func (a *App) claimPath(ref string) (string, error) {
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(a.claimsDir(), short+"-"+ident+".json"), nil
}

// readClaim returns nil if absent. A claim that is mid-write counts as fresh.
func (a *App) readClaim(path string) *Claim {
	b, err := os.ReadFile(path)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
	}
	var raw struct {
		Agent string  `json:"agent"`
		Pid   float64 `json:"pid"`
		Host  string  `json:"host"`
		TS    float64 `json:"ts"`
		TTL   float64 `json:"ttl"`
	}
	if err != nil || json.Unmarshal(b, &raw) != nil {
		return &Claim{"?", 0, "?", a.nowEpoch(), 60}
	}
	return &Claim{raw.Agent, int(raw.Pid), raw.Host, raw.TS, raw.TTL}
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == syscall.ESRCH {
		return false
	}
	return true
}

func (a *App) claimIsStale(c *Claim) bool {
	return claimExpired(c, a.workCtx())
}

func (a *App) claimStatus(ref string) (string, *Claim) {
	p, err := a.claimPath(ref)
	if err != nil {
		return "none", nil
	}
	c := a.readClaim(p)
	if c == nil {
		return "none", nil
	}
	if a.claimIsStale(c) {
		return "stale", c
	}
	return "fresh", c
}

func (a *App) holderText(c *Claim) string {
	pid := "-"
	if c.Pid != 0 {
		pid = strconv.Itoa(c.Pid)
	}
	return fmt.Sprintf("%s (pid %s on %s, claimed %ds ago, ttl %ds)", c.Agent, pid, c.Host, int(a.nowEpoch()-c.TS), int(c.TTL))
}

// heldMsg is the exit-3 text of a refused claim. The first line is what scripts have always seen; on a
// terminal it says who holds the issue, for how long, and what to do about it.
func (a *App) heldMsg(ref string, c *Claim) string {
	if !a.TTY {
		return "TAKEN " + ref + " is held by " + a.holderText(c)
	}
	pid := ""
	if c.Pid != 0 {
		pid = fmt.Sprintf(", pid %d", c.Pid)
	}
	left := int(c.TTL - (a.nowEpoch() - c.TS))
	return fmt.Sprintf("%s is already claimed by %s (on %s%s) for %s; the claim expires in %s.\nWait, pick another issue (moon issues frontier), or ask %s to run: moon issues release %s --agent %s",
		ref, c.Agent, c.Host, pid, humanDur(int(a.nowEpoch()-c.TS)), humanDur(left), c.Agent, ref, c.Agent)
}

func (a *App) releaseClaim(ref, agent string, force bool) string {
	p, err := a.claimPath(ref)
	if err != nil {
		return "none"
	}
	c := a.readClaim(p)
	if c == nil {
		return "none"
	}
	if !force && agent != c.Agent {
		return "held by " + c.Agent
	}
	os.Remove(p)
	return "released"
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

var _ = bytes.NewBuffer

func firstOr(l []string, d string) string {
	if len(l) > 0 {
		return l[0]
	}
	return d
}
