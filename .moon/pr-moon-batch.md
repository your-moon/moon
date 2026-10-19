## What changed

One PR, one commit per item. Earlier text for #25, #17 and #18 is in `.moon/pr-25-17-18.md`; this file covers the whole batch.

1. **#25 reads overlay queued writes** (`fcae3ba`). `readIndex`, the path ls, frontier, claims, milestones, status and the TUI share, applies each issue's queued close, reopen, label, milestone and edit ops in queue order (`internal/issues/overlay.go`). Blocked and done follow. Ops whose last send failed stop overlaying; dropped ops are gone. Pull reads `readIndexRaw`.
2. **#17 parallel sync** (`4a26004`). `push` keeps 4 writes in flight (`pushWorkerLimit`). Order kept per issue, per repo's milestone writes, and behind a queued milestone or label create. Longest chains first. One goroutine applies results. 17 writes at 100 ms per call: 1.76 s serial, 0.51 s at 4 workers, 0.44 s at 8 or 16.
3. **#23 `labels new`**. `moon issues labels new <name> [--color RRGGBB] [-D text] [-R repo]` queues a `label_new` op (`gh label create --force`). `checkLabels` accepts a label with a queued create, so `new -L` and `label --add` work offline; any other unknown label is still refused (#20), and the message now names `labels new`. Push holds writes that use the label until its create has landed (`labelWaiting`).
4. **Permanent errors.** A write that fails with "has disabled issues", "Could not resolve to a Repository" or an archived repository gets `Permanent` (stored as `"permanent": true`) and a last error that starts `permanent, no retry:`. Push skips it, and the later writes on the same issue wait. `outbox ls` and `status` show why. `moon issues outbox retry <op>` (or `outbox edit`) clears it. Temporary errors (for example HTTP 502) are retried as before.
5. **Local-only repos.** moon already has `local = true` in `[repos.<alias>]`. Found while testing it: an unscoped `sync` still pushed the queued writes of local-only repos (to `-R ""`); sync now sends only the repos it syncs. For pmddl, change its block in `~/.config/moon/moon.toml` to:
   ```
   [repos.pmddl]
   local = true
   paths = ["~/my/pm-ddl"]
   ```
   (`github` and `local` are mutually exclusive, so the `github = "your-moon/pm-ddl"` line is replaced.) Its five queued writes then stay in the outbox and are never sent; `outbox drop` removes them. Alternative that keeps the GitHub link: leave the config and run `moon issues outbox retry`/`drop` on the five ops once; with item 4 they stop costing 20 s per autosync after the first failure.
6. **Autosync control and visibility.** `MOON_AUTOSYNC=0|false|off|no` (or the old `ISSUES_NO_AUTOSYNC`) and `autosync = false` in `moon.toml` stop reads from starting a background sync; `MOON_AUTOSYNC=1` turns it on over the file. `status` prints `autosync: on|off (<why>)` and the last SYNC line of `autosync.log` (also under `autosync` in `--format json`). Documented in `moon issues help` (usage text) and `help status`.

## Why

Reads were stale until a GitHub round trip, sync was serial, labels forced a GitHub step first, five unsendable writes made every autosync fail for ~20 s, and autosync could neither be seen nor stopped.

## Verification

`go vet ./...` and `go test -race -count=1 ./...` green. A hand-break check for each item turned a test red: skipping the overlay; not skipping failed ops; dropping per-issue order, the in-flight key, the pool limit, `msWaiting`; dropping `labelWaiting`; not counting queued labels; ignoring `Permanent`; never setting `Permanent`; pushing every repo from `sync`; ignoring the autosync switches. Status goldens now carry the autosync line.

## Left from #18, and why

Background push after each write, GraphQL batching of writes, and since-based pull deltas stay open: each is its own design and cannot be checked offline. #17 cuts the sync wait to about the longest chain.

Fixes #25
Fixes #17
Fixes #23
