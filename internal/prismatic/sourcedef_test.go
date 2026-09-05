package prismatic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCreateSource(t *testing.T, root, integrationDir, body string) {
	t.Helper()
	dir := filepath.Join(root, integrationDir, "src", "flows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "createSource.ts"), []byte(body), 0o644); err != nil {
		t.Fatalf("write createSource.ts: %v", err)
	}
}

const sampleCreateSource = `
export const createSource = flow({
  onExecution: async (context, params) => {
    return { userMessage: "ok" }
  },
  inputs: {
    sourceDefinitionType: { value: "jira-cloud" },
    sourceName: { value: "Jira Cloud" },
    nativePermission: { value: true },
  },
})
`

func TestExtractSourceDefInfo_ParsesAllThreeFields(t *testing.T) {
	root := t.TempDir()
	writeCreateSource(t, root, "jira-integration", sampleCreateSource)

	info, err := ExtractSourceDefInfo(root, "jira-integration")
	if err != nil {
		t.Fatalf("ExtractSourceDefInfo: %v", err)
	}
	if info.SourceDefinitionType != "jira-cloud" {
		t.Errorf("SourceDefinitionType = %q, want jira-cloud", info.SourceDefinitionType)
	}
	if info.SourceName != "Jira Cloud" {
		t.Errorf("SourceName = %q, want Jira Cloud", info.SourceName)
	}
	if !info.NativePermission {
		t.Errorf("NativePermission = false, want true")
	}
}

func TestExtractSourceDefInfo_NativePermissionDefaultsFalseWhenAbsent(t *testing.T) {
	root := t.TempDir()
	body := `sourceDefinitionType: { value: "confluence-cloud" },
sourceName: { value: "Confluence Cloud" },`
	writeCreateSource(t, root, "confluence-integration", body)

	info, err := ExtractSourceDefInfo(root, "confluence-integration")
	if err != nil {
		t.Fatalf("ExtractSourceDefInfo: %v", err)
	}
	if info.NativePermission {
		t.Errorf("NativePermission = true, want false (field absent from source)")
	}
}

func TestExtractSourceDefInfo_MissingFileReturnsError(t *testing.T) {
	root := t.TempDir()
	if _, err := ExtractSourceDefInfo(root, "no-such-integration"); err == nil {
		t.Fatal("expected an error for a missing createSource.ts, got nil")
	}
}

func TestExtractSourceDefInfo_MissingRequiredFieldReturnsError(t *testing.T) {
	root := t.TempDir()
	writeCreateSource(t, root, "broken-integration", `sourceName: { value: "Only Name" },`)
	if _, err := ExtractSourceDefInfo(root, "broken-integration"); err == nil {
		t.Fatal("expected an error when sourceDefinitionType is missing, got nil")
	}
}

func TestBuildCurls_QAProducesOneGeneralCreateCurl(t *testing.T) {
	info := SourceDefInfo{SourceDefinitionType: "jira-cloud", SourceName: "Jira Cloud", NativePermission: true}
	steps, err := BuildCurls(info, "qa", "ipaas-123", "Ticketing/Project Management", "qauser:qatoken", nil, "", "")
	if err != nil {
		t.Fatalf("BuildCurls: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1 for qa", len(steps))
	}
	curl := steps[0].Curl
	for _, want := range []string{
		"-u qauser:qatoken",
		"https://qaapi.getguru.com/api/v1/admin/sources/definitions",
		`"availability": "GENERAL"`,
		`"ipaasIntegrationId": "ipaas-123"`,
		`"supportsSourceNativePermissions": true`,
	} {
		if !strings.Contains(curl, want) {
			t.Errorf("qa curl missing %q:\n%s", want, curl)
		}
	}
}

