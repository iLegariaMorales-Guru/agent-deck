package web

import (
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/git"
)

// branchCacheTTL mirrors the TUI's analyticsCacheTTL (internal/ui/home.go):
// short enough that switching a checkout shows up quickly, long enough that
// a sidebar with many sessions sharing a project doesn't shell out to git
// once per session on every menu snapshot (rebuilt every couple of seconds
// in headless mode, and on every session-list change in the TUI).
const branchCacheTTL = 5 * time.Second

type branchCacheEntry struct {
	branch string
	at     time.Time
}

// branchCache resolves a project directory's current git branch with a
// short-TTL cache, keyed by directory. Safe for concurrent use.
type branchCache struct {
	mu      sync.Mutex
	entries map[string]branchCacheEntry
}

func newBranchCache() *branchCache {
	return &branchCache{entries: make(map[string]branchCacheEntry)}
}

// sharedBranchCache is used by toMenuSession, the single funnel both the
// TUI-attached (MemoryMenuData, pushed on every session-list change) and
// headless (SessionDataService, polled every menuEventsPollInterval) code
// paths build a MenuSnapshot through.
var sharedBranchCache = newBranchCache()

// resolve returns the current branch for dir, using the TTL cache. Errors
// (not a git repo, git not installed, dir empty, etc.) resolve to "" —
// callers render that as "no branch" rather than surfacing a git error.
func (c *branchCache) resolve(dir string) string {
	if dir == "" {
		return ""
	}

	c.mu.Lock()
	if e, ok := c.entries[dir]; ok && time.Since(e.at) < branchCacheTTL {
		c.mu.Unlock()
		return e.branch
	}
	c.mu.Unlock()

	branch, err := git.GetCurrentBranch(dir)
	if err != nil {
		branch = ""
	}

	c.mu.Lock()
	c.entries[dir] = branchCacheEntry{branch: branch, at: time.Now()}
	c.mu.Unlock()
	return branch
}
