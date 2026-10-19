package issues

import (
	"path/filepath"
	"strings"
)

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
	s, err := readText(filepath.Join(a.Home, "autosync.log"))
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

// autosyncSummary is the one-line status text: "autosync: on, last run <line>" or "autosync: off (<why>)".
func (a *App) autosyncSummary() string {
	off, why := a.autosyncOff()
	last := a.autosyncLast()
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
