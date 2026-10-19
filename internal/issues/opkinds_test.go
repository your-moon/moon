package issues

import "testing"

// A new op kind cannot be half-added: every kind in the table has all the behaviours that have no sane default.
func TestOpKindsComplete(t *testing.T) {
	want := []string{"new", "comment", "close", "reopen", "label", "milestone", "edit", "label_new", "ms_new", "ms_close", "ms_reopen", "ms_edit"}
	for _, name := range want {
		if opKinds[name] == nil {
			t.Errorf("kind %q is missing from the table", name)
		}
	}
	for name, k := range opKinds {
		if k.name != name {
			t.Errorf("%s: table key and kind name differ (%s)", name, k.name)
		}
		if k.describe == nil {
			t.Errorf("%s: no describe", name)
		}
		if k.applyEff == nil {
			t.Errorf("%s: no apply to the effective view", name)
		}
		if k.applyLocal == nil {
			t.Errorf("%s: no apply to a local-only issue file", name)
		}
		if k.applyMs == nil {
			t.Errorf("%s: no apply to a milestone record", name)
		}
		if k.send == nil {
			t.Errorf("%s: no send", name)
		}
	}
}

// An unknown kind is an error wherever it is looked up, never a silent no-op.
func TestUnknownOpKindIsError(t *testing.T) {
	if _, err := kindOf("assign"); err == nil {
		t.Fatal("kindOf accepted an unknown kind")
	}
	if err := applyOps(&eff{}, []*Op{{Op: "assign"}}); err == nil {
		t.Fatal("applyOps accepted an unknown kind")
	}
	e := newEnv(t)
	if _, err := e.app().execute(&Op{Op: "assign", Ref: "x#1"}); err == nil {
		t.Fatal("execute accepted an unknown kind")
	}
	if err := e.app().applyLocalOnly("assign", "x#1", nil); err == nil {
		t.Fatal("applyLocalOnly accepted an unknown kind")
	}
}
