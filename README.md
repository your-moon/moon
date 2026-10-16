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

## Using it day to day

On a terminal, the reading commands print an aligned table with colours; piped or redirected they print the same
tab-separated text as always, so scripts and agents are unaffected. `--format table|tsv|json` forces one, `--no-color`
(or `NO_COLOR=1`) turns colours off, and `moon issues <command> --help` shows two examples per command.

    moon issues ls -s up -l 10                   # what changed lately (30 rows by default on a terminal)
    moon issues ls 10                            # same limit, shortest form
    moon issues frontier -L kind:bug             # open issues nobody has claimed
    moon issues show app#12                      # one issue, body wrapped to the terminal
    moon issues milestones                       # progress of the open milestones
    moon issues milestones show v1.0             # one milestone and its issues
    moon issues prs -S all                       # pull requests

Every flag has a short form, the same in every command: `-s --sort`, `-l --limit`, `-r --reverse`, `-f --format`
(`-j` = json, `-t` = tsv), `-L --label`, `-m --milestone`, `-S --state`, `-R --repo`, `-a --agent`, `-q --query`,
`-n --no-color`, `-w --working`, `-i --idle`, `-B --labels`, `-h --help`. They combine (`-rl5`), take `-l5`, `-l 5` or `--limit=5`, and `--sort` values can be
abbreviated (`-s up` is `updated`). The long forms keep working.

A sample of `moon issues ls` (120 columns; labels shrink to short names on narrower terminals):

    REF     STATE   STATUS              LABELS                  TITLE
    app#12  open    in review: PR #31   kind:bug,area:ui        login: button does nothing on Safari when the session cookie has…
    app#9   open    working: alice 11m  kind:feature            export: add CSV download
    app#8   open    open                kind:cleanup,area:docs  docs: quickstart mentions a removed flag
    app#5   closed  done (stale claim)  kind:bug                search: slow on big projects
    showing 4 of 4

and of `moon issues milestones` (the footer goes to stderr, so pipes stay clean; ASCII bars when colour is off):

    TITLE  PROGRESS          CLOSED/TOTAL  PCT  OPEN  DUE
    v1.0   ██████░░░░░░░░░░  3/7           42%  4     2026-04-01
    v1.1   ████████░░░░░░░░  1/2           50%  1     -
    showing 2 of 2

Sorting: `--sort number|updated|created|title` (`-s`) and `-r` on `ls`, `frontier` and `prs` (milestones take
`due|progress|title|open`). Percentages round down, so 99.6% reads 99 and only a finished milestone shows 100.
`--milestone` accepts a case-insensitive title or a unique prefix. A typo gets "did you mean ...".

## Work status

Every issue has a derived work status, shown in `ls`, `frontier`, `show`, `claims` and the TUI. Nothing is stored: it is
computed from the issue (state, labels), its claim and the open pull requests in the mirror, so it cannot go stale the way a
claim file does (an agent that finished and never released its claim no longer looks like it is working). The first rule that
matches wins:

