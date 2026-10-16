package issues

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Colour, in one place. Everything that prints colour asks three things of this file: is colour allowed
// (DetectColor), what colour is a role in the current theme (Palette), and what bytes draw it at the depth
// the terminal supports (Styler). Piped, TSV and JSON output never go through here.

// ---------- capability ----------

// Depth is how many colours the terminal can show.
const (
	Depth16   = 16
	Depth256  = 256
	DepthTrue = 24
)

// ColorCaps is the result of capability detection.
type ColorCaps struct {
	On     bool
	Depth  int  // Depth16, Depth256 or DepthTrue; meaningful when On
	Forced bool // --color always: colour even when stdout is not a terminal
}

// DetectColor decides whether colour is allowed and how deep it goes. mode is "auto", "always" or "never"
// (the --color flag); noColor is --no-color / -n. Precedence, strongest first: noColor, mode never, mode always,
// NO_COLOR, CLICOLOR_FORCE, not a terminal, TERM=dumb.
func DetectColor(getenv func(string) string, tty bool, noColor bool, mode string) ColorCaps {
	c := ColorCaps{Depth: colorDepth(getenv)}
	switch {
	case noColor || mode == "never":
	case mode == "always":
		c.On, c.Forced = true, true
	case getenv("NO_COLOR") != "":
	case getenv("CLICOLOR_FORCE") != "" && getenv("CLICOLOR_FORCE") != "0":
		c.On = true
	case !tty:
	case getenv("TERM") == "dumb":
	default:
		c.On = true
	}
	return c
}

func colorDepth(getenv func(string) string) int {
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return DepthTrue
	}
	if strings.Contains(getenv("TERM"), "256color") {
		return Depth256
	}
	return Depth16
}

// ---------- colours ----------

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

// Color is an RGB value, or one of the 16 named ANSI colours (Named >= 0), which the terminal themes itself.
type Color struct {
	RGB   RGB
	Named int // -1 = not named
}

func rgbColor(r, g, b uint8) Color { return Color{RGB: RGB{r, g, b}, Named: -1} }

var basic16 = [16]RGB{{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}}

var ansiNames = []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// ParseColor reads "#rrggbb", "#rgb" or an ANSI colour name (red, bright-red, gray).
func ParseColor(s string) (Color, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return Color{}, fmt.Errorf("not a #rrggbb colour")
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return Color{}, fmt.Errorf("not a #rrggbb colour")
		}
		return rgbColor(uint8(v>>16), uint8(v>>8), uint8(v)), nil
	}
	if s == "gray" || s == "grey" {
		s = "bright-black"
	}
	bright := strings.HasPrefix(s, "bright-")
	name := strings.TrimPrefix(s, "bright-")
	for i, n := range ansiNames {
		if n == name {
			if bright {
				i += 8
			}
			return Color{RGB: basic16[i], Named: i}, nil
		}
	}
	return Color{}, fmt.Errorf("not a #rrggbb colour or an ANSI colour name (black, red, green, yellow, blue, magenta, cyan, white, bright-<name>, gray)")
}

func dist(a, b RGB) int {
	dr, dg, db := int(a.R)-int(b.R), int(a.G)-int(b.G), int(a.B)-int(b.B)
	return dr*dr + dg*dg + db*db
}

var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// quantize returns the terminal colour index nearest to c at a depth (16 or 256) and the RGB that index shows.
func quantize(c RGB, depth int) (int, RGB) {
	if depth == Depth16 {
		i := basic16Index(c)
		return i, basic16[i]
	}
	nearest := func(v uint8) int {
		best, bd := 0, 1<<30
		for i, l := range cubeLevels {
			if d := (int(v) - int(l)) * (int(v) - int(l)); d < bd {
				best, bd = i, d
			}
		}
		return best
	}
	r, g, b := nearest(c.R), nearest(c.G), nearest(c.B)
	cube := RGB{cubeLevels[r], cubeLevels[g], cubeLevels[b]}
	idx, shown := 16+36*r+6*g+b, cube
	avg := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi := (avg - 8 + 5) / 10
	gi = max(0, min(23, gi))
	gv := uint8(8 + 10*gi)
	if gray := (RGB{gv, gv, gv}); dist(c, gray) < dist(c, cube) {
		idx, shown = 232+gi, gray
	}
	return idx, shown
}

// sgr is the SGR parameter string that sets the foreground (bg false) or background of c.
func (c Color) sgr(depth int, bg bool) string {
	base := 38
	if bg {
		base = 48
	}
	if c.Named >= 0 {
		n := c.Named
		code := 30 + n
		if n >= 8 {
			code = 90 + n - 8
		}
		if bg {
			code += 10
		}
		return strconv.Itoa(code)
	}
	switch depth {
	case DepthTrue:
		return fmt.Sprintf("%d;2;%d;%d;%d", base, c.RGB.R, c.RGB.G, c.RGB.B)
	case Depth256:
		i, _ := quantize(c.RGB, Depth256)
		return fmt.Sprintf("%d;5;%d", base, i)
	}
	i, _ := quantize(c.RGB, Depth16)
	code := 30 + i
	if i >= 8 {
		code = 90 + i - 8
	}
	if bg {
		code += 10
	}
	return strconv.Itoa(code)
}

