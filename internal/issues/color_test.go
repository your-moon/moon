package issues

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestDetectColorMatrix(t *testing.T) {
	for _, c := range []struct {
		name    string
		env     []string
		tty     bool
		noColor bool
		mode    string
		on      bool
		depth   int
	}{
		{"tty, nothing set", nil, true, false, "auto", true, Depth16},
		{"not a tty", nil, false, false, "auto", false, Depth16},
		{"NO_COLOR on a tty", []string{"NO_COLOR", "1"}, true, false, "auto", false, Depth16},
		{"NO_COLOR empty does not count", []string{"NO_COLOR", ""}, true, false, "auto", true, Depth16},
		{"--no-color", nil, true, true, "auto", false, Depth16},
		{"--color never", nil, true, false, "never", false, Depth16},
		{"TERM=dumb", []string{"TERM", "dumb"}, true, false, "auto", false, Depth16},
		{"--color always, piped", nil, false, false, "always", true, Depth16},
		{"--color always beats NO_COLOR", []string{"NO_COLOR", "1"}, false, false, "always", true, Depth16},
		{"--color always beats TERM=dumb", []string{"TERM", "dumb"}, true, false, "always", true, Depth16},
		{"--no-color beats --color always", nil, true, true, "always", false, Depth16},
		{"CLICOLOR_FORCE piped", []string{"CLICOLOR_FORCE", "1"}, false, false, "auto", true, Depth16},
		{"CLICOLOR_FORCE=0 is off", []string{"CLICOLOR_FORCE", "0"}, false, false, "auto", false, Depth16},
		{"NO_COLOR beats CLICOLOR_FORCE", []string{"NO_COLOR", "1", "CLICOLOR_FORCE", "1"}, false, false, "auto", false, Depth16},
		{"256 colour terminal", []string{"TERM", "xterm-256color"}, true, false, "auto", true, Depth256},
		{"COLORTERM=truecolor", []string{"TERM", "xterm-256color", "COLORTERM", "truecolor"}, true, false, "auto", true, DepthTrue},
		{"COLORTERM=24bit", []string{"COLORTERM", "24bit"}, true, false, "auto", true, DepthTrue},
		{"COLORTERM=yes is not truecolor", []string{"COLORTERM", "yes"}, true, false, "auto", true, Depth16},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := DetectColor(envOf(c.env...), c.tty, c.noColor, c.mode)
			if got.On != c.on || (c.on && got.Depth != c.depth) {
				t.Fatalf("got %+v want on=%v depth=%d", got, c.on, c.depth)
			}
		})
	}
}

func TestContrastPicksTheReadableText(t *testing.T) {
	black, white := RGB{0, 0, 0}, RGB{255, 255, 255}
	if r := Contrast(black, white); r < 20.9 || r > 21.1 {
		t.Fatalf("black on white should be 21:1, got %.2f", r)
	}
	for _, c := range []struct {
		hex  string
		text RGB
	}{
		{"#ffffff", black}, {"#000000", white}, {"#fbca04", black}, {"#e4e669", black}, {"#c2e0c6", black},
		{"#f9d0c4", black}, {"#bfdadc", black}, {"#0052cc", white}, {"#5319e7", white}, {"#b60205", white}, {"#006b75", white},
	} {
		col, err := ParseColor(c.hex)
		if err != nil {
			t.Fatal(err)
		}
		if got := TextOn(col.RGB); got != c.text {
			t.Errorf("%s: text %v want %v (contrast black %.2f white %.2f)", c.hex, got, c.text, Contrast(col.RGB, black), Contrast(col.RGB, white))
		}
		// whichever it picked is the better of the two
		other := black
		if c.text == black {
			other = white
		}
		if Contrast(col.RGB, c.text) < Contrast(col.RGB, other) {
			t.Errorf("%s: picked the worse text colour", c.hex)
		}
	}
}

