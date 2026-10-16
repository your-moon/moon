package issues

import (
	"regexp"
	"strings"
	"testing"
)

var longFlagRe = regexp.MustCompile(`--[a-z][a-z-]*`)

func flagSet(ci CmdInfo) map[string]bool {
	m := map[string]bool{}
	for _, f := range ci.Flags {
		if f.Name != "" {
			m["--"+f.Name] = true
		}
		for _, a := range f.Aliases {
			m["--"+a] = true
		}
	}
	return m
}

// The parser is built from the table, so these checks compare the table with the two things that are
// written by hand: the usage lines and the usage text. A flag that the parser accepts but the usage
// text never mentions, or a usage line that names a flag the parser rejects, fails here.
func TestTableMatchesUsageText(t *testing.T) {
	all := map[string]bool{"--help": true, "--config": true, "--dir": true}
	for _, ci := range Describe() {
		for f := range flagSet(ci) {
			all[f] = true
		}
		set := flagSet(ci)
		for _, f := range longFlagRe.FindAllString(ci.Usage, -1) {
			if !set[f] {
				t.Errorf("usage line of %q names %s, which the table does not have", ci.Name, f)
			}
		}
	}
	for _, f := range longFlagRe.FindAllString(usageText, -1) {
		if !all[f] {
			t.Errorf("usageText names %s, which no command has", f)
		}
	}
	// the other direction: every flag the parser accepts is named somewhere in the usage text
	docs := usageText
	for _, ci := range Describe() {
		docs += "\n" + ci.Usage
	}
	for f := range all {
		if f == "--fresh" { // documented only in each command's --help options, by design
			continue
		}
		if !strings.Contains(docs, f) {
			t.Errorf("table has %s but no usage text mentions it", f)
		}
	}
}

func TestTableDrivesParser(t *testing.T) {
	cmds := commands()
	for _, ci := range Describe() {
		sp := cmds[ci.Name]
		if sp == nil {
			t.Fatalf("Describe lists %q but the dispatcher has no such command", ci.Name)
		}
		if len(ci.Pos) != sp.npos {
			t.Errorf("%s: %d positional kinds, parser wants %d", ci.Name, len(ci.Pos), sp.npos)
		}
		if _, ok := helpTexts[ci.Name]; !ok {
			t.Errorf("%s has no help entry", ci.Name)
		}
		pos := make([]string, sp.npos)
		for i := range pos {
			pos[i] = "x"
		}
		try := func(arg ...string) error {
			// required flags are supplied so that only the flag under test can fail
			full := append([]string{}, arg...)
			for _, f := range sp.fls {
				if f.req && !contains(full, "--"+f.name) {
					v := "v"
					if f.name == "repo" {
						v = "app"
					}
					full = append(full, "--"+f.name, v)
				}
			}
			_, err := parseOpts(sp, ci.Name, append(full, pos...), []string{"app"})
			return err
		}
		for _, f := range ci.Flags {
			if f.Name == "help" || (f.Name == "" && (f.Short == "j" || f.Short == "t")) {
				continue
			}
			val := "app"
			if len(f.Choices) > 0 {
				val = f.Choices[0]
			} else if f.Kind == "number" {
				val = "5"
			} else if f.Kind != "repo" {
				val = "v"
			}
			args := []string{"--" + f.Name}
			if f.Takes {
				args = append(args, val)
			}
			if err := try(args...); err != nil {
				t.Errorf("%s: parser rejects table flag --%s: %v", ci.Name, f.Name, err)
			}
			if f.Short != "" {
				args = []string{"-" + f.Short}
				if f.Takes {
					args = append(args, val)
				}
				if err := try(args...); err != nil {
					t.Errorf("%s: parser rejects table flag -%s: %v", ci.Name, f.Short, err)
				}
			}
		}
		// a short flag that is not in the table must be rejected
		have := map[string]bool{}
		for _, f := range ci.Flags {
			have[f.Short] = true
		}
		for _, c := range "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			if have[string(c)] {
				continue
			}
			if err := try("-" + string(c)); err == nil {
				t.Errorf("%s: parser accepts -%c but the table does not list it", ci.Name, c)
			}
		}
		if err := try("--no-such-flag"); err == nil {
			t.Errorf("%s: parser accepts an unknown long flag", ci.Name)
		}
		for _, f := range ci.Flags {
			if f.Name != "" && f.Name != "help" && !strings.Contains(cmdHelp(ci.Name), "--"+f.Name) {
				t.Errorf("%s: help omits --%s", ci.Name, f.Name)
			}
		}
	}
	for n := range cmds {
		found := false
		for _, ci := range Describe() {
			found = found || ci.Name == n
		}
		if !found {
			t.Errorf("command %q is missing from Describe", n)
		}
	}
}
