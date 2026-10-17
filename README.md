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
The optional top-level `claim_ttl = "30m"` is the default lease for `moon issues claim` when `--ttl` is not given (120m when absent). A claim with no `--pid` looks alive until its lease ends, so a short lease keeps a killed agent from looking busy for hours; the agent keeps its claim alive by running `claim` again with the same `--agent` (that renews it).

## Using it day to day

On a terminal, the reading commands print an aligned table with colours; piped or redirected they print the same
tab-separated text as always, so scripts and agents are unaffected. `--format table|tsv|json` forces one, `--no-color`
(or `NO_COLOR=1`) turns colours off, and `moon issues <command> --help` shows two examples per command.

    moon issues ls -s up -l 10                   # what changed lately (30 rows by default on a terminal)
    moon issues ls 10                            # same limit, shortest form
    moon issues frontier -L kind:bug             # open issues nobody has claimed
    moon issues show app#12                      # one issue, body wrapped to the terminal
    moon issues show app#12 --format json        # one issue as JSON: ls fields plus body, assignees, author, url, claim
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

An empty body is refused where it would do harm: `edit --body-file F` (or `--body ""`) with a blank body, `new --body-file F` with
a blank file, and `comment` with a blank body exit 2 and queue nothing, because an empty input almost always means the script
that wrote it failed (an empty `edit` erases the issue body on GitHub). `--allow-empty-body` on `new` and `edit` says it is meant.

`new -L` and `label --add` check each label against the repo's mirrored `labels.tsv` and refuse one GitHub does not have (exit 2,
nothing queued, close matches listed), so a typo does not leave a write stuck in the outbox. A label that differs only in case is
queued in GitHub's spelling. moon never creates labels: add it on GitHub and `moon issues pull`. Nothing is checked for a repo that
was never pulled, a local-only repo, or when `labels.tsv` holds 100 labels (`pull` fetches no more, so it may be cut off).
`--remove` is not checked.

## Fixing a write that sync cannot send

A queued write that fails stays in the outbox and is retried by every `sync`. `moon issues outbox` is how to look at it and repair it:

    moon issues outbox                              # = outbox ls: id, op, ref, tries, what it does, last error (-j adds the args)
    moon issues outbox edit 3f9a21c0 --label bug --label area::web   # a queued new: replace its labels
    moon issues outbox edit 3f9a21c0 --title T --body-file fixed.md  # title and body (a new keeps its local-id marker)
    moon issues outbox edit 5be7d210 --add bug --remove stale        # a queued label write
    moon issues outbox drop 3f9a21c0                # remove a queued write
    moon issues outbox drop 3f9a21c0 --cascade      # a queued new that other writes act on: drop those too

`<op>` is the op id from `outbox ls` or any piece of it that only one op has (at least 4 characters; the table shows the last 8).
`edit` changes `new` (title, body, `--label` replaces the list, `--milestone`), `edit` (title, body), `comment` (body), `label`
(`--add`, `--remove`) and `milestone` writes; the fields are checked like the original command (unknown labels and blank bodies are
refused), and the old error is cleared so the next `sync` shows whether the fix worked. Both `edit` and `drop` refuse while a push is
running. Dropping a queued `new` that other writes act on needs `--cascade`, because they could never be sent without it.

## Projects and scope

Commands work on the project you are in. When you are not in one, they show every repo. The scope is decided once at
startup, first match wins:

1. `-R/--repo ALIAS` (repeatable) names the repos. `-R all` or `--all` forces every repo.
2. A `moon.toml` (or `.moon/moon.toml`) in the current folder or a parent that declares exactly one repo.
3. `paths` of a repo: folders that belong to it, for work that is not a git checkout. `~` is your home, a relative path is
   relative to `moon.toml`, the longest matching path wins, and symlinks are resolved. Local-only repos are matched this way too.
4. The git remote `origin` (`git remote get-url origin`, local only, 1 second at most), in ssh or https form, matched
   case-insensitively as `owner/name` against every `github` value. The host and any credentials in the URL are ignored.
5. Otherwise every configured repo.

    [repos.app]
    github = "octocat/app"
    paths  = ["~/work/app", "/srv/app"]     # optional, one line

