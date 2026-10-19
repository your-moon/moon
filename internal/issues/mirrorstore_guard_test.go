package issues

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMirrorStoreOwnsMirrorPaths fails when a file other than mirrorstore.go touches mirror data by hand:
// os file calls, the atomic/exclusive write helpers, raw readText, or the claims/outbox/localmap paths.
// The sync engine keeps its autosync marker files; config.go reads the config file; neither may name a
// mirror path.
func TestMirrorStoreOwnsMirrorPaths(t *testing.T) {
	osCall := regexp.MustCompile(`\bos\.(ReadFile|WriteFile|ReadDir|Remove|RemoveAll|Rename|Stat|Lstat|OpenFile|Open|MkdirAll|Mkdir|Create)\(`)
	helpers := regexp.MustCompile(`\b(atomicWrite|createExcl|readText|writeIfChanged)\(`)
	mirrorNames := regexp.MustCompile(`claimsDir|localmap\(|localmap\.tsv|\ba\.outbox\(\)|index\.tsv|prs\.tsv|milestones\.tsv|labels\.tsv|"lastpull"|local-seq|local-mseq`)
	// files that may call os directly, because what they touch is not mirror data
	osAllowed := map[string]bool{"syncengine.go": true, "config.go": true}

	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "mirrorstore.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if k := strings.Index(code, "//"); k >= 0 {
				code = code[:k]
			}
			at := func(what string) {
				t.Errorf("%s:%d bypasses the mirror store (%s): %s", f, i+1, what, strings.TrimSpace(line))
			}
			if osCall.MatchString(code) && !osAllowed[f] {
				at("os file call")
			}
			if helpers.MatchString(code) && f != "syncengine.go" {
				at("file helper")
			}
			if mirrorNames.MatchString(code) {
				at("mirror path")
			}
		}
	}
}
