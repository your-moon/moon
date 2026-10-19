package completion

import (
	"fmt"
	"io"
	"strings"
)

// RunComplete implements the hidden `moon __complete`. Plain form: `__complete <words...>`, the last word is
// the one under the cursor. The bash form passes the raw line instead, because bash splits words at : and =:
//
//	__complete --shell bash --cur <bash's current word> --line <command line up to the cursor>
//
// Output: one candidate per line, "value" or "value<TAB>description", then a last line ":<directive>".
// It never prints an error, whatever happens.
func RunComplete(o Options, args []string, out io.Writer) int {
	res := Result{Dir: DirNoFiles}
	func() {
		defer func() { _ = recover() }()
		if len(args) > 0 && strings.HasPrefix(args[0], "--") && (args[0] == "--line" || args[0] == "--shell" || args[0] == "--cur") {
			var shell, cur, line string
			for i := 0; i+1 < len(args); i += 2 {
				switch args[i] {
				case "--shell":
					shell = args[i+1]
				case "--cur":
					cur = args[i+1]
				case "--line":
					line = args[i+1]
				}
			}
			words, raw, quote := splitLine(line)
			if len(words) > 0 {
				words = words[1:] // the program name
			}
			res = Complete(o, words)
			if shell == "bash" {
				res = bashEscape(res, raw, cur, quote)
			}
			return
		}
		res = Complete(o, args)
	}()
	var b strings.Builder
	for _, c := range res.Cands {
		b.WriteString(oneLineTab(c.Value))
		if c.Desc != "" {
			b.WriteString("\t" + shorten(oneLineTab(c.Desc), 72))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, ":%d\n", int(res.Dir))
	io.WriteString(out, b.String())
	return 0
}

func oneLineTab(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

// splitLine splits a command line like a shell does (SplitWords). It returns the unquoted words, the raw
// text of the last word, and the quote character left open in it (0 if none). A line that ends in a space
// gets an empty last word.
func splitLine(line string) (words []string, raw string, quote byte) {
	ws, quote := SplitWords(line)
	for _, w := range ws {
		words = append(words, w.Text)
	}
	if last := ws[len(ws)-1]; last.Text != "" || quote != 0 {
		return words, line[last.Start:], quote
	}
	return words, "", 0
}

// bashEscape rewrites candidates the way bash needs them: escaped for the quote state of the word being
// typed, and cut so that they replace only the part of the word bash considers current (bash splits at : and =).
func bashEscape(r Result, raw, cur string, quote byte) Result {
	strip := len(raw) - len(cur)
	var out []Candidate
	for _, c := range r.Cands {
		e := escapeFor(c.Value, quote)
		if quote != 0 {
			e = string(quote) + e
		}
		if strip > 0 && len(e) >= strip && strings.HasPrefix(raw, e[:strip]) {
			e = e[strip:]
		}
		out = append(out, Candidate{Value: e})
	}
	r.Cands = out
	return r
}

func escapeFor(s string, quote byte) string {
	switch quote {
	case '\'':
		return strings.ReplaceAll(s, "'", `'\''`)
	case '"':
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(" \t\n\"'\\$`&;|<>()*?[]{}!", s[i]) >= 0 || (i == 0 && (s[i] == '~' || s[i] == '#')) {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// shorten cuts a description to n runes so one candidate stays on one line of the shell's list.
func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
