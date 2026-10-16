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
plus `moon version`. Run `moon issues help` for the flags. Environment: `ISSUES_NO_AUTOSYNC=1` turns off the background refresh of a
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

## Build and test

    go build -o dist/moon ./cmd/moon
    go test ./...

Tests use a fake GitHub and never touch the network. The on-disk layout (`<alias>/index.tsv`, `<alias>/<n>.md`, `<alias>/pr/`,
`claims/`, `outbox/`, `localmap.tsv`) is the same as the original Python tool's, so both can work on one mirror.

## License

MIT, see LICENSE.
