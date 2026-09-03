package prismatic

// Source-definition curl generation — PR 3/4 of the Prismatic pane build.
//
// Mirrors guru-prismatic/cli's lib/sourceDef.js exactly: parse an
// integration's src/flows/createSource.ts for the three fields Guru's
// admin API needs (source_definition_type, source_name, native_permission),
// then build the paste-and-run curl sequence that registers the source
// definition — one curl for QA, a four-step TEAM-scope-then-promote dance
// for Prod (Guru requires new prod source definitions to go through a test
// team before GENERAL availability).
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GuruTestTeamID is Guru's internal test team, used as the default (and
// fallback) team for the Prod TEAM-scoped rollout dance.
const GuruTestTeamID = "d99f50f8-72f1-4d0f-a5bb-f14c2d6d9173"

// Team is one selectable team for the Prod rollout dance.
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestTeams are the teams selectable in the UI's team picker. The first
// entry is the default — same order and same two teams as cni-cli.
var TestTeams = []Team{
	{ID: GuruTestTeamID, Name: "Caroline Test Team"},
	{ID: "014dc5f6-9488-43fe-a892-206d276a7a9c", Name: "Guru HQ"},
}

// SourceDefCategories are the source-definition categories Guru's admin API
// accepts, in the order cni-cli presents them.
var SourceDefCategories = []string{
	"Other",
	"Wiki/KB",
	"Ticketing/Project Management",
	"CRM",
	"File Storage",
}

// SourceDefInfo is what's extracted from createSource.ts.
type SourceDefInfo struct {
	SourceDefinitionType string `json:"source_definition_type"`
	SourceName           string `json:"source_name"`
	NativePermission     bool   `json:"native_permission"`
}

var (
	sourceDefTypeRe = regexp.MustCompile(`sourceDefinitionType:\s*\{\s*value:\s*"([^"]+)"`)
	sourceNameRe    = regexp.MustCompile(`sourceName:\s*\{\s*value:\s*"([^"]+)"`)
	nativePermRe    = regexp.MustCompile(`nativePermission:\s*\{\s*value:\s*(true|false)`)
)

// ExtractSourceDefInfo reads <root>/<integrationDir>/src/flows/createSource.ts
// and pulls out sourceDefinitionType, sourceName, and nativePermission via
// the same regexes cni-cli uses — this is a small, stable applicationSpec
// literal in every CNI, not a file worth a full TypeScript parse for.
func ExtractSourceDefInfo(root, integrationDir string) (SourceDefInfo, error) {
	path := filepath.Join(root, integrationDir, "src", "flows", "createSource.ts")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SourceDefInfo{}, fmt.Errorf("createSource.ts not found at %s", path)
		}
		return SourceDefInfo{}, err
	}

	typeMatch := sourceDefTypeRe.FindSubmatch(content)
	if typeMatch == nil {
		return SourceDefInfo{}, fmt.Errorf("could not extract sourceDefinitionType from createSource.ts")
	}
	nameMatch := sourceNameRe.FindSubmatch(content)
	if nameMatch == nil {
		return SourceDefInfo{}, fmt.Errorf("could not extract sourceName from createSource.ts")
	}
	permMatch := nativePermRe.FindSubmatch(content)

	return SourceDefInfo{
		SourceDefinitionType: string(typeMatch[1]),
		SourceName:           string(nameMatch[1]),
		NativePermission:     permMatch != nil && string(permMatch[1]) == "true",
	}, nil
}

// CurlStep is one paste-and-run curl in the generated sequence.
type CurlStep struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Curl        string `json:"curl"`
}

// apiBaseForEnv returns Guru's admin API base for env — same host for both,
// just a qa/prod subdomain split.
func apiBaseForEnv(env string) string {
	if env == "prod" {
		return "https://api.getguru.com"
	}
	return "https://qaapi.getguru.com"
}

