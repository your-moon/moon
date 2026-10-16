package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/your-moon/moon/internal/issues"
)

// Run starts the dashboard for `moon tui` or `moon issues tui`. args are the flags after the command:
// --config FILE, --dir MIRROR, --agent NAME. It returns the process exit status.
func Run(args []string, e issues.Env) int {
	var cfg, dir, agent, colorMode string
	noColor := false
	for i := 0; i < len(args); i++ {
		k, v, hv := strings.Cut(args[i], "=")
		switch k {
		case "--no-color", "-n":
			noColor = true
		case "--config", "--dir", "--agent", "--color":
			if !hv {
				if i+1 >= len(args) {
					fmt.Fprintf(e.Err, "%s needs a value\n", k)
					return 2
				}
				i++
				v = args[i]
			}
			switch k {
			case "--config":
				cfg = v
			case "--dir":
				dir = v
			case "--color":
				if v != "auto" && v != "always" && v != "never" {
					fmt.Fprintf(e.Err, "--color wants auto, always or never, got %q\n", v)
					return 2
				}
				colorMode = v
			default:
				agent = v
			}
		default:
			fmt.Fprintf(e.Err, "usage: moon tui [--config FILE] [--dir MIRROR] [--agent NAME] [--color auto|always|never] [--no-color]\n")
			return 2
		}
	}
	if agent == "" {
		agent = e.Getenv("USER")
	}
	if agent == "" {
		agent = "user"
	}
	caps := issues.DetectColor(e.Getenv, true, noColor, colorMode) // the UI only runs on a terminal
	switch {
	case !caps.On:
		lipgloss.SetColorProfile(termenv.Ascii) // plain text and the ASCII progress bar
	case caps.Depth == issues.DepthTrue:
		lipgloss.SetColorProfile(termenv.TrueColor)
	case caps.Depth == issues.Depth256:
		lipgloss.SetColorProfile(termenv.ANSI256)
	default:
		lipgloss.SetColorProfile(termenv.ANSI)
	}
	var model tea.Model
	if sess, err := issues.NewSession(cfg, dir, e); err != nil {
		model = ErrorShell(err.Error())
	} else {
		SetPalette(sess.Palette())
		model = NewShell([]Tab{
			NewIssuesTab(sess, Options{Agent: agent, Editor: e.Getenv("EDITOR"), MirrorDir: sess.MirrorDir()}),
			NewMilestonesTab(sess),
			NewClaimsTab(sess),
		}, 0)
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fmt.Fprintln(e.Err, "moon tui:", err)
		return 1
	}
	return 0
}
