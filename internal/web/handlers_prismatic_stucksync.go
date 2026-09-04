package web

// Stuck-sync force-fail curl generator — incident-response tool for a
// GURU_IPAAS source (Prismatic CNI) whose sync got stuck in SYNCING and now
// has Prismatic rejecting a new trigger with 409. See
// internal/prismatic/stucksync.go for the endpoint/UUID-format details.
//
//	POST /api/prismatic/curls/stuck-sync
//
// Not session-scoped: the source/objectType ids are pasted by the operator
// (looked up in rdsql beforehand), nothing here derives from a session's
// project. Gated behind WebMutations + the mutation rate limiter for the
// same reason as the other curl endpoints in this package: it's an
// admin-ish action even though agent-deck itself only ever produces curl
// text, never executes the write.
import (
	"encoding/json"
	"net/http"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

type prismaticStuckSyncRowRequest struct {
	ObjectTypeID           string   `json:"objectTypeId"`
	SyncNumber             int      `json:"syncNumber"`
	StatusReason           string   `json:"statusReason"`
	DependentObjectTypeIDs []string `json:"dependentObjectTypeIds,omitempty"`
	ErrorDetails           string   `json:"errorDetails,omitempty"`
}

type prismaticStuckSyncRequest struct {
	Env      string                         `json:"env"`
	SourceID string                         `json:"sourceId"`
	Rows     []prismaticStuckSyncRowRequest `json:"rows"`
}

type prismaticStuckSyncResponse struct {
	Curls []prismatic.CurlStep `json:"curls"`
}

// handlePrismaticCurlsStuckSync serves POST /api/prismatic/curls/stuck-sync.
func (s *Server) handlePrismaticCurlsStuckSync(w http.ResponseWriter, r *http.Request) {
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

	var req prismaticStuckSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if !prismatic.ValidEnvs[req.Env] {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, `env must be "qa" or "prod"`)
		return
	}
	if req.SourceID == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "sourceId is required")
		return
	}
	if len(req.Rows) == 0 {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "at least one row is required")
		return
	}

	rows := make([]prismatic.StuckSyncRow, len(req.Rows))
	for i, row := range req.Rows {
		rows[i] = prismatic.StuckSyncRow{
			ObjectTypeID:           row.ObjectTypeID,
			SyncNumber:             row.SyncNumber,
			StatusReason:           row.StatusReason,
			DependentObjectTypeIDs: row.DependentObjectTypeIDs,
			ErrorDetails:           row.ErrorDetails,
		}
	}

	credentials, err := prismatic.GetGuruCreds(req.Env)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "failed to load Guru credentials")
		return
	}
	curls, err := prismatic.BuildStuckSyncCurls(req.Env, req.SourceID, rows, credentials)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prismaticStuckSyncResponse{Curls: curls})
}
