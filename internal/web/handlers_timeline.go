package web

// Web UI Activity Timeline panel handler — GET /api/sessions/{id}/timeline.
//
// Merges four kinds of event, all cheap and local, none of them new
// infrastructure:
//   - "commit" / "push"   — internal/git.RecentCommits / RecentPushes,
//                            same worktree/branch resolution sessionHealth
//                            already does (handlers_health.go).
//   - "prompt" / "edit" /
//     "pr" / "compact"    — session.ParseTranscriptTimeline, a second pass
//                            over the same Claude JSONL transcript
//                            claudeTranscriptAnalytics already reads for
//                            cost/context (handlers_analytics.go).
//
// Unlike those two batch endpoints, this one is NOT polled — the right
// rail only fetches it once the Timeline card is actually toggled on for
// the selected session (see RightRail.js), so a short TTL cache here is
// about not re-scanning a transcript on every re-render while the card is
// open, not about surviving a tight poll loop.

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

const sessionTimelineCacheTTL = 15 * time.Second

// maxTimelineEvents caps the merged, sorted feed returned to the client.
// hasMore on the response says honestly when older events were cut rather
// than silently truncating.
const maxTimelineEvents = 150

// timelineEventOut is one item in the response's "events" array, shaped to
// cover all four kinds without a different envelope per kind (the UI
// switches on Kind — see the .tl-row classes in RightRail.js).
type timelineEventOut struct {
	Kind  string    `json:"kind"` // commit | push | prompt | edit | pr | compact
	Time  time.Time `json:"time"`
	Text  string    `json:"text,omitempty"`
	Path  string    `json:"path,omitempty"`  // edit events
	Hash  string    `json:"hash,omitempty"`  // commit/push events
	Files int       `json:"files,omitempty"` // commit events
}

type sessionTimelineResponse struct {
	SessionID string             `json:"sessionId"`
	Events    []timelineEventOut `json:"events"`
	HasMore   bool               `json:"hasMore"`
}

type timelineCacheEntry struct {
	resp sessionTimelineResponse
	at   time.Time
}

type timelineCache struct {
	mu      sync.Mutex
	entries map[string]timelineCacheEntry
}

var sharedTimelineCache = &timelineCache{entries: make(map[string]timelineCacheEntry)}

func (c *timelineCache) forSession(sessionID string, build func() sessionTimelineResponse) sessionTimelineResponse {
	c.mu.Lock()
	if e, ok := c.entries[sessionID]; ok && time.Since(e.at) < sessionTimelineCacheTTL {
		c.mu.Unlock()
		return e.resp
	}
	c.mu.Unlock()

	resp := build()

	c.mu.Lock()
	c.entries[sessionID] = timelineCacheEntry{resp: resp, at: time.Now()}
	c.mu.Unlock()
	return resp
}

// buildSessionTimeline gathers events from git (commits/pushes on ms's
// effective worktree+branch) and, for a locally-resolvable Claude session,
// its transcript (prompts/edits/pr/compact). Any single source coming up
// empty (no git repo, no transcript yet, no upstream configured) just
// contributes nothing — same "absent means nothing to report" convention
// as sessionHealth.
func buildSessionTimeline(ms *MenuSession) sessionTimelineResponse {
	resp := sessionTimelineResponse{SessionID: ms.ID}
	if ms == nil {
		return resp
	}

	workDir := ms.ProjectPath
	branch := ms.Branch
	if ms.WorktreePath != "" {
		workDir = ms.WorktreePath
		branch = ms.WorktreeBranch
	}

	var events []timelineEventOut

	if workDir != "" && ms.SSHHost == "" {
		// Fetch up to the full response cap from each source rather than a
		// smaller per-source limit — otherwise HasMore below would reflect
		// "git had more than 30" instead of "the merged feed had more than
		// maxTimelineEvents", which is the honest thing to report truncation
		// against.
		//
		// ms.CreatedAt bounds both calls: without it, a session's timeline
		// showed the branch's ENTIRE commit/push history, including
		// everything made before this session (or its worktree) ever
		// existed — a real false-positive hit live on every session
		// checked, since a feature branch is almost never created from a
		// completely empty history.
		for _, c := range git.RecentCommits(workDir, branch, maxTimelineEvents, ms.CreatedAt) {
			events = append(events, timelineEventOut{
				Kind: "commit", Time: c.Time, Text: c.Subject, Hash: c.Hash, Files: c.FilesChanged,
			})
		}
		for _, p := range git.RecentPushes(workDir, branch, maxTimelineEvents, ms.CreatedAt) {
			events = append(events, timelineEventOut{Kind: "push", Time: p.Time, Hash: p.Hash})
		}
	}

	if ms.SSHHost == "" && ms.ClaudeSessionID != "" && session.IsClaudeCompatible(ms.Tool) {
		if path := session.ResolveClaudeTranscriptPath(ms.ProjectPath, ms.ClaudeSessionID); path != "" {
			if parsed, err := session.ParseTranscriptTimeline(path); err == nil {
				for _, ev := range parsed {
					events = append(events, timelineEventOut{
						Kind: ev.Kind, Time: ev.Time, Text: ev.Text, Path: ev.Path,
					})
				}
			}
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Time.After(events[j].Time) })

	if len(events) > maxTimelineEvents {
		events = events[:maxTimelineEvents]
		resp.HasMore = true
	}
	resp.Events = events
	return resp
}

// handleSessionTimeline serves GET /api/sessions/{id}/timeline. Non-GET
// returns 405, unknown session id returns 404 — same contract as
// handleSessionChildren.
func (s *Server) handleSessionTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	sessionID := r.PathValue("id")
	snapshot, err := s.menuData.LoadMenuSnapshot()
	if err != nil || snapshot == nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load session data")
		return
	}

	var ms *MenuSession
	for _, item := range snapshot.Items {
		if item.Type == MenuItemTypeSession && item.Session != nil && item.Session.ID == sessionID {
			ms = item.Session
			break
		}
	}
	if ms == nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session not found")
		return
	}

	resp := sharedTimelineCache.forSession(sessionID, func() sessionTimelineResponse {
		return buildSessionTimeline(ms)
	})
	writeJSON(w, http.StatusOK, resp)
}
