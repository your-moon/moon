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

`moon.toml` is found by walking up from the current directory, or via `--config FILE` / `$MOON_CONFIG`.
Each `[repos.<alias>]` declares one repository (`github = "owner/name"`, alias is lowercase `[a-z0-9_]+`); refs look like
`<alias>#123`. The optional top-level `mirror_dir = "path"` (default `.moon/issues` beside `moon.toml`) holds the mirror.

## Commands

`ls show milestones prs pr show frontier claim release claims status new comment close reopen label milestone edit push pull sync prune`,
plus `moon version`. Run `moon issues help` for the flags. Environment: `ISSUES_NO_AUTOSYNC=1` turns off the background refresh of a
mirror older than 10 minutes, `ISSUES_KEEP_DAYS` (default 30) is how long closed items keep their file, `ISSUES_GH` points at another `gh` binary.

## Build and test

    go build -o dist/moon ./cmd/moon
    go test ./...

Tests use a fake GitHub and never touch the network. The on-disk layout (`<alias>/index.tsv`, `<alias>/<n>.md`, `<alias>/pr/`,
`claims/`, `outbox/`, `localmap.tsv`) is the same as the original Python tool's, so both can work on one mirror.

## License

MIT, see LICENSE.
