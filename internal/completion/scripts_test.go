package completion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScriptUnknownShell(t *testing.T) {
	if _, err := Script("tcsh", "moon"); err == nil {
		t.Fatal("want an error")
	}
}

func TestScriptsAreSyntacticallyValid(t *testing.T) {
	for _, tc := range []struct {
		shell string
		args  []string
	}{{"zsh", []string{"-n"}}, {"bash", []string{"-n"}}, {"fish", []string{"--no-execute"}}} {
		t.Run(tc.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tc.shell)
			if err != nil {
				t.Skipf("%s is not installed; its script was NOT syntax-checked here", tc.shell)
			}
			sc, err := Script(tc.shell, "moon")
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(t.TempDir(), "moon.script")
			os.WriteFile(p, []byte(sc), 0o644)
			if out, err := exec.Command(bin, append(tc.args, p)...).CombinedOutput(); err != nil {
				t.Fatalf("%s rejects its script: %v\n%s", tc.shell, err, out)
			}
		})
	}
}

func TestScriptsCallBackIntoTheBinary(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish"} {
		sc, _ := Script(sh, "moon")
		if !strings.Contains(sc, "__complete") {
			t.Errorf("%s script does not call __complete", sh)
		}
		if strings.Count(sc, "\n") > 45 {
			t.Errorf("%s script has %d lines; keep logic in Go", sh, strings.Count(sc, "\n"))
		}
	}
}

func TestBashRegistersWithComplete(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	sc, _ := Script("bash", "moon")
	p := filepath.Join(t.TempDir(), "moon.bash")
	os.WriteFile(p, []byte(sc), 0o644)
	out, err := exec.Command(bash, "--norc", "-c", fmt.Sprintf("source %s; complete -p moon", p)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "complete -F _moon_complete moon" {
		t.Fatalf("complete -p: %v %q", err, out)
	}
}

func TestZshRegistersWithCompdef(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	sc, _ := Script("zsh", "moon")
	p := filepath.Join(t.TempDir(), "moon.zsh")
	os.WriteFile(p, []byte(sc), 0o644)
	cmd := exec.Command(zsh, "-fc", fmt.Sprintf("autoload -Uz compinit && compinit -u -d %s/zcd && source %s && print -r -- $_comps[moon]", t.TempDir(), p))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "_moon" {
		t.Fatalf("compdef did not register _moon: %v %q", err, out)
	}
}

func TestBashEscaping(t *testing.T) {
	cases := []struct {
		raw, cur  string
		quote     byte
		val, want string
	}{
		{"", "", 0, "v1 beta", `v1\ beta`},
		{`v1\ b`, `v1\ b`, 0, "v1 beta", `v1\ beta`},
		{`"v1 b`, `"v1 b`, '"', "v1 beta", `"v1 beta`},
		{`'v1 b`, `'v1 b`, '\'', "it's", `'it'\''s`},
		{"kind:", ":", 0, "kind:bug", ":bug"},
		{"--repo=e", "e", 0, "--repo=erp", "erp"},
		{"erp#5", "erp#5", 0, "erp#51", "erp#51"},
		{`"a$b`, `"a$b`, '"', "a$b c", `"a\$b c`},
	}
	for _, c := range cases {
		got := bashEscape(Result{Cands: []Candidate{{Value: c.val, Desc: "x"}}}, c.raw, c.cur, c.quote).Cands[0]
		if got.Value != c.want || got.Desc != "" {
			t.Errorf("%+v: got %q", c, got)
		}
	}
}

func TestSplitLine(t *testing.T) {
	cases := []struct {
		line  string
		words []string
		raw   string
		quote byte
	}{
		{"moon issues ls ", []string{"moon", "issues", "ls", ""}, "", 0},
		{"moon issues ls -m 'v1 b", []string{"moon", "issues", "ls", "-m", "v1 b"}, "'v1 b", '\''},
		{`moon ls -m "v1 beta" -L`, []string{"moon", "ls", "-m", "v1 beta", "-L"}, "-L", 0},
		{`moon ls v1\ b`, []string{"moon", "ls", "v1 b"}, `v1\ b`, 0},
		{`moon ls "a\"b" `, []string{"moon", "ls", `a"b`, ""}, "", 0},
	}
	for _, c := range cases {
		w, raw, q := splitLine(c.line)
		if strings.Join(w, "|") != strings.Join(c.words, "|") || raw != c.raw || q != c.quote {
			t.Errorf("%q: got %q raw %q quote %q", c.line, w, raw, q)
		}
	}
}

func bigFixture(t testing.TB, n int) *fixture {
	d := t.TempDir()
	f := &fixture{dir: d, cfg: filepath.Join(d, "moon.toml")}
	os.WriteFile(f.cfg, []byte("mirror_dir = \"mirror\"\n[repos.erp]\ngithub = \"example/erp\"\n"), 0o644)
	var b strings.Builder
	for i := 1; i <= n; i++ {
		st := "open"
		if i%4 == 0 {
			st = "closed"
		}
		fmt.Fprintf(&b, "%d\t%s\tv%d\tkind:bug,area::a%d\t2026-02-%02dT00:00:00Z\tinvented issue number %d\n", i, st, i%5, i%7, 1+i%27, i)
	}
	p := filepath.Join(d, "mirror", "erp", "index.tsv")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(b.String()), 0o644)
	var ms strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&ms, "v%d\topen\t1\t1\t\n", i)
	}
	os.WriteFile(filepath.Join(d, "mirror", "erp", "milestones.tsv"), []byte(ms.String()), 0o644)
	return f
}

// Dynamic completion reads the mirror only; on 1000 issues it must stay far under 100 ms.
func TestCompletionIsFastOnAThousandIssues(t *testing.T) {
	f := bigFixture(t, 1000)
	best := time.Hour
	for i := 0; i < 5; i++ {
		start := time.Now()
		r := f.complete("issues", "show", "erp#")
		if d := time.Since(start); d < best {
			best = d
		}
		if len(r.Cands) != MaxRefs {
			t.Fatalf("want the %d-ref cap, got %d", MaxRefs, len(r.Cands))
		}
	}
	if best > 100*time.Millisecond {
		t.Fatalf("best of 5 took %v, want < 100ms", best)
	}
	t.Logf("1000 issues: refs in %v", best)
}

func BenchmarkRefs1000(b *testing.B) {
	f := bigFixture(b, 1000)
	for i := 0; i < b.N; i++ {
		f.complete("issues", "claim", "erp#1")
	}
}
