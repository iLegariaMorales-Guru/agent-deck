package web

// Web UI Prismatic pane handler — GET /api/sessions/{id}/prismatic.
//
// Detects whether the selected session's project lives inside a Prismatic
// CNI monorepo (internal/prismatic) and, if so, lists the sibling
// integrations the same way guru-prismatic/cli's "Prismatic Workbench" hub
// does. This is intentionally the ONLY thing this endpoint does for now —
// deploy/investigate/source-def-curls/credentials are Prismatic-API-specific
// (prism CLI, Prismatic admin API, Guru API creds) and land in later PRs;
// this one only answers "what integrations live next to this session" so
// PrismaticPane.js can prefill New Session's working dir + prompt fields.
import (
	"net/http"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

type prismaticInfoResponse struct {
	Supported bool `json:"supported"`
	// Root and Integrations are omitted (not just empty) when Supported is
	// false, so the frontend's "not a Prismatic checkout" branch doesn't
	// have to distinguish absent-because-unsupported from
	// present-but-empty.
	Root         string                  `json:"root,omitempty"`
	Integrations []prismatic.Integration `json:"integrations,omitempty"`
}

// handleSessionPrismatic serves GET /api/sessions/{id}/prismatic. Read-only,
// no mutation gate needed — mirrors handleSessionTimeline's shape.
func (s *Server) handleSessionPrismatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	sess, ok := s.lookupSession(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session not found")
		return
	}
	if sess.ProjectPath == "" {
		writeJSON(w, http.StatusOK, prismaticInfoResponse{Supported: false})
		return
	}

	root, integrations, ok := prismatic.FindRoot(sess.ProjectPath)
	if !ok {
		writeJSON(w, http.StatusOK, prismaticInfoResponse{Supported: false})
		return
	}
	if integrations == nil {
		integrations = []prismatic.Integration{}
	}
	writeJSON(w, http.StatusOK, prismaticInfoResponse{
		Supported:    true,
		Root:         root,
		Integrations: integrations,
	})
}
