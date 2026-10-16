# moon plan
1. `moon issues`: port scripts/issues.py (local mirror of GitHub issues and PRs, claims, write queue). Same on-disk format as the Python tool, so both run side by side.
2. `moon run`, `moon ship`: port run.py, ship.py, durable.py.
3. `moon tick`, `moon ci`, `moon smoke`, `moon check`, `moon pstack`.
4. Delete each Python script only after its Go version passes the same tests on the same data.
Rules: Go, standard library first, one static binary, no Python at runtime. Port behavior, not code. Every ported command is compared with the Python output on a copy of the real data.