func TestBuildCurls_ProdProducesFourStepDanceWithDefaultTeam(t *testing.T) {
	info := SourceDefInfo{SourceDefinitionType: "jira-cloud", SourceName: "Jira Cloud"}
	steps, err := BuildCurls(info, "prod", "ipaas-456", "Ticketing/Project Management", "", nil, "", "")
	if err != nil {
		t.Fatalf("BuildCurls: %v", err)
	}
	// create + 1 assign + 1 remove + promote = 4, using the fallback team.
	if len(steps) != 4 {
		t.Fatalf("len(steps) = %d, want 4 for prod with default team, got labels: %v", len(steps), labels(steps))
	}
	if !strings.Contains(steps[0].Curl, `"availability": "TEAM"`) {
		t.Errorf("step 1 should create TEAM-scoped, got: %s", steps[0].Curl)
	}
	if !strings.Contains(steps[1].Curl, "/teams/"+GuruTestTeamID) || !strings.Contains(steps[1].Curl, "-X PUT") {
		t.Errorf("step 2 should PUT-assign the default test team, got: %s", steps[1].Curl)
	}
	if !strings.Contains(steps[2].Curl, "-X DELETE") {
		t.Errorf("step 3 should DELETE-remove the team, got: %s", steps[2].Curl)
	}
	if !strings.Contains(steps[3].Curl, `"availability": "GENERAL"`) {
		t.Errorf("step 4 should promote to GENERAL, got: %s", steps[3].Curl)
	}
	// No stored credentials -> prod placeholder, not the qa one.
	if !strings.Contains(steps[0].Curl, "-u user:password") {
		t.Errorf("expected the prod placeholder credential, got: %s", steps[0].Curl)
	}
}

func TestBuildCurls_ProdWithMultipleTeamsGeneratesOneAssignRemovePerTeam(t *testing.T) {
	info := SourceDefInfo{SourceDefinitionType: "jira-cloud", SourceName: "Jira Cloud"}
	teams := []Team{
		{ID: "team-a", Name: "Team A"},
		{ID: "team-b", Name: "Team B"},
	}
	steps, err := BuildCurls(info, "prod", "ipaas-789", "CRM", "u:t", teams, "", "")
	if err != nil {
		t.Fatalf("BuildCurls: %v", err)
	}
	// create + 2 assign + 2 remove + promote = 6
	if len(steps) != 6 {
		t.Fatalf("len(steps) = %d, want 6 for two teams, got labels: %v", len(steps), labels(steps))
	}
	if !strings.Contains(steps[1].Curl, "team-a") || !strings.Contains(steps[2].Curl, "team-b") {
		t.Errorf("expected both team assign curls in order, got: %v", labels(steps))
	}
	if !strings.Contains(steps[3].Curl, "team-a") || !strings.Contains(steps[4].Curl, "team-b") {
		t.Errorf("expected both team remove curls in order, got: %v", labels(steps))
	}
}

func TestBuildCurls_NameOverrideAlsoDerivesDefaultIconURL(t *testing.T) {
	info := SourceDefInfo{SourceDefinitionType: "jira-cloud", SourceName: "JIRA ISSUES ALL CAPS"}
	steps, err := BuildCurls(info, "qa", "ipaas-1", "CRM", "", nil, "Jira Issues", "")
	if err != nil {
		t.Fatalf("BuildCurls: %v", err)
	}
	curl := steps[0].Curl
	if !strings.Contains(curl, `"name": "Jira Issues"`) {
		t.Errorf("curl should use the overridden name, got: %s", curl)
	}
	if strings.Contains(curl, "JIRA ISSUES ALL CAPS") {
		t.Errorf("curl should not contain the un-overridden extracted name, got: %s", curl)
	}
	if !strings.Contains(curl, "source-icons/jira_issues.png") {
		t.Errorf("icon URL should be derived from the overridden name, got: %s", curl)
	}
}

func TestBuildCurls_IconURLOverrideWins(t *testing.T) {
	info := SourceDefInfo{SourceDefinitionType: "jira-cloud", SourceName: "Jira Issues"}
	steps, err := BuildCurls(info, "qa", "ipaas-1", "CRM", "", nil, "", "https://example.com/custom-icon.png")
	if err != nil {
		t.Fatalf("BuildCurls: %v", err)
	}
	if !strings.Contains(steps[0].Curl, `"iconUrl": "https://example.com/custom-icon.png"`) {
		t.Errorf("curl should use the overridden icon URL, got: %s", steps[0].Curl)
	}
}

func labels(steps []CurlStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Label
	}
	return out
}
