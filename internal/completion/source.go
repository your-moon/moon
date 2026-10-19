// Package completion is the one place that knows what can be typed after `moon`. The shell scripts, the
// hidden `moon __complete` command and the dashboard's Tab key all call into it. It reads the command table
// of internal/issues and the local mirror; it never talks to GitHub and never syncs.
package completion

import (
	"sync"

	"github.com/your-moon/moon/internal/issues"
)

// Source is the mirror data completion draws from.
type Source interface {
	Aliases() []string
	Items() []issues.Item
	Milestones() []issues.MilestoneRow
	PRs() []issues.PRItem
}

// SessionSource reads a mirror through an issues.Session, once and lazily.
type SessionSource struct {
	S  *issues.Session
	Me string // the agent asking ($USER); empty when unknown

	mu      sync.Mutex
	snap    *issues.Snapshot
	ms      []issues.MilestoneRow
	msDone  bool
	prs     []issues.PRItem
	prsDone bool
}

func (s *SessionSource) load() *issues.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap == nil {
		snap := s.S.LoadQuick()
		s.snap = &snap
	}
	return s.snap
}

func (s *SessionSource) Aliases() []string    { return s.load().Aliases }
func (s *SessionSource) Items() []issues.Item { return s.load().Items }
func (s *SessionSource) Milestones() []issues.MilestoneRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.msDone {
		s.ms, s.msDone = s.S.Milestones(), true
	}
	return s.ms
}
func (s *SessionSource) PRs() []issues.PRItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prsDone {
		s.prs, s.prsDone = s.S.PRs(), true
	}
	return s.prs
}

// Agent is who is asking.
func (s *SessionSource) Agent() string { return s.Me }

// Scope is the repos commands default to here (the project the working directory belongs to); empty means every repo.
func (s *SessionSource) Scope() []string {
	if sc := s.S.Scope(); sc.Kind != issues.KindGlobal {
		return sc.Repos
	}
	return nil
}

// agented is implemented by a Source that knows who is asking, so claim offers that agent's own claims.
type agented interface{ Agent() string }

func agentOf(s Source) string {
	if a, ok := s.(agented); ok {
		return a.Agent()
	}
	return ""
}

// scoped is implemented by a Source that knows the project scope; a Source without it offers every repo.
type scoped interface{ Scope() []string }

// OpenSession finds the config exactly as the CLI does (--config, $MOON_CONFIG, moon.toml upward, the user
// config) and returns its mirror, or nil when there is none. It never prints.
func OpenSession(config, dir string, e issues.Env) Source {
	sess, err := issues.NewSession(config, dir, e)
	if err != nil {
		return nil
	}
	return &SessionSource{S: sess, Me: e.Getenv("USER")}
}
