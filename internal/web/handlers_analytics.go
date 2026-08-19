package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// contextCacheTTL mirrors the TUI's analyticsCacheTTL (internal/ui/home.go)
// — long enough that a sidebar polling this endpoint every few seconds
// doesn't re-parse a Claude transcript on every request, short enough that
// an active session's bar keeps moving.
const contextCacheTTL = 5 * time.Second

// liveModel is the display-friendly model name/version session.ParseModelID
// derived from the last assistant message actually seen in the transcript —
// distinct from MenuSession.Model, which reflects what was REQUESTED at
// launch (empty when the session was created with "Tool default"). Letting
// the sidebar fall back to this is what makes a "Tool default" session's
// chip show "Sonnet 5" instead of nothing, once Claude has said a word.
type liveModel struct {
	Model   string `json:"model"`
	Version string `json:"version,omitempty"`
}

type contextCacheEntry struct {
	percent float64
	model   liveModel
	// cost is CalculateCost's price-table estimate from the transcript's
	// token usage — the same number the TUI's analytics panel shows
	// (analytics_panel.go), not a real billed figure from the cost-events
	// ledger (handlers_costs.go). It exists purely as a fallback for the
	// common case where cost-event hooks were never wired up for this
	// profile, so the sidebar doesn't show "$0.00" on a session that has
	// plainly been used. The ledger figure always wins when it has one.
	cost float64
	at   time.Time
}

// contextCache TTL-caches parsed context-window percentage + detected model,
// keyed by session ID. Parsing a Claude JSONL transcript is real file I/O
// (unlike the DB-backed cost lookups in handlers_costs.go), so this exists
// specifically to keep /api/analytics/context/batch cheap under polling.
type contextCache struct {
	mu      sync.Mutex
	entries map[string]contextCacheEntry
}

var sharedContextCache = &contextCache{entries: make(map[string]contextCacheEntry)}

func (c *contextCache) analyticsFor(sessionID, jsonlPath string) contextCacheEntry {
	c.mu.Lock()
	if e, ok := c.entries[sessionID]; ok && time.Since(e.at) < contextCacheTTL {
		c.mu.Unlock()
		return e
	}
	c.mu.Unlock()

	var entry contextCacheEntry
	if analytics, err := session.ParseSessionJSONL(jsonlPath); err == nil {
		entry.percent = analytics.ContextPercent(0)
		if entry.percent > 100 {
			entry.percent = 100
		}
		if info := session.ParseModelID(analytics.Model); info.Model != "" {
			entry.model = liveModel{Model: info.Model, Version: info.Version}
		}
		entry.cost = analytics.CalculateCost(analytics.Model)
	}
	entry.at = time.Now()

	c.mu.Lock()
	c.entries[sessionID] = entry
	c.mu.Unlock()
	return entry
}

// claudeTranscriptAnalytics resolves a Claude session's context-window
// percentage + detected model from its transcript. Returns the zero value
// for non-Claude tools, remote sessions (transcript isn't on this machine),
// or sessions with no resolvable transcript yet — all of which the sidebar
// just renders as "no bar / no chip" rather than a bogus 0%.
func claudeTranscriptAnalytics(ms *MenuSession) contextCacheEntry {
	if ms == nil || ms.ClaudeSessionID == "" {
		return contextCacheEntry{}
	}
	if !session.IsClaudeCompatible(ms.Tool) {
		return contextCacheEntry{}
	}
	if ms.SSHHost != "" {
		// Transcript lives on the remote host, not here (see
		// Instance.TranscriptIsResolvableLocally).
		return contextCacheEntry{}
	}

	// Prefer Claude Code's OWN numbers over our transcript-derived estimate,
	// when available (see internal/session/claude_statusline.go). This is
	// authoritative — Claude computes context_window.used_percentage and
	// cost.total_cost_usd itself on every turn — instead of agent-deck
	// re-deriving them via a model-context-window lookup table that has
	// repeatedly gone stale the moment a new model family ships (#1963).
	// Requires the user to have run `agent-deck hooks install-statusline`
	// (opt-in: it edits global Claude Code settings); falls through to the
	// transcript parse below when that file doesn't exist yet, e.g. before
	// the first turn since install.
	if ctx, ok := session.ReadStatusLineContext(ms.ClaudeSessionID); ok {
		entry := contextCacheEntry{percent: ctx.UsedPercentage, cost: ctx.CostUSD, at: time.Now()}
		if entry.percent > 100 {
			entry.percent = 100
		}
		if info := session.ParseModelID(ctx.Model); info.Model != "" {
			entry.model = liveModel{Model: info.Model, Version: info.Version}
		}
		return entry
	}

	path := session.ResolveClaudeTranscriptPath(ms.ProjectPath, ms.ClaudeSessionID)
	if path == "" {
		return contextCacheEntry{}
	}
	return sharedContextCache.analyticsFor(ms.ID, path)
}

// handleContextBatch dispatches GET/POST /api/analytics/context/batch,
// mirroring handlers_costs.go's /api/costs/batch shape: a map of
// sessionId -> contextPercent for the requested ids. Gemini sessions are
// already carrying ContextPercent on their MenuSession (cheap, no I/O — see
// toMenuSession), so this endpoint only has work to do for Claude sessions;
// ids for other tools simply come back absent from the map.
func (s *Server) handleContextBatch(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]any{
			"context": map[string]float64{}, "liveModel": map[string]liveModel{}, "estimatedCost": map[string]float64{},
		})
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

	context := make(map[string]float64, len(ids))
	models := make(map[string]liveModel, len(ids))
	estimatedCosts := make(map[string]float64, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ms, ok := byID[id]
		if !ok {
			continue
		}
		entry := claudeTranscriptAnalytics(ms)
		if entry.percent > 0 {
			context[id] = entry.percent
		}
		// Only fall back to the transcript-detected model when the session
		// wasn't launched with an explicit one — the requested model always
		// wins over what the last turn happened to run on.
		if ms.Model == "" && entry.model.Model != "" {
			models[id] = entry.model
		}
		if entry.cost > 0 {
			estimatedCosts[id] = entry.cost
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"context": context, "liveModel": models, "estimatedCost": estimatedCosts})
}
