package tui

import "testing"

// moon#41: inside the dashboard, ":claim" Tab offers the agent's own claim (renew), and ":release" offers
// only that claim, not robin's.
func TestColonClaimOffersOwnClaim(t *testing.T) {
	mi := newMirror(t, compItems, "erp")
	mi.claim("erp#2", "robin", 600, 7200)
	mi.claim("erp#3", "me", 600, 7200)
	m := mi.model(120, 30)
	m = press(typed(press(m, ":"), "claim erp#"), "tab")
	eq(t, "own claim is offered, robin's is not", cands(m), "erp#510 erp#51 erp#50 erp#3")
	m = press(typed(press(m, "esc", "esc", ":"), "release erp#"), "tab")
	eq(t, "release: only mine", m.input.Value(), "release erp#3")
}