// shown is the RGB the terminal will actually display for c at a depth.
func (c Color) shown(depth int) RGB {
	if c.Named >= 0 || depth == DepthTrue {
		return c.RGB
	}
	_, s := quantize(c.RGB, depth)
	return s
}

// ---------- contrast ----------

func lin(v uint8) float64 {
	f := float64(v) / 255
	if f <= 0.03928 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

// Luminance is the WCAG relative luminance of c.
func Luminance(c RGB) float64 { return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B) }

// Contrast is the WCAG contrast ratio of two colours (1 to 21).
func Contrast(a, b RGB) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TextOn is black or white, whichever reads better on bg.
func TextOn(bg RGB) RGB {
	if Contrast(bg, RGB{0, 0, 0}) >= Contrast(bg, RGB{255, 255, 255}) {
		return RGB{0, 0, 0}
	}
	return RGB{255, 255, 255}
}

// Hex is "#rrggbb".
func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ---------- palette ----------

// Roles are the names colours are asked for by. Add a role here and in both palettes.
var Roles = []string{"state-open", "state-closed", "state-merged",
	"status-working", "status-review", "status-stale", "status-blocked", "status-ready", "status-done",
	"label-kind", "label-area", "label-triage", "label-other",
	"bar-low", "bar-mid", "bar-high", "bar-empty",
	"claim-fresh", "claim-stale", "muted", "accent", "warning", "error"}

// RoleAliases set several roles at once in the config: bar-fill sets bar-low, bar-mid and bar-high.
var RoleAliases = map[string][]string{"bar-fill": {"bar-low", "bar-mid", "bar-high"}}

func hexc(h string) Color { c, _ := ParseColor(h); return c }

var darkPalette = map[string]Color{
	"state-open": hexc("#3fb950"), "state-closed": hexc("#f85149"), "state-merged": hexc("#a371f7"),
	"status-working": hexc("#2ea043"), "status-review": hexc("#58a6ff"), "status-stale": hexc("#d29922"),
	"status-blocked": hexc("#f85149"), "status-ready": hexc("#39c5cf"), "status-done": hexc("#6e7681"),
	"label-kind": hexc("#1f6feb"), "label-area": hexc("#8957e5"), "label-triage": hexc("#bf8700"), "label-other": hexc("#6e7681"),
	"bar-low": hexc("#f85149"), "bar-mid": hexc("#d29922"), "bar-high": hexc("#3fb950"), "bar-empty": hexc("#6e7681"),
	"claim-fresh": hexc("#2ea043"), "claim-stale": hexc("#6e7681"), "muted": hexc("#7d8590"), "accent": hexc("#58a6ff"),
	"warning": hexc("#d29922"), "error": hexc("#f85149"),
}

var lightPalette = map[string]Color{
	"state-open": hexc("#1a7f37"), "state-closed": hexc("#cf222e"), "state-merged": hexc("#8250df"),
	"status-working": hexc("#1a7f37"), "status-review": hexc("#0969da"), "status-stale": hexc("#9a6700"),
	"status-blocked": hexc("#cf222e"), "status-ready": hexc("#11818b"), "status-done": hexc("#6e7781"),
	"label-kind": hexc("#0969da"), "label-area": hexc("#8250df"), "label-triage": hexc("#9a6700"), "label-other": hexc("#8c959f"),
	"bar-low": hexc("#cf222e"), "bar-mid": hexc("#9a6700"), "bar-high": hexc("#1a7f37"), "bar-empty": hexc("#afb8c1"),
	"claim-fresh": hexc("#1a7f37"), "claim-stale": hexc("#6e7781"), "muted": hexc("#656d76"), "accent": hexc("#0969da"),
	"warning": hexc("#9a6700"), "error": hexc("#cf222e"),
}

// Palette maps every role to a colour.
type Palette map[string]Color

// NewPalette is the theme's palette ("light" or anything else = dark) with the overrides applied.
func NewPalette(theme string, overrides map[string]Color) Palette {
	src := darkPalette
	if theme == "light" {
		src = lightPalette
	}
	p := Palette{}
	for k, v := range src {
		p[k] = v
	}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

// DetectTheme picks "light" or "dark": MOON_THEME, then the config's theme, then COLORFGBG (background 7 and
// 15 are light), then dark.
func DetectTheme(getenv func(string) string, configured string) string {
	if t := strings.ToLower(getenv("MOON_THEME")); t == "light" || t == "dark" {
		return t
	}
	if configured == "light" || configured == "dark" {
		return configured
	}
	if v := getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		switch parts[len(parts)-1] {
		case "7", "15":
			return "light"
		}
	}
	return "dark"
}

// ColorConfig is the [colors] table of moon.toml.
type ColorConfig struct {
	Theme     string
	Overrides map[string]Color
}

