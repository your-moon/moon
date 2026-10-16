# moon

A single static binary of small developer tools. The first is `moon issues`: a local-first mirror of
GitHub issues and pull requests, with agent claims and a write queue. Reads are instant files on disk;
writes queue locally and `moon issues sync` sends them to GitHub through the `gh` CLI.

## Install

    go install github.com/your-moon/moon/cmd/moon@latest

or download a release binary and put it on your `PATH`. You also need the [`gh`](https://cli.github.com) CLI, logged in.

## Quickstart

    cat > moon.toml <<'TOML'
    [repos.app]
    github = "owner/app"
    TOML
    moon issues pull              # mirror issues and PRs into .moon/issues
    moon issues ls --label bug    # list (reads the mirror, no network)
    moon issues show app#12
    moon issues claim app#12 --agent me   # local claim; exit 3 if someone else holds it
    moon issues comment app#12 --body "on it"; moon issues sync

Config lookup, first hit wins: `--config FILE`, `$MOON_CONFIG`, then `moon.toml` (or `.moon/moon.toml`) walking up
from the current directory, then `$XDG_CONFIG_HOME/moon/moon.toml` (default `~/.config/moon/moon.toml`).
Put a `moon.toml` in that last place to run `moon issues` from any directory.
Each `[repos.<alias>]` declares one repository (`github = "owner/name"`, alias is lowercase `[a-z0-9_]+`); refs look like
`<alias>#123`. The optional top-level `mirror_dir = "path"` (default `.moon/issues` beside `moon.toml`) holds the mirror.

## Commands

`ls show milestones prs pr show frontier claim release claims status new comment close reopen label milestone edit push pull sync prune`,
`tui` (also `moon tui`, see Terminal UI), plus `moon version`. Run `moon issues help` for the flags. Run `moon tui` for the interactive dashboard. Environment: `ISSUES_NO_AUTOSYNC=1` turns off the background refresh of a
mirror older than 10 minutes, `ISSUES_KEEP_DAYS` (default 30) is how long closed items keep their file, `ISSUES_GH` points at another `gh` binary.

## Local numbering

A new issue that is not on GitHub yet gets a short sequential id per repo alias: `app#L-1`, `app#L-2`, ... Numbers come
from `<mirror>/<alias>/local-seq`, updated under an exclusive file lock, and are never reused, even after the issue is
closed or pushed. After a push lands, `app#L-3` keeps resolving to the real issue through `localmap.tsv`, and `show` prints
`ref: L-3 (now #612)`. In `ls`, unpushed issues carry a trailing `local` column. Older ids of the form
`L-<14 digits>-<4 hex>` stay valid.

## Local-only trackers

A repo with no GitHub behind it: declare `local = true` instead of `github`. Issues are numbered `notes#1`, `notes#2`, ...
and live only in the mirror; `push`, `pull` and `sync` print "alias is local-only: nothing to sync" and exit 0.

    [repos.notes]
    local = true

    moon issues new --repo notes --title "buy milk" --body "two litres"   # ref notes#1
    moon issues ls --repo notes; moon issues close notes#1

Exactly one of `github` or `local = true` is allowed per repo. Everything else (`ls show claim milestones label comment close
reopen edit status`) works as for a GitHub repo, with the same on-disk layout.

## Terminal UI

    moon tui                 # dashboard: Issues, Milestones, Claims
    moon issues tui          # the same dashboard, opened on the Issues tab

Flags: `--config FILE`, `--dir MIRROR`, `--agent NAME` (claim holder, default `$USER`). The config is found the same way as for
`moon issues`; if none is found the screen lists the four places searched. It reads the local mirror only. Writes (`c u x m M`) are
queued locally through the same code as the CLI, and the footer shows `queued: N`. There is no push, pull or sync in the UI: run
`moon issues sync` yourself. `NO_COLOR` is respected (plain text, ASCII progress bars). Mouse wheel scrolls, a click selects.
Under 100 columns the detail stacks under the list; under 60 columns only the list shows and `enter` opens the detail.

| Key | Action |
|---|---|
| `1`-`9`, `[` `]` | switch tab |
| `j` `k`, arrows, `g` `G`, `pgup` `pgdn` | move |
| `/` | live search, the same match as `ls -q` (title or labels); `esc` clears |
| `s`, `r` | cycle sort (number, updated, created, title), reverse |
| `f` | filter menu: state, label, milestone, clear |
| `tab` | next repo alias, when there are several |
| `enter`, `esc` | open the detail full screen, go back |
| `c`, `u` | claim, release; refused (never forced) when another agent holds it |
| `x` | close or reopen (queued) |
| `m` | comment (queued; opens `$EDITOR`, else a one-line prompt) |
| `M` | set the milestone: type to filter, `-` for none (queued) |
| `?`, `q` | help, quit |

Milestones tab: `enter` opens the Issues tab filtered to that milestone (`esc` clears the chip), `s` cycles due, progress, title and open
count, `x` hides fully closed milestones. Claims tab: the same data as `moon issues claims`, stale claims dimmed.

Issues tab at 80 columns (invented data):

```
  REF    STATE  MILESTONE  LABELS           TITLE
  app#7  open   v1         feature,area::a… auth: add passkey support
> app#5  open   v2         bug              widget: renders blank for ünïcode a…
  app#4  open              docs             docs: explain config lookup
  app#3  open   v2         feature          export: add CSV download
@ app#2  open   v1         bug,area::auth   auth: token refresh loops
────────────────────────────────────────────────────────────────────────────────
 app#5  open
 widget: renders blank for ünïcode and 日本語 titles

 labels: bug
 milestone: v2
 author: sam
 claim: none
 ───────────────────────────────────────────────────────────────────────────────













j/k move  / search  s sort  f filter  c claim  u release  x close  m comment  ?…
5/7  state:open  sort:number↓  |  mirror pulled 5 min ago
```

Milestones tab:

```
 1 Issues  [2 Milestones]  3 Claims
  MILESTONE      PROGRESS                  DONE    PCT   OPEN  CLOSED DUE
> alpha          [#######################] 9/9     100%  0     9      2026-01-01
  v2             [#####------------------] 1/4     25%   3     1      2026-03-01
  v1             [#############----------] 6/10    60%   4     6      2026-03-10
  v3             [-----------------------] 0/0     0%    0     0      -
  (no milestone) [###########------------] 1/2     50%   1     1      -





j/k move  enter issues  s sort  x hide done  r reload  q quit
5 shown of 5  sort:due
```

## Build and test

    go build -o dist/moon ./cmd/moon
    go test ./...

Tests use a fake GitHub and never touch the network. The on-disk layout (`<alias>/index.tsv`, `<alias>/<n>.md`, `<alias>/pr/`,
`claims/`, `outbox/`, `localmap.tsv`) is the same as the original Python tool's, so both can work on one mirror.

## License

MIT, see LICENSE.
