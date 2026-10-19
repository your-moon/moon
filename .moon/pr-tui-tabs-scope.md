## What changed

The Milestones and Claims tabs of `moon tui` now follow the same repo scope as the Issues tab.

- `Shell` hands the Issues tab's repo filter to every tab that implements `scopedTab` (`SetScope`), at start and whenever the Issues filter changes (`a`, `tab`, `:filter repo:X`, `:filter clear`). A milestone drill-in still narrows only the Issues list.
- `a` and `tab` pressed in the Milestones or Claims tab are routed to the Issues tab's own logic, so the three tabs always agree.
- Both tabs show the scope text in their footer, as the Issues tab does (`scope: falcon (paths in config)`).
- With `--all` or `-R all` the scope is empty and every tab lists every repo.

## Why

Started inside a configured repo path, `moon tui` opened Issues on that repo while Milestones and Claims listed every repo.

## How verified

- `go vet ./...` and `go test ./...` pass.
- New `internal/tui/tabs_scope_test.go`: scoped session shows only that alias in milestones and claims; `a` toggles to all and back; `tab` wraps like Issues; `:filter repo:app` carries over; a global scope shows all.
- Mutations turn the tests red: dropping the milestone filter, making the claims filter accept every repo, and never syncing the scope from Issues.

## Look first

`Shell.forwardTo` and `syncScope` in `internal/tui/shell.go`.