// IsRole reports whether name can be set in [colors].
func IsRole(name string) bool {
	if _, ok := RoleAliases[name]; ok {
		return true
	}
	for _, r := range Roles {
		if r == name {
			return true
		}
	}
	return false
}

// Set records one [colors] entry; a bad key or value is an error naming the key.
func (c *ColorConfig) Set(key, val string) error {
	if key == "theme" {
		if val != "light" && val != "dark" {
			return fmt.Errorf("[colors] theme must be \"light\" or \"dark\", got %q", val)
		}
		c.Theme = val
		return nil
	}
	if !IsRole(key) {
		known := append(append([]string{"theme"}, Roles...), "bar-fill")
		sort.Strings(known)
		msg := fmt.Sprintf("[colors] unknown key %q", key)
		if cl := closest(key, known); cl != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", cl)
		}
		return fmt.Errorf("%s; known keys: %s", msg, strings.Join(known, ", "))
	}
	col, err := ParseColor(val)
	if err != nil {
		return fmt.Errorf("[colors] %s = %q: %v", key, val, err)
	}
	if c.Overrides == nil {
		c.Overrides = map[string]Color{}
	}
	if al, ok := RoleAliases[key]; ok {
		for _, r := range al {
			c.Overrides[r] = col
		}
		return nil
	}
	c.Overrides[key] = col
	return nil
}

// ---------- drawing ----------

// Styler turns roles into escape sequences. A nil Styler, or one that is not On, returns text unchanged.
type Styler struct {
	On    bool
	Depth int
	Pal   Palette
}

// NewStyler builds a Styler from detected capability, the environment and the config.
func NewStyler(caps ColorCaps, getenv func(string) string, cfg ColorConfig) *Styler {
	return &Styler{On: caps.On, Depth: caps.Depth, Pal: NewPalette(DetectTheme(getenv, cfg.Theme), cfg.Overrides)}
}

const reset = "\x1b[0m"

// P paints text in a role: "bold", or one of Roles.
func (s *Styler) P(role, text string) string {
	if s == nil || !s.On || role == "" || text == "" {
		return text
	}
	if role == "bold" {
		return "\x1b[1m" + text + reset
	}
	c, ok := s.Pal[role]
	if !ok {
		return text
	}
	return "\x1b[" + c.sgr(s.Depth, false) + "m" + text + reset
}

// Chip draws text on a coloured background with black or white text, whichever contrasts better with the
// background as this terminal will show it.
func (s *Styler) Chip(text string, bg Color) string {
	if s == nil || !s.On || text == "" {
		return text
	}
	fg := rgbColor(0, 0, 0)
	if TextOn(bg.shown(s.Depth)) == (RGB{255, 255, 255}) {
		fg = rgbColor(255, 255, 255)
	}
	return "\x1b[" + fg.sgr(s.Depth, false) + ";" + bg.sgr(s.Depth, true) + "m" + text + reset
}

// ChipColors are the background and text colours (as #rrggbb) of a label chip, for renderers with their own
// colour handling (the terminal UI).
func ChipColors(bg Color) (fgHex, bgHex string) {
	return TextOn(bg.RGB).Hex(), bg.RGB.Hex()
}

// LabelColor is the chip colour of a label: its GitHub colour when known (hex without #), otherwise a fixed
// colour for its prefix from the palette.
func (p Palette) LabelColor(label, hex string) Color {
	if hex != "" {
		if c, err := ParseColor("#" + strings.TrimPrefix(hex, "#")); err == nil {
			return c
		}
	}
	switch labelGroup(label) {
	case 0:
		return p["label-kind"]
	case 1:
		return p["label-area"]
	case 2:
		return p["label-triage"]
	}
	return p["label-other"]
}

// BarRole is the palette role of a progress bar filled to pct percent.
func BarRole(pct int) string {
	switch {
	case pct < 25:
		return "bar-low"
	case pct < 75:
		return "bar-mid"
	}
	return "bar-high"
}

// basic16Index maps a colour to one of the 16 ANSI colours by hue, not by RGB distance (which turns every
// muted green into grey): near-greys become black, grey or white by lightness; others take the nearest of
// red, yellow, green, cyan, blue, magenta, bright when the colour is light.
func basic16Index(c RGB) int {
	r, g, b := float64(c.R), float64(c.G), float64(c.B)
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if mx-mn < 40 {
		switch {
		case mx < 60:
			return 0
		case mx < 150:
			return 8
		case mx < 235:
			return 7
		}
		return 15
	}
	var h float64
	switch mx {
	case r:
		h = math.Mod((g-b)/(mx-mn), 6)
	case g:
		h = (b-r)/(mx-mn) + 2
	default:
		h = (r-g)/(mx-mn) + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	base := []int{1, 3, 2, 6, 4, 5, 1}[int(math.Floor((h+30)/60))] // red yellow green cyan blue magenta
	if mx >= 220 {
		base += 8
	}
	return base
}
