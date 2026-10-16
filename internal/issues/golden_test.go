package issues

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestGoldenAgainstPython runs every read-only subcommand through the original Python tool and through
// this port, against the same COPY of a real mirror, and diffs stdout, stderr and exit status.
//
//	MOON_GOLDEN_MIRROR  a mirror dir to copy (never written to)
//	MOON_GOLDEN_PY      path of the Python issues.py
//	MOON_GOLDEN_REPOS   alias=owner/name[,alias=owner/name] for the config written into the temp dir
//
// Skipped when they are unset. Only values that depend on the wall clock are normalised.
func TestGoldenAgainstPython(t *testing.T) {
	src, py, repos := os.Getenv("MOON_GOLDEN_MIRROR"), os.Getenv("MOON_GOLDEN_PY"), os.Getenv("MOON_GOLDEN_REPOS")
	if src == "" || py == "" || repos == "" {
		t.Skip("MOON_GOLDEN_MIRROR, MOON_GOLDEN_PY and MOON_GOLDEN_REPOS not set")
	}
	tmp := t.TempDir()
	mirror := filepath.Join(tmp, "mirror")
	if out, err := exec.Command("cp", "-R", src, mirror).CombinedOutput(); err != nil {
		t.Fatalf("copy: %v %s", err, out)
	}
	var toml strings.Builder
	toml.WriteString("mirror_dir = \"" + mirror + "\"\n")
	var aliases []string
	for _, kv := range strings.Split(repos, ",") {
		a, g, _ := strings.Cut(kv, "=")
		aliases = append(aliases, a)
		toml.WriteString("[repos." + a + "]\ngithub = \"" + g + "\"\n")
	}
	cfg := filepath.Join(tmp, "moon.toml")
	os.WriteFile(cfg, []byte(toml.String()), 0o644)

	app := &App{Home: mirror, Environ: os.Getenv, Repos: nil}
	for _, a := range aliases {
		app.Repos = append(app.Repos, Repo{Alias: a})
	}
	cases := [][]string{
		{"ls"}, {"ls", "--state", "all"}, {"ls", "--state", "closed"}, {"ls", "-q", "a"}, {"ls", "--label", "bug"},
		{"frontier"}, {"frontier", "--label", "bug"}, {"milestones"}, {"claims"}, {"status"},
		{"prs"}, {"prs", "--state", "all"}, {"prs", "--state", "merged"}, {"prs", "--state", "closed"}, {"prs", "--state", "all", "--json"},
		{"prs", "--state", "merged", "--since", "2000-01-01T00:00:00Z", "--json"},
		{"show", aliases[0] + "#999999"}, {"show", "bad"}, {"pr", "show", aliases[0] + "#999999"},
	}
	for _, a := range aliases {
		cases = append(cases, []string{"ls", "--repo", a, "--state", "all"}, []string{"prs", "--repo", a, "--state", "all"})
		rows := app.readIndex(a)
		for i, r := range rows {
			if i%max(1, len(rows)/12) == 0 || i < 3 {
				cases = append(cases, []string{"show", a + "#" + r[0]})
			}
		}
		prs := app.readPRs(a)
		n := 0
		for num := range prs {
			if n++; n > 6 {
				break
			}
			cases = append(cases, []string{"pr", "show", a + "#" + num})
		}
	}
	// pending-op overlay and local refs, on the copy only
	if ops, _ := os.ReadDir(filepath.Join(mirror, "outbox")); len(ops) > 0 {
		t.Logf("copy holds %d queued ops; ls/show overlay them", len(ops))
	}
	vol := []*regexp.Regexp{regexp.MustCompile(`age=\d+s`), regexp.MustCompile(`claimed \d+s ago`), regexp.MustCompile(`last pull \d+s ago`)}
	norm := func(s string) string {
		for _, r := range vol {
			s = r.ReplaceAllStringFunc(s, func(m string) string { return r.String() })
		}
		return s
	}
	same := 0
	for _, c := range cases {
		cmd := exec.Command("python3", append([]string{py}, c...)...)
		cmd.Env = append(os.Environ(), "ISSUES_HOME="+mirror, "ISSUES_NO_AUTOSYNC=1", "ISSUES_GH=/usr/bin/false")
		var po, pe bytes.Buffer
		cmd.Stdout, cmd.Stderr = &po, &pe
		pcode := 0
		if err := cmd.Run(); err != nil {
			pcode = err.(*exec.ExitError).ExitCode()
		}
		var go_, ge bytes.Buffer
		env := map[string]string{"ISSUES_NO_AUTOSYNC": "1", "ISSUES_GH": "/usr/bin/false"}
		gcode := Main(append([]string{"--config", cfg}, c...), Env{Getenv: func(k string) string { return env[k] }, Stdin: strings.NewReader(""),
			Out: &go_, Err: &ge, Cwd: tmp, Now: time.Now})
		// error text for usage mistakes is not byte-compared (argparse vs ours), only the status
		if strings.Join(c, " ") == "show bad" {
			if pcode != gcode {
				t.Errorf("%v: exit %d vs %d", c, pcode, gcode)
			}
			continue
		}
		if norm(po.String()) != norm(go_.String()) || norm(pe.String()) != norm(ge.String()) || pcode != gcode {
			t.Errorf("%v differs\npython (exit %d): %q\ngo     (exit %d): %q\npython err: %q\ngo err: %q", c, pcode, trimTo(po.String()), gcode, trimTo(go_.String()), pe.String(), ge.String())
			continue
		}
		same++
	}
	t.Logf("%d/%d commands byte-identical", same, len(cases))
}

func trimTo(s string) string {
	if len(s) > 600 {
		return s[:600]
	}
	return s
}
