package issues

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo is one tracked GitHub repository under a short local alias.
type Repo struct {
	Alias  string
	GitHub string // owner/name
}

// Config is the parsed moon.toml. Parsed once at the boundary; nothing else reads the file.
type Config struct {
	Dir       string // directory holding moon.toml
	MirrorDir string // absolute
	Repos     []Repo // in file order
}

var aliasRe = regexp.MustCompile(`^[a-z0-9_]+$`)
var githubRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// FindConfig resolves the config path: explicit flag, $MOON_CONFIG, then walk up from start.
func FindConfig(explicit, env, start string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env != "" {
		return env, nil
	}
	d := start
	for {
		p := filepath.Join(d, "moon.toml")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no moon.toml found (searched up from %s); create one or pass --config / set MOON_CONFIG", start)
		}
		d = parent
	}
}

// LoadConfig parses the small TOML subset moon needs:
//
//	mirror_dir = "path"            # optional, default .moon/issues beside moon.toml
//	[repos.<alias>]
//	github = "owner/name"
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c := &Config{Dir: filepath.Dir(abs)}
	mirror := ""
	var cur *Repo
	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.GitHub == "" {
			return fmt.Errorf("%s: [repos.%s] needs github = \"owner/name\"", path, cur.Alias)
		}
		c.Repos = append(c.Repos, *cur)
		cur = nil
		return nil
	}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if err := flush(); err != nil {
				return nil, err
			}
			if !strings.HasSuffix(line, "]") || !strings.HasPrefix(line, "[repos.") {
				return nil, fmt.Errorf("%s:%d: unsupported table %s (want [repos.<alias>])", path, n, line)
			}
			alias := strings.TrimSuffix(strings.TrimPrefix(line, "[repos."), "]")
			if !aliasRe.MatchString(alias) {
				return nil, fmt.Errorf("%s:%d: alias %q must match [a-z0-9_]+", path, n, alias)
			}
			for _, r := range c.Repos {
				if r.Alias == alias {
					return nil, fmt.Errorf("%s:%d: duplicate repo %q", path, n, alias)
				}
			}
			cur = &Repo{Alias: alias}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want key = \"value\"", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			return nil, fmt.Errorf("%s:%d: value of %s must be a quoted string", path, n, k)
		}
		v = v[1 : len(v)-1]
		switch {
		case cur == nil && k == "mirror_dir":
			mirror = v
		case cur != nil && k == "github":
			if !githubRe.MatchString(v) {
				return nil, fmt.Errorf("%s:%d: github must be owner/name, got %q", path, n, v)
			}
			cur.GitHub = v
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %s", path, n, k)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(c.Repos) == 0 {
		return nil, fmt.Errorf("%s: no [repos.<alias>] declared", path)
	}
	switch {
	case mirror == "":
		c.MirrorDir = filepath.Join(c.Dir, ".moon", "issues")
	case filepath.IsAbs(mirror):
		c.MirrorDir = mirror
	default:
		c.MirrorDir = filepath.Join(c.Dir, mirror)
	}
	return c, nil
}

func stripComment(s string) string {
	in := false
	for i, r := range s {
		if r == '"' {
			in = !in
		}
		if r == '#' && !in {
			return s[:i]
		}
	}
	return s
}

func (a *App) Shorts() []string {
	out := make([]string, len(a.Repos))
	for i, r := range a.Repos {
		out[i] = r.Alias
	}
	return out
}

func (a *App) hasRepo(alias string) bool {
	for _, r := range a.Repos {
		if r.Alias == alias {
			return true
		}
	}
	return false
}

func (a *App) repoFull(alias string) string {
	for _, r := range a.Repos {
		if r.Alias == alias {
			return r.GitHub
		}
	}
	return ""
}
