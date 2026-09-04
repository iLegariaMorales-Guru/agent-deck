package prismatic

// Stuck-sync force-fail curl generator — an admin/incident-response tool for
// a GURU_IPAAS (Prismatic CNI) source whose sync got stuck in SYNCING (a
// crashed worker, a job timeout, an expired token) and now has Prismatic
// rejecting a new trigger with 409. The fix is a live Guru admin API call
// that forces the stuck row(s) to FAILED so a retry isn't blocked:
//
//	PUT /api/v1/admin/sources/{sourceId}/types/{objectTypeId}/status
//
// Same "generate the curl, never run it" stance as sourcedef.go's create/
// update flows — this only ever produces PUT curl text. The write against a
// customer's real source data stays a human decision, made by pasting and
// running the curl after confirming the IDs/syncNumber against the DB.
import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// StuckSyncStatusReasons are the statusReason values that map to FAILED
// (resumable by a plain retry) rather than INVALID (which needs a separate
// revalidatesync call). Deliberately excludes INVALID_AUTHENTICATION and
// TARGET_INACCESSIBLE — offering them here would produce a curl that looks
// like it fixes the stuck row but actually leaves it in a different broken
// state.
var StuckSyncStatusReasons = []string{"JOB_TIMEOUT", "API_TIMEOUT", "API_ERROR", "UNKNOWN_ERROR"}

var stuckSyncStatusReasonSet = func() map[string]bool {
	set := make(map[string]bool, len(StuckSyncStatusReasons))
	for _, r := range StuckSyncStatusReasons {
		set[r] = true
	}
	return set
}()

// hexNoDashesRe matches a bare 32-hex-character id — the shape rdsql's
// encode(id,'hex') returns, and NOT what the admin API accepts.
var hexNoDashesRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// NormalizeStatusEndpointID reformats a plain 32-char hex id (as returned by
// `encode(id,'hex')` in rdsql) into the dashed 8-4-4-4-12 UUID form the
// status endpoint requires. Anything else — an already-dashed UUID, or a
// PermissionEntityType literal like "USER"/"OBJECT_ACCESS"/"TAG_ACCESS~<uuid>"
// — is returned unchanged, since those are correct as pasted.
func NormalizeStatusEndpointID(id string) string {
	id = strings.TrimSpace(id)
	if !hexNoDashesRe.MatchString(id) {
		return id
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", id[0:8], id[8:12], id[12:16], id[16:20], id[20:32])
}

// StuckSyncRow describes one source_object_sync row stuck in SYNCING that
// needs to be force-failed. ObjectTypeID accepts any of the three forms the
// endpoint's path param accepts (GldObjectTypeDO UUID, GldObjectTagConfigDO
// UUID, or a PermissionEntityType literal) — hex-without-dashes ids get
// normalized, everything else passes through as-is.
type StuckSyncRow struct {
	ObjectTypeID string `json:"objectTypeId"`
	SyncNumber   int    `json:"syncNumber"`
	StatusReason string `json:"statusReason"`
	// DependentObjectTypeIDs are other open rows to fail in the same call
	// (same three id forms as ObjectTypeID). Optional — a separate
	// StuckSyncRow per dependent works just as well and is often simpler.
	DependentObjectTypeIDs []string `json:"dependentObjectTypeIds,omitempty"`
	ErrorDetails           string   `json:"errorDetails,omitempty"`
}

// BuildStuckSyncCurls builds one PUT curl per row that forces it to FAILED.
// credentials is a "user:token" (or "user:password" for prod, per cni-cli's
// stored shape) string embedded verbatim via -u; empty falls back to an
// env-appropriate placeholder, same convention as BuildCurls.
func BuildStuckSyncCurls(env, sourceID string, rows []StuckSyncRow, credentials string) ([]CurlStep, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, fmt.Errorf("sourceId is required")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("at least one stuck row is required")
	}

	apiBase := apiBaseForEnv(env)
	cred := strings.TrimSpace(credentials)
	if cred == "" {
		if env == "prod" {
			cred = "user:password"
		} else {
			cred = "user:token"
		}
	}
	normSource := url.PathEscape(NormalizeStatusEndpointID(sourceID))

	steps := make([]CurlStep, 0, len(rows))
	for i, row := range rows {
		objectTypeID := strings.TrimSpace(row.ObjectTypeID)
		if objectTypeID == "" {
			return nil, fmt.Errorf("row %d: objectTypeId is required", i+1)
		}
		if row.SyncNumber < 0 {
			return nil, fmt.Errorf("row %d: syncNumber must be >= 0", i+1)
		}
		if !stuckSyncStatusReasonSet[row.StatusReason] {
			return nil, fmt.Errorf("row %d: statusReason must be one of %s", i+1, strings.Join(StuckSyncStatusReasons, ", "))
		}

		body := map[string]any{
			"statusAction": "FAIL",
			"statusReason": row.StatusReason,
			"syncNumber":   row.SyncNumber,
		}
		if len(row.DependentObjectTypeIDs) > 0 {
			deps := make([]string, 0, len(row.DependentObjectTypeIDs))
			for _, d := range row.DependentObjectTypeIDs {
				d = strings.TrimSpace(d)
				if d == "" {
					continue
				}
				deps = append(deps, NormalizeStatusEndpointID(d))
			}
			if len(deps) > 0 {
				body["dependentObjectTypeIds"] = deps
			}
		}
		if details := strings.TrimSpace(row.ErrorDetails); details != "" {
			body["errorDetails"] = details
		}

		raw, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, err
		}

		endpoint := fmt.Sprintf("%s/api/v1/admin/sources/%s/types/%s/status",
			apiBase, normSource, url.PathEscape(NormalizeStatusEndpointID(objectTypeID)))

		steps = append(steps, CurlStep{
			Label:       fmt.Sprintf("Fail stuck row %d — %s", i+1, row.ObjectTypeID),
			Description: fmt.Sprintf("Forces this row to FAILED (syncNumber %d, reason %s) so the next trigger isn't blocked by a 409.", row.SyncNumber, row.StatusReason),
			Curl:        fmt.Sprintf("curl -u %s %s -H 'content-type:application/json' -D - -X PUT -d '%s'", cred, endpoint, raw),
		})
	}
	return steps, nil
}