func TestDepthFallbacks(t *testing.T) {
	pal := NewPalette("dark", nil) // status-review is #58a6ff
	for _, c := range []struct {
		depth int
		want  string
	}{{DepthTrue, "\x1b[38;2;88;166;255mx\x1b[0m"}, {Depth256, "\x1b[38;5;75mx\x1b[0m"}, {Depth16, "\x1b[94mx\x1b[0m"}} {
		s := &Styler{On: true, Depth: c.depth, Pal: pal}
		if got := s.P("status-review", "x"); got != c.want {
			t.Errorf("depth %d: %q want %q", c.depth, got, c.want)
		}
	}
	// every role has a colour at every depth, and every escape is closed
	for _, d := range []int{DepthTrue, Depth256, Depth16} {
		for _, theme := range []string{"dark", "light"} {
			s := &Styler{On: true, Depth: d, Pal: NewPalette(theme, nil)}
			for _, r := range Roles {
				if got := s.P(r, "x"); !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "x\x1b[0m") || got == "x" {
					t.Errorf("%s/%d/%s: %q", theme, d, r, got)
				}
			}
		}
	}
	// a muted green must not collapse into grey at 16 colours
	if i, _ := quantize(RGB{63, 185, 80}, Depth16); i != 2 {
		t.Errorf("green at 16 colours is index %d", i)
	}
	if i, _ := quantize(RGB{110, 118, 129}, Depth16); i != 8 {
		t.Errorf("grey at 16 colours is index %d", i)
	}
	if i, _ := quantize(RGB{255, 0, 0}, Depth256); i != 196 {
		t.Errorf("red at 256 colours is index %d", i)
	}
	// a named ANSI colour is the terminal's own at every depth
	named, _ := ParseColor("red")
	for _, d := range []int{DepthTrue, Depth256, Depth16} {
		s := &Styler{On: true, Depth: d, Pal: Palette{"x": named}}
		if got := s.P("x", "t"); got != "\x1b[31mt\x1b[0m" {
			t.Errorf("named at depth %d: %q", d, got)
		}
	}
	// off means untouched
	if got := (&Styler{Pal: pal}).P("status-review", "x"); got != "x" {
		t.Errorf("off: %q", got)
	}
	if got := (*Styler)(nil).P("status-review", "x"); got != "x" {
		t.Errorf("nil: %q", got)
	}
}

func TestChipRendering(t *testing.T) {
	pal := NewPalette("dark", nil)
	s := &Styler{On: true, Depth: DepthTrue, Pal: pal}
	// a known colour: black text on pale yellow, white on dark blue
	if got := s.Chip("bug", pal.LabelColor("kind:bug", "fbca04")); got != "\x1b[38;2;0;0;0;48;2;251;202;4mbug\x1b[0m" {
		t.Errorf("known light colour: %q", got)
	}
	if got := s.Chip("bug", pal.LabelColor("kind:bug", "#0052cc")); got != "\x1b[38;2;255;255;255;48;2;0;82;204mbug\x1b[0m" {
		t.Errorf("known dark colour: %q", got)
	}
	// an unknown colour comes from the prefix, the same every time
	for label, role := range map[string]string{"kind:bug": "label-kind", "kind::feature": "label-kind", "area:ui": "label-area", "area::auth": "label-area",
		"triage:ready": "label-triage", "wontfix": "label-other"} {
		if got := pal.LabelColor(label, ""); got != pal[role] {
			t.Errorf("%s: %v want %s %v", label, got, role, pal[role])
		}
		if pal.LabelColor(label, "") != pal.LabelColor(label, "") {
			t.Errorf("%s: not stable", label)
		}
	}
	// a malformed colour falls back to the prefix colour
	if got := pal.LabelColor("area:ui", "zzzzzz"); got != pal["label-area"] {
		t.Errorf("bad hex: %v", got)
	}
	// 16 colours: text contrast is judged on the colour the terminal shows, not the requested one
	s16 := &Styler{On: true, Depth: Depth16, Pal: pal}
	if got := s16.Chip("x", pal.LabelColor("a", "fbca04")); !strings.HasPrefix(got, "\x1b[30;") {
		t.Errorf("16-colour yellow chip should have black text: %q", got)
	}
	if got := (&Styler{Pal: pal}).Chip("bug", pal["label-kind"]); got != "bug" {
		t.Errorf("off: %q", got)
	}
}

func TestParseColorAndConfig(t *testing.T) {
	for in, want := range map[string]RGB{"#fff": {255, 255, 255}, "#2DA44E": {45, 164, 78}, "#000000": {0, 0, 0}} {
		if c, err := ParseColor(in); err != nil || c.RGB != want || c.Named != -1 {
			t.Errorf("%s: %+v %v", in, c, err)
		}
	}
	for _, bad := range []string{"", "#12", "#gggggg", "banana", "#1234567"} {
		if _, err := ParseColor(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	if c, err := ParseColor("bright-green"); err != nil || c.Named != 10 {
		t.Errorf("bright-green: %+v %v", c, err)
	}
	if c, err := ParseColor("gray"); err != nil || c.Named != 8 {
		t.Errorf("gray: %+v %v", c, err)
	}
}

func writeCfg(e *env, extra string) {
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n[repos.lib]\ngithub = \"example/lib\"\n"+extra), 0o644)
}

