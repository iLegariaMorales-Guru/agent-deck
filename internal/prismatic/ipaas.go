package prismatic

// Resolves a CNI integration's Prismatic ipaasIntegrationId by shelling out
// to the `prism` CLI, same as cni-cli's lib/prismFetch.js. Only used when
// the on-disk cache (ipaascache.go) has no fresh entry — the curl wizard's
// happy path never touches the network or a subprocess at all.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const prismListTimeout = 30 * time.Second

// PrismaticURL is Guru's single Prismatic instance — same URL for QA and
// Prod; the refresh token is what actually scopes the session.
const PrismaticURL = "https://integrations.getguru.com/"

// ErrPrismTokenNotConfigured is returned by ResolveIpaasID when no Prism
// refresh token is stored for env — the caller should fall back to letting
// the user paste the ipaasIntegrationId in by hand.
var ErrPrismTokenNotConfigured = fmt.Errorf("no Prism refresh token configured for this environment")

// rawIntegration is one entry of `prism integrations:list --output json`.
type rawIntegration struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	VersionNumber any    `json:"versionNumber"`
	Description   string `json:"description"`
}

func parseIntegrationsJSON(stdout []byte) ([]IpaasMatch, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, nil
	}

	// prism's JSON output is either a bare array or an object with a
	// results/integrations array — try the bare-array case first (the
	// common one), then unwrap.
	var arr []rawIntegration
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		var wrapped struct {
			Results      []rawIntegration `json:"results"`
			Integrations []rawIntegration `json:"integrations"`
		}
		if err2 := json.Unmarshal(trimmed, &wrapped); err2 != nil {
			return nil, fmt.Errorf("parse prism JSON: %w", err)
		}
		arr = wrapped.Results
		if arr == nil {
			arr = wrapped.Integrations
		}
	}

	matches := make([]IpaasMatch, 0, len(arr))
	for _, x := range arr {
		if x.ID == "" || x.Name == "" {
			continue
		}
		var ver int
		switch v := x.VersionNumber.(type) {
		case float64:
			ver = int(v)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				ver = n
			}
		}
		matches = append(matches, IpaasMatch{ID: x.ID, Name: x.Name, VersionNumber: ver})
	}
	return matches, nil
}

// listIntegrations runs `prism integrations:list --extended --output json
// [--search term]` with PRISMATIC_URL/PRISM_REFRESH_TOKEN set for env.
// --extended is required for prism to include the id column at all — the
// default projection omits it.
func listIntegrations(ctx context.Context, env, search, token string) ([]IpaasMatch, error) {
	args := []string{"integrations:list", "--extended", "--output", "json"}
	if strings.TrimSpace(search) != "" {
		args = append(args, "--search", strings.TrimSpace(search))
	}

	ctx, cancel := context.WithTimeout(ctx, prismListTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "prism", args...)
	cmd.Env = append(cmd.Environ(), "PRISMATIC_URL="+PrismaticURL, "PRISM_REFRESH_TOKEN="+token)
	cmd.Stdin = nil

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("prism integrations:list timed out after %s", prismListTimeout)
		}
		tail := strings.TrimSpace(stderr.String())
		if tail != "" {
			return nil, fmt.Errorf("prism integrations:list failed: %s", tail)
		}
		return nil, fmt.Errorf("prism integrations:list failed: %w", err)
	}
	return parseIntegrationsJSON(stdout.Bytes())
}

// IpaasResolution is the outcome of ResolveIpaasID.
type IpaasResolution struct {
	Match   *IpaasMatch  // set when there's exactly one confident match
	Options []IpaasMatch // set instead of Match when the search was ambiguous
}

// ResolveIpaasID finds the Prismatic integration matching candidates (name
// variants to try, in preference order) by calling `prism
// integrations:list --search <candidate>` for each until one returns
// results, then scoring: exact name match > prefix > substring. Mirrors
// cni-cli's findIntegrationByName exactly, including its "results.length
// > 1" ambiguity fallback.
func ResolveIpaasID(ctx context.Context, env string, candidates []string) (IpaasResolution, error) {
	token, err := GetPrismToken(env)
	if err != nil {
		return IpaasResolution{}, err
	}
	if token == "" {
		return IpaasResolution{}, ErrPrismTokenNotConfigured
	}

	seen := map[string]bool{}
	clean := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		clean = append(clean, c)
	}
	if len(clean) == 0 {
		return IpaasResolution{}, fmt.Errorf("no search candidates provided")
	}

	var options []IpaasMatch
	for _, term := range clean {
		results, err := listIntegrations(ctx, env, term, token)
		if err != nil {
			return IpaasResolution{}, err
		}
		if len(results) > 0 {
			options = results
			break
		}
	}
	if len(options) == 0 {
		return IpaasResolution{Options: []IpaasMatch{}}, nil
	}

	for _, candidate := range clean {
		lc := strings.ToLower(candidate)

		if m := filterMatches(options, func(o IpaasMatch) bool { return strings.ToLower(o.Name) == lc }); len(m) == 1 {
			return IpaasResolution{Match: &m[0], Options: options}, nil
		} else if len(m) > 1 {
			return IpaasResolution{Options: m}, nil
		}

		if m := filterMatches(options, func(o IpaasMatch) bool { return strings.HasPrefix(strings.ToLower(o.Name), lc) }); len(m) == 1 {
			return IpaasResolution{Match: &m[0], Options: options}, nil
		}

		if m := filterMatches(options, func(o IpaasMatch) bool { return strings.Contains(strings.ToLower(o.Name), lc) }); len(m) == 1 {
			return IpaasResolution{Match: &m[0], Options: options}, nil
		}
	}
	return IpaasResolution{Options: options}, nil
}

func filterMatches(options []IpaasMatch, keep func(IpaasMatch) bool) []IpaasMatch {
	out := make([]IpaasMatch, 0, len(options))
	for _, o := range options {
		if keep(o) {
			out = append(out, o)
		}
	}
	return out
}

// NameCandidates builds the search-term list ResolveIpaasID tries, in
// preference order: the extracted Guru source name first (most reliable),
// then a humanized version of the integration's directory name as a
// fallback for integrations whose source name diverges from their repo
// name.
func NameCandidates(info SourceDefInfo, integrationDir string) []string {
	humanized := strings.ReplaceAll(integrationDir, "-", " ")
	humanized = strings.TrimSuffix(humanized, " integration")
	humanized = strings.TrimSuffix(humanized, " Integration")
	return []string{info.SourceName, humanized}
}
