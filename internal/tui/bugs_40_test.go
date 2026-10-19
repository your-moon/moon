package tui

import "testing"

// moon#40: ":filter label:X" and "milestone:X" resolve names like the "/" search does.
func TestColonFilterResolvesLikeSearch(t *testing.T) {
	m, _ := compModel(t, 120, 30)
	m = press(typed(press(m, "/"), "label:bug"), "enter")
	want := refs(m)
	if want != "erp#51 erp#2" {
		t.Fatalf("search label:bug: %q", want)
	}
	m = press(m, "esc", "esc")
	m = press(typed(press(m, ":"), "filter label:bug"), "enter")
	eq(t, ":filter label:bug matches kind:bug like /label:bug", refs(m), want)
	m = press(typed(press(m, ":"), "filter clear"), "enter")
	m = press(typed(press(m, ":"), "filter milestone:none"), "enter")
	eq(t, ":filter milestone:none", refs(m), "erp#510 erp#50")
}