`ls frontier prs milestones claims status sweep` default to the scope, as do `sync pull push prune` and the milestone commands
(`milestones new|edit|close|reopen` need no `-R` inside a project). `show claim release comment
close reopen label milestone edit` take a bare number (`585` or `#585`) for the scope's single repo; with several repos in
scope the number must exist in exactly one (otherwise they exit 2 and list the candidates). `new` needs no `--repo` inside a
project and refuses in all-repos mode, listing the aliases. Refs with an alias (`lib#3`) work everywhere, and `-R` always wins.

On a terminal every list starts with a line on stderr, `scope: app (git remote origin)` or
`scope: all repos (not inside a project)`, and lists over several repos get a REPO column. Piped and tab-separated output is
unchanged byte for byte, with one intended difference: inside a project the default repo filter applies, so
`moon issues ls | wc -l` counts that project only. Use `-R all` (or `--all`) to get every repo as before.

`moon issues where` prints the resolved scope, the reason, the config file in use and what each rule found, to debug a
scope that is not what you expected (the remote is shown as `owner/name` only). `moon tui` opens on the scoped repo, `a`
toggles all repos, and the footer names the scope; it takes `-R` and `--all` too. Tab completion (shell and TUI) offers the scope's repo
first: refs, labels and milestones come from it, a bare number completes when one repo is in scope, and `-R all`/`--all` widen it. The Milestones and Claims tabs still show every repo.

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
    moon issues sweep --live --agent sonnet-17 --release   # stop work: also drop that agent's LIVE claims (dry run without --release)

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

`ls show milestones (new edit close reopen show) prs pr show frontier claim release claims sweep status new comment close reopen label milestone edit outbox (ls edit drop) push pull sync prune where`,
`tui` (also `moon tui`, see Terminal UI), plus `moon version`. Run `moon issues help` for the flags. Run `moon tui` for the interactive dashboard. Environment: `ISSUES_NO_AUTOSYNC=1` turns off the background refresh of a
mirror older than 10 minutes, `ISSUES_KEEP_DAYS` (default 30) is how long closed items keep their file, `ISSUES_GH` points at another `gh` binary.

## Milestones

How work is tracked: **big work is a milestone with issues under it; small work is a single issue.** Everything is created
locally first and synced to the git provider afterwards, so nothing waits for the network.

    moon issues milestones new "auth rewrite" -d 2026-12-31 -D "move login to the new session store" -R app   # app#M-1
    moon issues new -R app -m M-1 --title "auth: sessions expire early" --body "..."    # M-1 works wherever a title does
    moon issues milestones edit M-1 -d 2027-01-15            # or --title T, -D text, -d none
    moon issues milestones show "auth rewrite"               # due date, description, open/closed counts, its issues
    moon issues sync                                         # creates the milestone on GitHub first, then the issues
    moon issues milestones close "auth rewrite"              # reopen undoes it; sync sends both
    moon issues new -R app --title "docs: typo" --body "..." # small work: one issue, no milestone

`milestones new|edit|close|reopen` queue like every other write (`M-1`, `M-2`, ... are sequential per repo, never reused, and
resolve to the real number through `localmap.tsv` after the sync). Titles are unique per repo, ignoring case; a duplicate is
refused. Due dates are `YYYY-MM-DD`. A title that already exists on GitHub is adopted, not created twice, and a sync that
died half way is safe to run again. A repo with `local = true` has no sync: the same commands write the mirror directly and
milestones get plain numbers. `<alias>/milestones.tsv` holds number, local id, title, state, due, description and update time;
`pull` fills it from GitHub, and the open/closed counts always come from the issues. Tab completion offers titles and `M-n` ids for
`milestones edit|close|reopen` (close: open ones, reopen: closed ones) and for `-m`.

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

Flags: `--config FILE`, `--dir MIRROR`, `-R ALIAS`, `--all`, `--agent NAME` (claim holder, default `$USER`), `--color auto|always|never`, `--no-color`. The config is found the same way as for
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
| `a` | all repos, or back to the project's repo |
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
