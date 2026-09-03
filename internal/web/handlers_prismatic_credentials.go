package web

// Web UI Prismatic credentials vault — PR 2/4 of the Prismatic pane build.
//
//	GET    /api/prismatic/credentials  -> presence status (never the secret)
//	POST   /api/prismatic/credentials  -> set {kind, env, value}
//	DELETE /api/prismatic/credentials  -> clear {kind, env}
//
// Not session-scoped (unlike /api/sessions/{id}/prismatic): these are
// machine-wide secrets, same scope as cni-cli's ~/.cni-cli/*.json. Storage
// and validation live in internal/prismatic; this file is just the HTTP
// envelope + mutation gating, same shape as handlers_skills.go.
import (
	"encoding/json"
	"net/http"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

type prismaticCredentialRequest struct {
	Kind  string `json:"kind"`  // "prism" | "guru"
	Env   string `json:"env"`   // "qa" | "prod"
	Value string `json:"value"` // POST only
}

// handlePrismaticCredentials serves all three verbs on
// /api/prismatic/credentials.
func (s *Server) handlePrismaticCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		status, err := prismatic.LoadStatus()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load credential status")
			return
		}
		writeJSON(w, http.StatusOK, status)

	case http.MethodPost:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		var req prismaticCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		var err error
		switch req.Kind {
		case "prism":
			err = prismatic.SetPrismToken(req.Env, req.Value)
		case "guru":
			err = prismatic.SetGuruCreds(req.Env, req.Value)
		default:
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, `kind must be "prism" or "guru"`)
			return
		}
		if err != nil {
			// Every error from the prismatic package here is a validation
			// failure (unknown env, empty value, missing ':') — none of them
			// warrant a 500, and the message is safe to return verbatim (no
			// secret value ever appears in an error string).
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	case http.MethodDelete:
		if !s.checkMutationsAllowed(w) {
			return
		}
		if !s.checkMutationRateLimit(w) {
			return
		}
		var req prismaticCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
			return
		}
		var err error
		switch req.Kind {
		case "prism":
			err = prismatic.ClearPrismToken(req.Env)
		case "guru":
			err = prismatic.ClearGuruCreds(req.Env)
		default:
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, `kind must be "prism" or "guru"`)
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
	}
}
