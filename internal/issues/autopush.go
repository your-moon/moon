package issues

import (
	"os"
	"path/filepath"
)

// noAutopush: commands that are the sync themselves, or touch nothing the outbox holds.
var noAutopush = map[string]bool{"push": true, "pull": true, "sync": true, "prune": true, "where": true}

// kickPush sends queued writes in the background right after a write, so no command waits on GitHub.
// A background sync already running gets an .autosync.again note instead and pushes once more before it exits.
func (a *App) kickPush() {
	if off, _ := a.autosyncOff(); off || !a.remoteOpsQueued() {
		return
	}
	if !a.takeAutosyncMarker() {
		os.WriteFile(a.againPath(), nil, 0o644)
		return
	}
	if err := a.SpawnSync(a); err != nil {
		os.Remove(filepath.Join(a.Home, ".autosync"))
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

func (a *App) againPath() string { return filepath.Join(a.Home, ".autosync.again") }

// releaseSyncMarker ends a background sync. A write queued while it ran left .autosync.again;
// then the marker is taken back and the outbox pushed again, so that write is not stranded until the next command.
func (a *App) releaseSyncMarker(pushOnly []string) {
	for i := 0; i < 5; i++ {
		os.Remove(filepath.Join(a.Home, ".autosync"))
		if os.Remove(a.againPath()) != nil || !a.takeAutosyncMarker() {
			return
		}
		a.pushRepos(pushOnly, "")
	}
	os.Remove(filepath.Join(a.Home, ".autosync"))
}
