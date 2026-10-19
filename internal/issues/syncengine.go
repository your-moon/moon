package issues

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// The sync engine: every rule for when a background `sync` starts, and the status text about it.
// The entry points on App are
//   - BeforeRead(name, o): a read of an old mirror starts a background sync;
//   - AfterWrite(): a queued write is pushed in the background (or noted for the sync already running);
//   - SyncStatus(): the data for the status and outbox lines;
//   - EndBackgroundSync(pushOnly): the end hook of `sync --quiet`.
//
// App.SpawnSync starts the detached process; tests replace it. The autosync = false key is parsed in config.go.

const (
	autosyncMaxAge      = 600 // seconds a mirror may be old before a read starts a sync
	autosyncMarkerStale = 120 // seconds after which a .autosync marker is taken to belong to a dead sync
)

// noAutopush: commands that are the sync themselves, or touch nothing the outbox holds.
var noAutopush = map[string]bool{"push": true, "pull": true, "sync": true, "prune": true, "where": true}

// AfterWrite sends queued writes in the background right after a write, so no command waits on GitHub.
// A background sync already running gets an .autosync.again note instead and pushes once more before it exits.
func (a *App) AfterWrite() {
	if off, _ := a.autosyncOff(); off || !a.remoteOpsQueued() {
		return
	}
	if !a.takeAutosyncMarker() {
		a.mirror().Markers.NoteAgain()
		return
	}
	if err := a.SpawnSync(a); err != nil {
		a.mirror().Markers.Release()
	}
}

// remoteOpsQueued: the outbox holds a write a sync could still send: for a repo that has a GitHub side, and
// not one that sync gave up on (Permanent), which would only make every write spawn a sync that sends nothing.
func (a *App) remoteOpsQueued() bool {
	for _, op := range a.loadOutbox() {
		if op.Permanent {
			continue
		}
		if short, _, err := a.parseRef(op.Ref); err != nil || !a.localOnly(short) {
			return true
		}
	}
	return false
}

// EndBackgroundSync (the end hook of `sync --quiet`) ends a background sync. A write queued while it ran left .autosync.again;
// then the marker is taken back and the outbox pushed again, so that write is not stranded until the next command.
func (a *App) EndBackgroundSync(pushOnly []string) {
	for i := 0; i < 5; i++ {
		a.mirror().Markers.Release()
		if !a.mirror().Markers.TakeAgain() || !a.takeAutosyncMarker() {
			return
		}
		a.pushRepos(pushOnly, "")
	}
	a.mirror().Markers.Release()
}

// Autosync is the background `sync` that a read starts when the mirror is old. It can be turned off for a
// session that must stay offline, and `status` shows whether it is on and how its last run went.

// autosyncOff reports whether reads must not start a background sync, and which switch said so. Order:
// ISSUES_NO_AUTOSYNC (old name) and MOON_AUTOSYNC=0|false|off|no turn it off; MOON_AUTOSYNC=1|true|on|yes turns
// it on even when moon.toml says autosync = false; otherwise moon.toml decides.
func (a *App) autosyncOff() (bool, string) {
	if a.Environ("ISSUES_NO_AUTOSYNC") != "" {
		return true, "ISSUES_NO_AUTOSYNC"
	}
	switch strings.ToLower(a.Environ("MOON_AUTOSYNC")) {
	case "0", "false", "off", "no":
		return true, "MOON_AUTOSYNC=" + a.Environ("MOON_AUTOSYNC")
	case "1", "true", "on", "yes":
		return false, ""
	}
	if a.Cfg != nil && a.Cfg.NoAutosync {
		return true, "autosync = false in moon.toml"
	}
	return false, ""
}

// autosyncLast is the last SYNC line the background sync logged ("" when it never ran).
func (a *App) autosyncLast() string {
	s, err := a.mirror().Markers.LastLog()
	if err != nil {
		return ""
	}
	lines := splitLines(s)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], " SYNC ") {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

// SyncInfo is what the status and outbox lines show about autosync.
type SyncInfo struct {
	Off  bool
	Why  string // which switch turned it off
	Last string // the last SYNC line the background sync logged
}

// SyncStatus is the data for the status line and the outbox listing.
func (a *App) SyncStatus() SyncInfo {
	off, why := a.autosyncOff()
	return SyncInfo{Off: off, Why: why, Last: a.autosyncLast()}
}

// Summary is the one-line status text: "autosync: on, last run <line>" or "autosync: off (<why>)".
func (s SyncInfo) Summary() string {
	off, why, last := s.Off, s.Why, s.Last
	switch {
	case off && last != "":
		return "autosync: off (" + why + "), last run " + last
	case off:
		return "autosync: off (" + why + ")"
	case last != "":
		return "autosync: on, last run " + last
	}
	return "autosync: on, has not run yet"
}

// BeforeRead is called by every read command (read: true in the command table) before it runs.
func (a *App) BeforeRead(name string, o *Opts) error {
	shorts := a.shortsFor(o)
	if name == "show" || name == "pr show" {
		s, _, err := a.parseRef(o.Pos[0])
		if err != nil {
			return err
		}
		shorts = []string{s}
	}
	shorts, _ = a.splitLocal(shorts) // local-only aliases have nothing to refresh
	if len(shorts) == 0 {
		return nil
	}
	if o.B["fresh"] { // M_fresh
		a.doSync(shorts, false, a.Err, nil)
		return nil
	}
	if off, _ := a.autosyncOff(); off {
		return nil
	}
	age, ok := a.mirrorAge(shorts)
	if ok && age <= autosyncMaxAge { // M_asage
		return nil
	}
	if !a.takeAutosyncMarker() {
		return nil
	}
	if err := a.SpawnSync(a); err != nil {
		a.mirror().Markers.Release()
		return nil
	}
	if ok {
		fmt.Fprintf(a.Err, "(mirror %d min old; refreshing in background)\n", int(age)/60)
	} else {
		fmt.Fprintf(a.Err, "(mirror never pulled, so old; refreshing in background)\n")
	}
	return nil
}

func (a *App) mirrorAge(shorts []string) (float64, bool) {
	var max float64
	for _, s := range shorts {
		b, err := a.mirror().Issues.Read(s, fileLastpull)
		t, perr := strconv.Atoi(pyStrip(b))
		if err != nil || perr != nil {
			return 0, false
		}
		if age := float64(a.Now().UnixNano())/1e9 - float64(t); age > max {
			max = age
		}
	}
	return max, true
}

func (a *App) takeAutosyncMarker() bool { return a.mirror().Markers.Take(autosyncMarkerStale) }

// spawnSync starts `moon issues sync --quiet` fully detached, output appended to autosync.log.
func spawnSync(a *App) error {
	if f := a.Environ("ISSUES_TEST_SPAWN"); f != "" {
		return appendUserText(f, "spawn\n")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.HasSuffix(self, ".test") { // a go test binary would re-run its tests detached, and those spawn again
		return errors.New("background sync: not from a test binary")
	}
	log, err := a.mirror().Markers.Log()
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(self, "issues", "--dir", a.Home, "sync", "--quiet", "--all")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "ISSUES_NO_AUTOSYNC=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
