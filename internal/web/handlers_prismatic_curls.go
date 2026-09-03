package web

// Source-definition curl generator — PR 3/4 of the Prismatic pane build.
//
//	POST /api/sessions/{id}/prismatic/curls
//
// Parses the selected integration's createSource.ts, resolves its
// Prismatic ipaasIntegrationId (cache -> `prism` CLI -> manual), and
// returns the paste-and-run curl sequence that registers the source
// definition in Guru. Session-scoped (like handleSessionPrismatic) because
// the integration lives relative to that session's monorepo root; the
// generation itself has nothing session-specific about it otherwise.
//
// Gated behind WebMutations + the mutation rate limiter even though it
// doesn't write any agent-deck session state: it can shell out to an
// external binary (`prism`) and it writes the ipaas-id cache file, both of
// which are closer to a mutation than a plain read.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

type prismaticCurlsRequest struct {
	IntegrationDir string   `json:"integrationDir"`
	Env            string   `json:"env"`
	Category       string   `json:"category"`
	TeamIDs        []string `json:"teamIds,omitempty"` // prod only; empty = default team
	IpaasID        string   `json:"ipaasId,omitempty"` // manual override, skips resolution
	// Name/IconURL override what was parsed out of createSource.ts / derived
	// from it — e.g. the source has its name in ALL CAPS and Guru's
	// convention wants Title Case. Empty means "use the extracted default".
	Name    string `json:"name,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
}

type prismaticCurlsResponse struct {
	Info  prismatic.SourceDefInfo `json:"info"`
	Ipaas *ipaasResult            `json:"ipaas,omitempty"`
	Curls []prismatic.CurlStep    `json:"curls,omitempty"`
	// NeedsInput is set instead of Curls when resolution didn't land on a
	// single confident match: "manual" (no token / prism error / no
	// results — user must paste an ipaasId) or "selection" (prism found
	// more than one plausible match — user must pick one from Options).
	NeedsInput string                 `json:"needsInput,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
	Options    []prismatic.IpaasMatch `json:"options,omitempty"`
}

type ipaasResult struct {
	prismatic.IpaasMatch
	Source string `json:"source"` // "cache" | "prism" | "manual"
}

const prismaticResolveTimeout = 20 * time.Second

// handleSessionPrismaticCurls serves POST /api/sessions/{id}/prismatic/curls.
func (s *Server) handleSessionPrismaticCurls(w http.ResponseWriter, r *http.Request) {
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

	var req prismaticCurlsRequest
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
	if req.Category == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "category is required")
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

	ipaas, needsInput, reason, options, err := resolveIpaasForRequest(r.Context(), req, info)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to resolve the Prismatic integration id")
		return
	}
	if needsInput != "" {
		writeJSON(w, http.StatusOK, prismaticCurlsResponse{
			Info:       info,
			NeedsInput: needsInput,
			Reason:     reason,
			Options:    options,
		})
		return
	}

	teams := teamsForIDs(req.TeamIDs)
	credentials, err := prismatic.GetGuruCreds(req.Env)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load Guru credentials")
		return
	}
	curls, err := prismatic.BuildCurls(info, req.Env, ipaas.ID, req.Category, credentials, teams, req.Name, req.IconURL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to build curl commands")
		return
	}

	writeJSON(w, http.StatusOK, prismaticCurlsResponse{
		Info:  info,
		Ipaas: ipaas,
		Curls: curls,
	})
}

type prismaticUpdateCurlRequest struct {
	Env        string         `json:"env"`
	Definition map[string]any `json:"definition"`
}

type prismaticUpdateCurlResponse struct {
	Curls []prismatic.CurlStep `json:"curls"`
}

// handlePrismaticCurlsUpdate serves POST /api/prismatic/curls/update — the
// "edit an existing source definition" counterpart to
// handleSessionPrismaticCurls's create flow. Not session-scoped: the
// caller already fetched+edited the definition via
// POST /api/sessions/{id}/prismatic/existing, so everything needed (the
// type, the edited fields) travels in the request body.
func (s *Server) handlePrismaticCurlsUpdate(w http.ResponseWriter, r *http.Request) {
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

	var req prismaticUpdateCurlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if !prismatic.ValidEnvs[req.Env] {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, `env must be "qa" or "prod"`)
		return
	}
	if len(req.Definition) == 0 {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "definition is required")
		return
	}

	credentials, err := prismatic.GetGuruCreds(req.Env)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load Guru credentials")
		return
	}
	curl, err := prismatic.BuildUpdateCurl(req.Definition, req.Env, credentials)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prismaticUpdateCurlResponse{Curls: []prismatic.CurlStep{curl}})
}

func dirIsAmongIntegrations(integrations []prismatic.Integration, dir string) bool {
	for _, in := range integrations {
		if in.Dir == dir {
			return true
		}
	}
	return false
}

func teamsForIDs(ids []string) []prismatic.Team {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]prismatic.Team, len(prismatic.TestTeams))
	for _, t := range prismatic.TestTeams {
		byID[t.ID] = t
	}
	teams := make([]prismatic.Team, 0, len(ids))
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			teams = append(teams, t)
		} else {
			teams = append(teams, prismatic.Team{ID: id, Name: id})
		}
	}
	return teams
}

// resolveIpaasForRequest implements the cache -> manual-override -> prism
// CLI resolution order. Returns either a confident ipaasResult, or a
// needsInput/reason/options triple for the frontend's wizard to act on.
func resolveIpaasForRequest(ctx context.Context, req prismaticCurlsRequest, info prismatic.SourceDefInfo) (*ipaasResult, string, string, []prismatic.IpaasMatch, error) {
	if req.IpaasID != "" {
		match := prismatic.IpaasMatch{ID: req.IpaasID, Name: info.SourceName}
		if err := prismatic.SetCachedIpaasMatch(req.Env, req.IntegrationDir, match); err != nil {
			return nil, "", "", nil, err
		}
		return &ipaasResult{IpaasMatch: match, Source: "manual"}, "", "", nil, nil
	}

	if cached, ok := prismatic.GetCachedIpaasMatch(req.Env, req.IntegrationDir); ok {
		return &ipaasResult{IpaasMatch: cached, Source: "cache"}, "", "", nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, prismaticResolveTimeout)
	defer cancel()

	candidates := prismatic.NameCandidates(info, req.IntegrationDir)
	res, err := prismatic.ResolveIpaasID(ctx, req.Env, candidates)
	if err != nil {
		if errors.Is(err, prismatic.ErrPrismTokenNotConfigured) {
			return nil, "manual", "No Prism refresh token configured for " + req.Env + " — set one in the Prismatic credentials vault, or paste the ipaasIntegrationId directly.", nil, nil
		}
		// A prism CLI failure (binary missing, timeout, network) is also a
		// "fall back to manual" case, not a 500 — the wizard should let the
		// user paste an ID and move on rather than dead-ending.
		return nil, "manual", err.Error(), nil, nil
	}
	if res.Match != nil {
		if cacheErr := prismatic.SetCachedIpaasMatch(req.Env, req.IntegrationDir, *res.Match); cacheErr != nil {
			return nil, "", "", nil, cacheErr
		}
		return &ipaasResult{IpaasMatch: *res.Match, Source: "prism"}, "", "", nil, nil
	}
	if len(res.Options) == 0 {
		return nil, "manual", "No matching Prismatic integration found for \"" + info.SourceName + "\" — paste the ipaasIntegrationId directly.", nil, nil
	}
	return nil, "selection", "Multiple Prismatic integrations matched — pick the right one.", res.Options, nil
}