| Status | When | Shown as |
|---|---|---|
| `done` | the issue is closed (a claim still on file shows `done (stale claim)` and is listed by `sweep`) | `done` |
| `in review` | an open PR in the mirror says `Closes`, `Fixes` or `Resolves #N` (any case; `alias#N` or `owner/name#N` for this repo; GitHub's own closing references too). Other repos' references and the PR's branch name do not count | `in review: PR #588` |
| `working` | a fresh claim: inside its TTL and, when a pid was recorded on this host, that process is alive | `working: sonnet-17 11m` |
| `stale` | a claim past its TTL, or whose recorded pid on this host is gone: needs a look | `stale: haiku 2h41m` |
| `blocked` | a label containing `blocked`, `needs-decision` or `needs-information`, and no fresh claim | `blocked` |
| `ready` | open, no claim, label `ready` or `<prefix>:ready` | `ready` |
| `open` | everything else | `open` |

The table is data (`WorkRules` in `internal/issues/work.go`): a project can reorder it. `WorkStatus` is one pure function,
unit-tested for every transition and the priority order.

    moon issues ls -w                  # only working
    moon issues ls -i                  # idle: ready or open, nobody on it
    moon issues ls --status stale,blocked      # any of: stale working in-review blocked ready open done
    moon issues ls -s status           # stale first, then working, in review, blocked, ready, open, done
    moon issues claims                 # STATE is the same word; a claim on a closed issue says "done (stale claim)"
    moon issues sweep                  # list claims that are stale or on closed issues (dry run)
    moon issues sweep --release        # release them; a fresh claim on an open issue is never touched

`-w`, `-i` and `--status` add up (`-w -i` is working or idle). Piped output keeps its exact bytes; a trailing status column
(or, for `show`, a `status:` line right after `claim:`) is added only when you ask: `--show-status`, `-w`, `-i`,
`--status`, or `-s status`. On a terminal the STATUS column is coloured (working green, in review blue, stale yellow,
blocked red, done dim). `frontier` still means open issues with no fresh claim. A frozen sample (invented data):

    REF     STATE   STATUS                  LABELS                              TITLE
    app#31  open    blocked                 kind:bug,area:payments-gateway +3   checkout: card form loses focus after the 3…
    app#29  closed  done (stale claim)      kind:bug,area:auth                  login: crash on empty password
    app#28  open    working: sonnet-17 11m  kind:bug,area:auth                  auth: refresh token loops
    app#27  open    in review: PR #61       kind:feature,area:web               export: add CSV download
    app#26  open    ready                   kind:bug,triage:ready               docs: explain config lookup
    app#25  open    stale: haiku 2h41m      kind:cleanup                        chore: bump the linter

## Labels

The LABELS column is as wide as the widest label list on screen, at most 45 percent of the terminal; the title gives way
first. A label is never cut mid-name: what does not fit is dropped whole from the end and counted, `kind:bug,area:web +2`.
Labels are ordered `kind:*`, `area:*`, `triage:*`, then the rest alphabetically (piped output keeps the stored order).
`show` and the TUI detail pane list every label in full, wrapped.

`-B/--labels full|short|none` picks the display: `full` is the stored names, `short` drops the `kind:`, `area:` and `triage:`
prefixes and collapses `::`, `none` hides the column. The default follows the terminal: full from 110 columns, short from
70, none below. What is shown is what `-L/--label` accepts: the stored name (`kind:bug`), the shown name (`bug`), or any
unique suffix after a colon (`ready` matches `triage:ready`). If two labels end the same way the command prints the
candidates and exits 2.

## Colour

Colour is only for tables and the TUI; piped, TSV and JSON output never contain an escape code. It is off for `NO_COLOR`,
`-n/--no-color`, `--color never`, `TERM=dumb` and when stdout is not a terminal. `--color always` forces it and prints the
table even when piped (`moon issues ls --color always | less -R`; it beats `NO_COLOR`, and `--no-color` beats it);
`CLICOLOR_FORCE=1` enables colour for tables without changing what a pipe prints. Depth: truecolor when `COLORTERM` is
`truecolor`/`24bit`, 256 colours when `TERM` says `256color`, else the 16 basic colours; every colour has a fallback at each depth.

Colours are roles, not escapes: `state-open state-closed state-merged`, `status-working status-review status-stale
status-blocked status-ready status-done`, `label-kind label-area label-triage label-other`, `bar-low bar-mid bar-high
bar-empty` (milestone bars: red under 25 percent, yellow 25-74, green 75 and up), `claim-fresh claim-stale muted accent
warning error`, each with a dark and a light value. The theme is `MOON_THEME=light|dark`, else the config, else
`COLORFGBG`, else dark. The TUI builds its styles from the same palette. Labels are drawn as chips in their GitHub colour
(`pull` also saves `<alias>/labels.tsv`: name, colour, description), with black or white text chosen by WCAG contrast; a label
with no known colour gets a fixed colour from its prefix. Optional overrides in `moon.toml`, validated at load:

    [colors]
    theme = "light"                  # or "dark"
    status-working = "#2da44e"       # a #rrggbb colour ...
    state-closed = "red"             # ... or an ANSI name (black red green yellow blue magenta cyan white, bright-<name>, gray)
    bar-fill = "#0969da"             # sets bar-low, bar-mid and bar-high at once

A bad key or value fails with the file, line and key (`moon.toml:7: [colors] unknown key "stat-working" (did you mean status-working?)`).
Only the first 100 labels of a repo are fetched for colours.

## Tab completion

`moon completion zsh|bash|fish` prints the completion script to stdout. It writes no file and needs no sudo.

    source <(moon completion zsh)            # zsh: add to ~/.zshrc (after compinit, or let the script run it)
    source <(moon completion bash)           # bash: add to ~/.bashrc
    moon completion fish | source            # fish: add to ~/.config/fish/config.fish

For a permanent install without running moon at every shell start, save the output once:

    moon completion zsh  > "${fpath[1]}/_moon"                  # zsh: any directory on $fpath, then restart the shell
    moon completion bash > ~/.local/share/bash-completion/completions/moon
    moon completion fish > ~/.config/fish/completions/moon.fish

It completes commands and subcommands, long and short flags (with descriptions in zsh and fish), enum values
(`--sort`, `--state`, `--format`, `--reason`), repo aliases for `-R`, labels for `-L` (stored names and the short display
names), milestone titles for `-m` (quoted when they have spaces), `--config` as a file, and refs for the commands that take
one: `moon issues show er<Tab>` offers `erp#512  title...`, newest first, at most 200, filtered by what you typed
(`erp#5` offers `erp#5xx`). `claim` offers only unclaimed open issues, `release` only claimed ones, `reopen` only closed
ones. Candidates come from the local mirror only: never GitHub, never an autosync, and with no `moon.toml` found nothing is
offered and nothing is printed. The scripts only call `moon __complete <words...>` (hidden; one candidate per line, optional
`<TAB>description`, last line `:<directive>`), so all logic is in Go and tested there.

## Commands

`ls show milestones prs pr show frontier claim release claims sweep status new comment close reopen label milestone edit push pull sync prune`,
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

Flags: `--config FILE`, `--dir MIRROR`, `--agent NAME` (claim holder, default `$USER`), `--color auto|always|never`, `--no-color`. The config is found the same way as for
`moon issues`; if none is found the screen lists the four places searched. It reads the local mirror only. Writes (`c u x m M`) are
queued locally through the same code as the CLI, and the footer shows `queued: N ops` (operations, not issues: two ops on one issue read 2). There is no push, pull or sync in the UI: run
`moon issues sync` yourself. `NO_COLOR` is respected (plain text, ASCII progress bars); colours come from the palette above. Mouse wheel scrolls, a click selects.
Under 100 columns the detail stacks under the list; under 60 columns only the list shows and `enter` opens the detail.

**Agents and scripts must not use the TUI**: it waits for keys. `moon tui` and `moon issues tui` refuse unless stdin and stdout
are terminals (exit 2), and always refuse when `MOON_NO_TUI` is set to anything; use `moon issues ls` / `show` instead.

In the dashboard `Tab` completes with the same engine as the shell. In the `/` search box it completes the word under the
cursor from refs, label names and milestone titles, and understands the filter tokens `label:bug`, `milestone:"v1 beta"`,
`status:working` (any work status: `working`, `in review`, `stale`, `blocked`, `ready`, `open`, `done`; comma-separated for several) and `repo:erp`. One match fills the word, several fill their common
prefix and show a list; `Tab` again cycles, `Shift-Tab` goes back, `Esc` closes the list. `:` opens a command line
(`:claim erp#5<Tab>`, `release close reopen comment label milestone sort filter repo q`), completed like the shell and run
through the same write path as the CLI; errors show in the footer and `Up`/`Down` browse the history. The `M` picker and the
`f` label and milestone lists (`/` types to narrow) use the same Tab behavior.

| Key | Action |
|---|---|
| `1`-`9`, `[` `]` | switch tab |
| `j` `k`, arrows, `g` `G`, `pgup` `pgdn` | move |
| `/` | live search, the same match as `ls -q` (title or labels), plus `label:` `milestone:` `status:` `repo:` tokens; `tab` completes, `esc` clears |
| `:` | command line: `claim release close reopen comment label milestone sort filter repo q`; `tab` completes, up/down history |
| `s`, `r` | cycle sort (number, updated, created, title, status), reverse |
| `w` | only issues being worked on now (status working); again for all |
| `l` | labels column: full, short, hidden (the footer shows the mode; default by width: full from 110, short from 70) |
| `f` | filter menu: state, label, milestone, clear |
| `tab` | next repo alias, when there are several |
| `enter`, `esc` | open the detail full screen, go back |
| `c`, `u` | claim, release; refused (never forced) when another agent holds it |
| `x` | close or reopen (queued) |
| `m` | comment (queued; opens `$EDITOR`, else a one-line prompt) |
| `M` | set the milestone: type to filter, `-` for none (queued) |
| `?`, `q` | help, quit |

Milestones tab: `enter` opens the Issues tab filtered to that milestone (`esc` clears the chip), `s` cycles due, progress, title and open
count, `x` hides fully closed milestones. Claims tab: the same data as `moon issues claims` (STATE is the status word), with a `!` marker and dimming on claims that are stale or sit on a closed issue; `moon issues sweep --release` clears them.
The Issues list has a STATUS column (`done!` = closed but still claimed), the detail pane a `status:` line after `claim:`. (`l` now cycles labels; it no longer opens the detail, `enter` and the right arrow still do.)

Issues tab at 120 and 80 columns (invented data; colours not shown):

```
[1 Issues]  2 Milestones   3 Claims
  REF    STATE  STATUS    LABELS                         TITLE          │ app#31  open
> app#31 open   blocked   kind:bug +4                    checkout: card…│ checkout: card form loses focus after the 3-D
  app#30 open   blocked   kind:feature +1                export: which …│ secure redirect
@ app#28 open   working   kind:bug,area:auth             auth: refresh …│
  app#27 open   in review kind:feature,area:web          export: add CS…│ labels: kind:bug, area:payments-gateway,
  app#26 open   ready     kind:bug,triage:ready          docs: explain …│         triage:needs-information,
  app#25 open   stale     kind:cleanup                   chore: bump th…│         customer-reported, priority-high
  app#24 open   ready     kind:bug,area:web,triage:ready search: slow o…│ milestone: -
                                                                        │ author: sam
                                                                        │ claim: none
                                                                        │ status: blocked
                                                                        │ ──────────────────────────────────────────────
j/k move  / search  s sort  f filter  w working  l labels  c claim  u release  x close  m comment  ? help  q quit
7/8  state:open  sort:number↓  labels:full  |  mirror pulled 0 min ago
```

```
[1 Issues]  2 Milestones   3 Claims
  REF    STATE  STATUS    LABELS                  TITLE
> app#31 open   blocked   bug,payments-gateway +3 checkout: card form loses foc…
  app#30 open   blocked   feature,needs-decision  export: which CSV dialect do …
@ app#28 open   working   bug,auth                auth: refresh token loops
  app#27 open   in review feature,web             export: add CSV download
  app#26 open   ready     bug,ready               docs: explain config lookup
  app#25 open   stale     cleanup                 chore: bump the linter
  app#24 open   ready     bug,web,ready           search: slow on big projects
────────────────────────────────────────────────────────────────────────────────
 app#31  open
 checkout: card form loses focus after the 3-D secure redirect
 labels: kind:bug, area:payments-gateway, triage:needs-information,
         customer-reported, priority-high
 milestone: -
 author: sam
 claim: none
 status: blocked
 ───────────────────────────────────────────────────────────────────────────────
 Steps are in the first comment.
j/k move  / search  s sort  f filter  w working  l labels  c claim  u release  …
7/8  state:open  sort:number↓  labels:short  |  mirror pulled 1 min ago
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
