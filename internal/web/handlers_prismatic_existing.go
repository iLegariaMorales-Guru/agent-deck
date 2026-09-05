package web

// Fetches an already-registered source definition from Guru's admin API,
// so the curl wizard can offer "edit the existing one" — see
// internal/prismatic/existing.go. Read-only (a GET against Guru), but
// still gated behind WebMutations + the mutation rate limiter like the
// sibling curls endpoint: it's a live outbound call using stored admin
// credentials, not a local read.
import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

type prismaticExistingRequest struct {
	IntegrationDir string `json:"integrationDir"`
	Env            string `json:"env"`
}

type prismaticExistingResponse struct {
	Found      bool           `json:"found"`
	Definition map[string]any `json:"definition,omitempty"`
	// Reason explains a found:false that ISN'T "not registered yet" — e.g.
	// no Guru credentials configured. Absent when Found is true or when the
	// integration is simply new (a plain 404 from Guru).
	Reason string `json:"reason,omitempty"`
}

// handleSessionPrismaticExisting serves POST /api/sessions/{id}/prismatic/existing.
func (s *Server) handleSessionPrismaticExisting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	if !s.checkMutationsAllowed(w) {
		return
	}
	if !s.checkMutationRateLimit(w) {
		return
	}

	sess, ok := s.lookupSession(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session not found")
		return
	}
	if sess.ProjectPath == "" {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session has no project path")
		return
	}

	var req prismaticExistingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.IntegrationDir == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "integrationDir is required")
		return
	}
	if !prismatic.ValidEnvs[req.Env] {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, `env must be "qa" or "prod"`)
		return
	}

	root, integrations, ok := prismatic.FindRoot(sess.ProjectPath)
	if !ok {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "session's project is not a Prismatic CNI monorepo")
		return
	}
	if !dirIsAmongIntegrations(integrations, req.IntegrationDir) {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "integrationDir is not a detected integration in this monorepo")
		return
	}

	info, err := prismatic.ExtractSourceDefInfo(root, req.IntegrationDir)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	credentials, err := prismatic.GetGuruCreds(req.Env)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load Guru credentials")
		return
	}
	if credentials == "" {
		writeJSON(w, http.StatusOK, prismaticExistingResponse{
			Found:  false,
			Reason: "No Guru API credentials configured for " + req.Env + " — set them in the Prismatic credentials vault.",
		})
		return
	}

	def, err := prismatic.FetchExistingSourceDefinition(r.Context(), req.Env, info.SourceDefinitionType, credentials)
	if errors.Is(err, prismatic.ErrSourceDefNotFound) {
		writeJSON(w, http.StatusOK, prismaticExistingResponse{Found: false})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, ErrCodeUpstreamError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prismaticExistingResponse{Found: true, Definition: def})
}