// sourceDefBody builds the JSON body for a create/promote request at the
// given availability ("TEAM" or "GENERAL").
func sourceDefBody(info SourceDefInfo, ipaasID, category, availability string) (string, error) {
	iconSlug := strings.ReplaceAll(strings.ToLower(info.SourceName), " ", "_")
	iconURL := fmt.Sprintf("https://assets.getguru.com/source-icons/%s.png", iconSlug)

	config := map[string]any{"ipaasIntegrationId": ipaasID}
	if info.NativePermission {
		config["supportsSourceNativePermissions"] = true
	}

	body := map[string]any{
		"configType":   "GURU_IPAAS",
		"name":         info.SourceName,
		"type":         info.SourceDefinitionType,
		"config":       config,
		"iconUrl":      iconURL,
		"availability": availability,
		"category":     category,
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// BuildCurls builds the curl sequence for env. credentials is a "user:token"
// string embedded verbatim via -u; an empty string falls back to an
// env-appropriate placeholder so the curl is still readable/copyable. teams
// is only consulted for prod — an empty slice falls back to GuruTestTeamID,
// same as cni-cli.
func BuildCurls(info SourceDefInfo, env, ipaasID, category, credentials string, teams []Team) ([]CurlStep, error) {
	apiBase := apiBaseForEnv(env)
	cred := strings.TrimSpace(credentials)
	if cred == "" {
		if env == "prod" {
			cred = "user:password"
		} else {
			cred = "user:token"
		}
	}
	baseDefs := apiBase + "/api/v1/admin/sources/definitions"

	if env != "prod" {
		generalBody, err := sourceDefBody(info, ipaasID, category, "GENERAL")
		if err != nil {
			return nil, err
		}
		return []CurlStep{
			{
				Label:       "Create (GENERAL)",
				Description: "Creates the source definition in QA available to all.",
				Curl:        fmt.Sprintf("curl -u %s %s -H 'content-type:application/json' -D - -X POST -d '%s'", cred, baseDefs, generalBody),
			},
		}, nil
	}

	teamBody, err := sourceDefBody(info, ipaasID, category, "TEAM")
	if err != nil {
		return nil, err
	}
	generalBody, err := sourceDefBody(info, ipaasID, category, "GENERAL")
	if err != nil {
		return nil, err
	}

	selectedTeams := teams
	if len(selectedTeams) == 0 {
		selectedTeams = []Team{{ID: GuruTestTeamID, Name: "test team"}}
	}

	steps := []CurlStep{
		{
			Label:       "1. Create (TEAM only)",
			Description: "Creates the source definition scoped to the test team(s) only.",
			Curl:        fmt.Sprintf("curl -u %s %s -H 'content-type:application/json' -D - -X POST -d '%s'", cred, baseDefs, teamBody),
		},
	}
	for _, team := range selectedTeams {
		steps = append(steps, CurlStep{
			Label:       fmt.Sprintf("2. Assign %s", team.Name),
			Description: fmt.Sprintf("Associates the source definition with %s.", team.Name),
			Curl:        fmt.Sprintf("curl -u %s %s/%s/teams/%s -X PUT -D -", cred, baseDefs, info.SourceDefinitionType, team.ID),
		})
	}
	for _, team := range selectedTeams {
		steps = append(steps, CurlStep{
			Label:       fmt.Sprintf("3. Remove %s", team.Name),
			Description: fmt.Sprintf("After testing, removes %s before going GENERAL.", team.Name),
			Curl:        fmt.Sprintf("curl -u %s -X DELETE %s/%s/teams/%s -D -", cred, baseDefs, info.SourceDefinitionType, team.ID),
		})
	}
	steps = append(steps, CurlStep{
		Label:       "4. Set GENERAL availability",
		Description: "Makes the source definition available to all customers.",
		Curl:        fmt.Sprintf("curl -u %s %s/%s -H 'content-type:application/json' -D - -X PUT -d '%s'", cred, baseDefs, info.SourceDefinitionType, generalBody),
	})
	return steps, nil
}
