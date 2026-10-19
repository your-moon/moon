package completion

import "testing"

// moon#40: the dashboard's lines and the shell path split words the same way.
func TestSplittersAgree(t *testing.T) {
	for _, line := range []string{
		`comment app#1 "cost \$5"`,
		"comment app#1 \"run \\`id\\`\"",
		`comment app#1 "a \"q\" \\ b"`,
		`comment app#1 'it''s' x\ y`,
		`comment app#1 "open`,
		`comment app#1 `,
		``,
	} {
		ws, q := SplitWords(line)
		sw, raw, sq := splitLine(line)
		if q != sq || len(ws) != len(sw) {
			t.Errorf("%q: SplitWords %v quote %q, splitLine %q quote %q", line, ws, q, sw, sq)
			continue
		}
		for i := range ws {
			if ws[i].Text != sw[i] {
				t.Errorf("%q word %d: SplitWords %q, splitLine %q", line, i, ws[i].Text, sw[i])
			}
		}
		_ = raw
	}
	ws, _ := SplitWords(`comment app#1 "cost \$5"`)
	if got := ws[len(ws)-1].Text; got != "cost $5" {
		t.Errorf("dashboard line: last word %q, want %q", got, "cost $5")
	}
	ws, _ = SplitWords("x \"run \\`id\\`\"")
	if got := ws[len(ws)-1].Text; got != "run `id`" {
		t.Errorf("backtick: last word %q", got)
	}
}