func TestColorsConfig(t *testing.T) {
	e := newEnv(t)
	writeCfg(e, "[colors]\ntheme = \"light\"\nstatus-working = \"#2da44e\"\nstate-open = \"red\"\nbar-fill = \"#123456\"\n")
	cfg, err := LoadConfig(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Colors.Theme != "light" || cfg.Colors.Overrides["status-working"].RGB != (RGB{45, 164, 78}) || cfg.Colors.Overrides["state-open"].Named != 1 {
		t.Fatalf("%+v", cfg.Colors)
	}
	for _, r := range []string{"bar-low", "bar-mid", "bar-high"} {
		if cfg.Colors.Overrides[r].RGB != (RGB{0x12, 0x34, 0x56}) {
			t.Errorf("bar-fill should set %s", r)
		}
	}
	pal := NewPalette(DetectTheme(envOf(), cfg.Colors.Theme), cfg.Colors.Overrides)
	if pal["status-review"] != lightPalette["status-review"] || pal["status-working"].RGB != (RGB{45, 164, 78}) {
		t.Errorf("light theme with an override: %+v", pal["status-working"])
	}
	// the override reaches the table
	humanFixture(e)
	writeCfg(e, "[colors]\nstatus-review = \"#2da44e\"\n")
	e.tty, e.width, e.extra["COLORTERM"] = true, 100, "truecolor"
	out, _, _ := e.run("ls", "--repo", "app")
	if !strings.Contains(out, "\x1b[38;2;45;164;78min review: PR #31\x1b[0m") {
		t.Errorf("override not applied: %q", out)
	}
	// theme: MOON_THEME beats the config, COLORFGBG is read, dark is the default
	for _, c := range []struct {
		env  []string
		conf string
		want string
	}{{nil, "", "dark"}, {nil, "light", "light"}, {[]string{"MOON_THEME", "dark"}, "light", "dark"}, {[]string{"MOON_THEME", "light"}, "", "light"},
		{[]string{"COLORFGBG", "0;15"}, "", "light"}, {[]string{"COLORFGBG", "15;0"}, "", "dark"}, {[]string{"COLORFGBG", "0;7"}, "dark", "dark"},
		{[]string{"MOON_THEME", "neon"}, "", "dark"}} {
		if got := DetectTheme(envOf(c.env...), c.conf); got != c.want {
			t.Errorf("%v conf %q: %s want %s", c.env, c.conf, got, c.want)
		}
	}
}

func TestColorsConfigErrors(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"[colors]\nstat-working = \"#2da44e\"\n", `moon.toml:`},
		{"[colors]\nstat-working = \"#2da44e\"\n", `unknown key "stat-working" (did you mean status-working?)`},
		{"[colors]\nstatus-working = \"banana\"\n", `[colors] status-working = "banana": not a #rrggbb colour or an ANSI colour name`},
		{"[colors]\nstatus-working = #2da44e\n", `value of status-working must be a quoted string`},
		{"[colors]\ntheme = \"neon\"\n", `[colors] theme must be "light" or "dark"`},
		{"[colors]\nmuted = \"#12\"\n", `[colors] muted = "#12"`},
	} {
		e := newEnv(t)
		writeCfg(e, c.body)
		_, err := LoadConfig(e.cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v (want %q)", c.body, err, c.want)
			continue
		}
		if !strings.Contains(err.Error(), ":6:") && !strings.Contains(err.Error(), ":7:") {
			t.Errorf("error should carry the line: %v", err)
		}
	}
	e := newEnv(t)
	writeCfg(e, "[colors]\nstatus-working = \"#2da44e\"\n[repos.zed]\ngithub = \"o/z\"\n") // a repo table after colors still works
	if cfg, err := LoadConfig(e.cfg); err != nil || len(cfg.Repos) != 3 {
		t.Errorf("repos after colors: %v %v", cfg, err)
	}
}

const esc = "\x1b"

func TestNoEscapesWhenPiped(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	e.putFile("app", fileLabels, "kind:bug\td73a4a\tbroken\narea:ui\t1d76db\tui\n")
	for _, force := range [][]string{nil, {"--color", "always"}} {
		for _, cmd := range [][]string{{"ls", "-S", "all"}, {"ls", "--show-status"}, {"frontier"}, {"claims"}, {"claims", "--show-status"}, {"sweep"},
			{"show", "app#21", "--show-status"}, {"milestones"}, {"prs", "-S", "all"}, {"status"}} {
			for _, format := range []string{"tsv", "json"} {
				if cmd[0] == "show" && format == "json" {
					continue
				}
				args := append(append(append([]string{}, cmd...), "--format", format), force...)
				out, errs, _ := e.run(args...)
				if strings.Contains(out, esc) || strings.Contains(errs, esc) {
					t.Errorf("%v: escape codes in %s output: %q", args, format, out)
				}
			}
		}
	}
	// CLICOLOR_FORCE alone does not turn a piped list into a table
	e.extra["CLICOLOR_FORCE"] = "1"
	if out, _, _ := e.run("ls"); strings.Contains(out, esc) || !strings.Contains(out, "app#28\topen") {
		t.Errorf("CLICOLOR_FORCE piped: %q", out)
	}
	delete(e.extra, "CLICOLOR_FORCE")
	// --color always on its own is a coloured table, for less -R
	out, _, _ := e.run("ls", "--color", "always")
	if !strings.Contains(out, esc+"[1mREF") || !strings.Contains(out, esc+"[") {
		t.Errorf("--color always should print a coloured table: %q", out)
	}
}

