package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/git"
)

// sessionHealthCacheTTL mirrors contextCacheTTL/branchCacheTTL: long enough
// that a sidebar polling this endpoint doesn't shell out to git 4x per
// session on every tick, short enough that fixing a flagged issue (commit,
// recreate the worktree, fetch --prune) clears the badge quickly.
const sessionHealthCacheTTL = 10 * time.Second

type sessionHealthCacheEntry struct {
	health git.WorktreeHealth
	at     time.Time
}

// sessionHealthCache TTL-caches CheckWorktreeHealth results keyed by session
// ID — see contextCache in handlers_analytics.go for the identical pattern.
type sessionHealthCache struct {
	mu      sync.Mutex
	entries map[string]sessionHealthCacheEntry
}

var sharedSessionHealthCache = &sessionHealthCache{entries: make(map[string]sessionHealthCacheEntry)}

func (c *sessionHealthCache) healthFor(sessionID, workDir, branch string, worktreeExpected bool) git.WorktreeHealth {
	c.mu.Lock()
	if e, ok := c.entries[sessionID]; ok && time.Since(e.at) < sessionHealthCacheTTL {
		c.mu.Unlock()
		return e.health
	}
	c.mu.Unlock()

	h := git.CheckWorktreeHealth(workDir, branch, worktreeExpected)

	c.mu.Lock()
	c.entries[sessionID] = sessionHealthCacheEntry{health: h, at: time.Now()}
	c.mu.Unlock()
	return h
}

// sessionHealth resolves the effective working directory + branch for ms
// (its worktree, if it has one, otherwise its project checkout) and runs
// the cheap local git checks from internal/git/health.go, TTL-cached per
// session. Returns the zero value for sessions with nothing local to check:
// no project path yet, or a remote SSH session whose checkout isn't on this
// machine (mirrors claudeTranscriptAnalytics's SSHHost guard).
func sessionHealth(ms *MenuSession) git.WorktreeHealth {
	if ms == nil || ms.SSHHost != "" {
		return git.WorktreeHealth{}
	}
	workDir := ms.ProjectPath
	branch := ms.Branch
	worktreeExpected := ms.WorktreePath != ""
	if worktreeExpected {
		workDir = ms.WorktreePath
		branch = ms.WorktreeBranch
	}
	if workDir == "" {
		return git.WorktreeHealth{}
	}
	return sharedSessionHealthCache.healthFor(ms.ID, workDir, branch, worktreeExpected)
}

// handleSessionHealthBatch dispatches GET/POST /api/sessions/health/batch,
// mirroring handleContextBatch's/handleCostsBatch's shape: a map of
// sessionId -> WorktreeHealth for the requested ids, with clean sessions
// (nothing to badge) simply absent from the map.
func (s *Server) handleSessionHealthBatch(w http.ResponseWriter, r *http.Request) {
	var ids []string
	switch r.Method {
	case http.MethodGet:
		if !s.authorizeRequest(r) {
			writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
			return
		}
		raw := r.URL.Query().Get("ids")
		if raw != "" {
			ids = strings.Split(raw, ",")
		}
	case http.MethodPost:
		if !s.authorizeRequest(r) {
			writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
			return
		}
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid json body")
			return
		}
		ids = req.IDs
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"health": map[string]git.WorktreeHealth{}})
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		ids = ids[:maxBatch]
	}

	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load sessions")
		return
	}
	byID := make(map[string]*MenuSession, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if item.Type == MenuItemTypeSession && item.Session != nil {
			byID[item.Session.ID] = item.Session
		}
	}

	result := make(map[string]git.WorktreeHealth, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ms, ok := byID[id]
		if !ok {
			continue
		}
		h := sessionHealth(ms)
		if !h.IsClean() {
			result[id] = h
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"health": result})
}
