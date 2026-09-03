package prismatic

// Fetches an already-registered source definition from Guru's admin API so
// the curl wizard can offer "edit the existing one" instead of always
// starting from a blank create — a real gap cni-cli never covered (it's
// create-only). Read-only GET here; BuildUpdateCurl still only ever
// produces a curl for the user to run, same non-automation stance as the
// create/promote flow — agent-deck never issues the write itself.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrSourceDefNotFound means Guru's admin API returned 404 — the
// integration hasn't been registered as a source definition in this
// environment yet, not a failure.
var ErrSourceDefNotFound = errors.New("source definition not found")

const fetchExistingTimeout = 15 * time.Second

// FetchExistingSourceDefinition GETs
// /api/v1/admin/sources/definitions/{sourceDefinitionType} and returns the
// raw response as a generic map — deliberately not a typed struct, so
// fields this package doesn't know about (Guru's admin API has several:
// discriminatorType, modelNames, customDataStrategy, etc.) survive a
// round-trip through BuildUpdateCurl unmodified instead of being silently
// dropped.
func FetchExistingSourceDefinition(ctx context.Context, env, sourceDefType, credentials string) (map[string]any, error) {
	user, pass, ok := strings.Cut(strings.TrimSpace(credentials), ":")
	if !ok || user == "" {
		return nil, fmt.Errorf("Guru API credentials must be in user:token format")
	}

	endpoint := apiBaseForEnv(env) + "/api/v1/admin/sources/definitions/" + url.PathEscape(sourceDefType)
	ctx, cancel := context.WithTimeout(ctx, fetchExistingTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to Guru API failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSourceDefNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tail := strings.TrimSpace(string(body))
		if len(tail) > 300 {
			tail = tail[:300]
		}
		return nil, fmt.Errorf("Guru API returned %d: %s", resp.StatusCode, tail)
	}

	var def map[string]any
	if err := json.Unmarshal(body, &def); err != nil {
		return nil, fmt.Errorf("failed to parse Guru API response: %w", err)
	}
	return def, nil
}

// BuildUpdateCurl wraps an (already fetched-and-edited) source definition
// into a single PUT curl. definition's "type" field determines the target
// URL; "id" is stripped before marshaling since Guru's admin API doesn't
// accept it back on create/update bodies (matches the shape sourceDefBody
// already builds for create/promote).
func BuildUpdateCurl(definition map[string]any, env, credentials string) (CurlStep, error) {
	typ, _ := definition["type"].(string)
	if typ == "" {
		return CurlStep{}, fmt.Errorf(`definition is missing a non-empty "type" field`)
	}

	body := make(map[string]any, len(definition))
	for k, v := range definition {
		if k == "id" {
			continue
		}
		body[k] = v
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return CurlStep{}, err
	}

	cred := strings.TrimSpace(credentials)
	if cred == "" {
		if env == "prod" {
			cred = "user:password"
		} else {
			cred = "user:token"
		}
	}
	baseDefs := apiBaseForEnv(env) + "/api/v1/admin/sources/definitions"

	return CurlStep{
		Label:       "Update existing definition",
		Description: fmt.Sprintf("Applies your edits to the existing %q source definition.", typ),
		Curl:        fmt.Sprintf("curl -u %s %s/%s -H 'content-type:application/json' -D - -X PUT -d '%s'", cred, baseDefs, typ, string(raw)),
	}, nil
}