func TestNoColorRules(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	e.tty, e.width = true, 100
	has := func(args ...string) bool { o, _, _ := e.run(args...); return strings.Contains(o, esc) }
	if !has("ls") {
		t.Fatal("tty ls should be coloured")
	}
	if has("ls", "-n") || has("ls", "--no-color") || has("ls", "--color", "never") {
		t.Error("flags did not turn colour off")
	}
	e.extra["NO_COLOR"] = "1"
	if has("ls") {
		t.Error("NO_COLOR ignored")
	}
	if !has("ls", "--color", "always") {
		t.Error("--color always should override NO_COLOR")
	}
	delete(e.extra, "NO_COLOR")
	e.extra["TERM"] = "dumb"
	if has("ls") {
		t.Error("TERM=dumb ignored")
	}
}

func TestLabelChipsInTable(t *testing.T) {
	e := newEnv(t)
	humanFixture(e)
	e.putFile("app", fileLabels, "kind:bug\td73a4a\tSomething is broken\narea:ui\t\t\n") // ui has no colour
	e.tty, e.width, e.extra["COLORTERM"] = true, 130, "truecolor"
	out, _, _ := e.run("ls", "--repo", "app")
	bugFg := TextOn(RGB{0xd7, 0x3a, 0x4a})
	want := fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;215;58;74mkind:bug\x1b[0m", bugFg.R, bugFg.G, bugFg.B)
	if !strings.Contains(out, want) {
		t.Errorf("known colour chip missing %q in:\n%q", want, out)
	}
	area := NewPalette("dark", nil)["label-area"].RGB
	areaFg := TextOn(area)
	want = fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dmarea:ui\x1b[0m", areaFg.R, areaFg.G, areaFg.B, area.R, area.G, area.B)
	if !strings.Contains(out, want) {
		t.Errorf("prefix-colour chip missing %q in:\n%q", want, out)
	}
}

func TestPullWritesLabelColors(t *testing.T) {
	e := newEnv(t)
	issue := map[string]any{"number": 5, "title": "t", "body": "", "state": "OPEN", "stateReason": nil, "url": "u", "createdAt": "2026-02-01T00:00:00Z",
		"updatedAt": "2026-02-02T00:00:00Z", "id": "I5", "author": map[string]any{"login": "al"}, "milestone": nil,
		"assignees": map[string]any{"nodes": []any{}}, "labels": map[string]any{"nodes": []any{map[string]any{"name": "bug"}}},
		"comments": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}}
	e.useMem().cannedPull(t, "example/app", map[string]any{
		"labels": map[string]any{"nodes": []any{
			map[string]any{"name": "kind:bug", "color": "d73a4a", "description": "Something\tis broken"},
			map[string]any{"name": "wontfix", "color": "ffffff", "description": nil}}},
		"milestones":   map[string]any{"nodes": []any{}},
		"issues":       map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{issue}},
		"pullRequests": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}})
	e.now = time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	if _, errs, code := e.run("pull", "--repo", "app"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "labels.tsv", e.getFile("app", fileLabels), "kind:bug\td73a4a\tSomething is broken\nwontfix\tffffff\t\n")
	a := e.app()
	if got := a.labelColors("app")["kind:bug"]; got != "d73a4a" {
		t.Errorf("labelColors: %q", got)
	}
	// the existing files are untouched by the new one: the index still has its six columns
	eq(t, "index", e.getFile("app", fileIndex), "5\topen\t\tbug\t2026-02-02T00:00:00Z\tt\n")
	// and a mirror that never fetched labels draws prefix colours
	if got := a.labelColors("lib"); len(got) != 0 {
		t.Errorf("lib: %v", got)
	}
}

func TestMilestoneBarColorByPercent(t *testing.T) {
	for pct, want := range map[int]string{0: "bar-low", 24: "bar-low", 25: "bar-mid", 74: "bar-mid", 75: "bar-high", 100: "bar-high"} {
		if got := BarRole(pct); got != want {
			t.Errorf("%d%%: %s want %s", pct, got, want)
		}
	}
}
