// Command moon is a thin dispatcher over the tools in internal/.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/your-moon/moon/internal/completion"
	"github.com/your-moon/moon/internal/issues"
	"github.com/your-moon/moon/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// topCommands is the one list of top-level commands: the usage text, "did you mean" and shell completion read it.
var topCommands = []completion.Command{
	{Name: "tui", Summary: "interactive terminal dashboard: issues, claims (press ? inside for keys)"},
	{Name: "issues", Summary: "local-first mirror of GitHub issues and pull requests (moon help issues)"},
	{Name: "version", Summary: "print the version"},
	{Name: "completion", Summary: "print the shell completion script: zsh, bash or fish"},
	{Name: "help", Summary: "print this text, or: moon help <tool>"},
}

var usage = func() string {
	var b strings.Builder
	b.WriteString("usage: moon <command> [args]\n\ncommands:\n")
	for _, c := range topCommands {
		pad := strings.Repeat(" ", max(1, 10-len(c.Name))) // column 10, wider only for a longer name
		fmt.Fprintf(&b, "  %s%s%s\n", c.Name, pad, c.Summary)
	}
	return b.String()
}()

func completeOptions(e issues.Env) completion.Options {
	return completion.Options{Top: topCommands, Open: func(cfg, dir string) completion.Source {
		return completion.OpenSession(cfg, dir, issues.Env{Getenv: e.Getenv, Cwd: e.Cwd})
	}}
}

func main() {
	cwd, _ := os.Getwd()
	e := issues.Env{Getenv: os.Getenv, Stdin: os.Stdin, Out: os.Stdout, Err: os.Stderr, Cwd: cwd}
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) {
		e.TTY = true
		e.Width, _, _ = term.GetSize(fd) // 0 on failure: the table code falls back to 100
	}
	os.Exit(run(os.Args[1:], e))
}

func run(args []string, e issues.Env) int {
	if len(args) == 0 {
		fmt.Fprint(e.Out, usage)
		return 0
	}
	switch args[0] {
	case "__complete": // hidden: called by the shell scripts, never prints an error
		return completion.RunComplete(completeOptions(e), args[1:], e.Out)
	case "completion":
		if len(args) != 2 {
			fmt.Fprintln(e.Err, "usage: moon completion zsh|bash|fish")
			return 2
		}
		sc, err := completion.Script(args[1], "moon")
		if err != nil {
			fmt.Fprintln(e.Err, "moon completion:", err)
			return 2
		}
		io.WriteString(e.Out, sc)
		return 0
	case "tui":
		return tui.Run(args[1:], e)
	case "issues":
		if rest, ok := issuesTUIArgs(args[1:]); ok {
			return tui.Run(rest, e)
		}
		return issues.Main(args[1:], e)
	case "version", "--version":
		fmt.Fprintln(e.Out, "moon", version)
		return 0
	case "help", "-h", "--help":
		if len(args) > 1 && args[0] == "help" {
			if args[1] == "issues" {
				return issues.Main(append([]string{"help"}, args[2:]...), e)
			}
			fmt.Fprintf(e.Err, "moon: no tool named %q%s\n", args[1], didYouMean(args[1]))
			return 2
		}
		fmt.Fprint(e.Out, usage)
		return 0
	}
	fmt.Fprintf(e.Err, "moon: unknown command %q%s\n\n", args[0], didYouMean(args[0]))
	io.WriteString(e.Err, usage)
	return 2
}

// issuesTUIArgs recognizes `moon issues [--config F] [--dir D] tui [flags]` and returns every flag.
func issuesTUIArgs(args []string) ([]string, bool) {
	var flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "tui":
			return append(flags, args[i+1:]...), true
		case a == "--config" || a == "--dir":
			if i+1 >= len(args) {
				return nil, false
			}
			flags = append(flags, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "--dir="):
			flags = append(flags, a)
		default:
			return nil, false
		}
	}
	return nil, false
}

func didYouMean(word string) string {
	if c := issues.Closest(word, []string{"issues", "tui", "version", "completion", "help"}); c != "" {
		return fmt.Sprintf(" (did you mean %s?)", c)
	}
	return ""
}
