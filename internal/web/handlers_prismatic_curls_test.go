package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

const sampleCreateSourceTS = `
export const createSource = flow({
  inputs: {
    sourceDefinitionType: { value: "jira-cloud" },
    sourceName: { value: "Jira Cloud" },
    nativePermission: { value: false },
  },
})
`

// writePrismaticCurlsFixture extends writePrismaticFixtureIntegration with a
// createSource.ts, so ExtractSourceDefInfo has something real to parse.
func writePrismaticCurlsFixture(t *testing.T, root, dir string) {
	t.Helper()
	writePrismaticFixtureIntegration(t, root, dir)
	path := filepath.Join(root, dir, "src", "flows", "createSource.ts")
	if err := os.WriteFile(path, []byte(sampleCreateSourceTS), 0o644); err != nil {
		t.Fatalf("write createSource.ts: %v", err)
	}
}

func newCurlsServer(t *testing.T, root, integrationDir string) *Server {
	t.Helper()
	sess := &MenuSession{ID: "s1", Title: integrationDir, Tool: "claude",
		ProjectPath: filepath.Join(root, integrationDir), Status: session.StatusRunning}
	snapshot := &MenuSnapshot{Items: []MenuItem{{Type: MenuItemTypeSession, Session: sess}}}
	return NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: snapshot}})
}

func doCurlsRequest(t *testing.T, srv *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/prismatic/curls", &buf)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismaticCurls(rr, req)
	return rr
}

func TestSessionPrismaticCurls_ManualIpaasIdProducesQACurls(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-qa-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration") // FindRoot needs >=2

	if err := prismatic.SetGuruCreds("qa", "quser:qatoken"); err != nil {
		t.Fatalf("SetGuruCreds: %v", err)
	}
	srv := newCurlsServer(t, root, "curls-qa-integration")

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{
		IntegrationDir: "curls-qa-integration",
		Env:            "qa",
		Category:       "Wiki/KB",
		IpaasID:        "manual-ipaas-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticCurlsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NeedsInput != "" {
		t.Fatalf("NeedsInput = %q, want empty (manual ipaasId given), reason=%q", resp.NeedsInput, resp.Reason)
	}
	if resp.Ipaas == nil || resp.Ipaas.Source != "manual" || resp.Ipaas.ID != "manual-ipaas-1" {
		t.Fatalf("Ipaas = %+v, want manual/manual-ipaas-1", resp.Ipaas)
	}
	if len(resp.Curls) != 1 {
		t.Fatalf("len(Curls) = %d, want 1 for qa", len(resp.Curls))
	}
	curl := resp.Curls[0].Curl
	if !bytesContain([]byte(curl), "quser:qatoken") {
		t.Errorf("curl should embed the stored Guru credentials, got: %s", curl)
	}
	if !bytesContain([]byte(curl), "manual-ipaas-1") {
		t.Errorf("curl should embed the manual ipaasId, got: %s", curl)
	}
	if resp.Info.SourceName != "Jira Cloud" {
		t.Errorf("Info.SourceName = %q, want Jira Cloud", resp.Info.SourceName)
	}
}

func TestSessionPrismaticCurls_CachedMatchSkipsPrismOnSecondCall(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-cache-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration-2")
	srv := newCurlsServer(t, root, "curls-cache-integration")

	// First call seeds the cache via the manual override.
	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{
		IntegrationDir: "curls-cache-integration", Env: "prod", Category: "CRM", IpaasID: "cached-id-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed call status = %d, body=%s", rr.Code, rr.Body.String())
	}

	// Second call, no ipaasId and no Prism token configured (so it would
	// otherwise need to fall back to manual) — the cache should still
	// resolve it without ever shelling out to `prism`.
	rr = doCurlsRequest(t, srv, prismaticCurlsRequest{
		IntegrationDir: "curls-cache-integration", Env: "prod", Category: "CRM",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("cached call status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticCurlsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Ipaas == nil || resp.Ipaas.Source != "cache" || resp.Ipaas.ID != "cached-id-1" {
		t.Fatalf("Ipaas = %+v, want cache/cached-id-1", resp.Ipaas)
	}
	if len(resp.Curls) != 4 {
		t.Fatalf("len(Curls) = %d, want 4 for prod rollout dance", len(resp.Curls))
	}
}

func TestSessionPrismaticCurls_NoTokenAndNoManualIdNeedsManualInput(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-notoken-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration-3")
	srv := newCurlsServer(t, root, "curls-notoken-integration")

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{
		IntegrationDir: "curls-notoken-integration", Env: "qa", Category: "Other",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (needsInput, not an error), body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticCurlsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NeedsInput != "manual" {
		t.Fatalf("NeedsInput = %q, want manual (no Prism token, no override)", resp.NeedsInput)
	}
	if len(resp.Curls) != 0 {
		t.Errorf("Curls should be empty when NeedsInput is set, got %+v", resp.Curls)
	}
	if resp.Reason == "" {
		t.Error("Reason should explain why manual input is needed")
	}
}

func TestSessionPrismaticCurls_UnknownIntegrationDirReturns404(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-known-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration-4")
	srv := newCurlsServer(t, root, "curls-known-integration")

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{
		IntegrationDir: "does-not-exist", Env: "qa", Category: "Other",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an integrationDir that isn't in the monorepo, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticCurls_NonPrismaticProjectReturns404(t *testing.T) {
	resetPrismaticCredentials(t)
	dir := t.TempDir() // empty, not a monorepo at all
	sess := &MenuSession{ID: "s1", ProjectPath: dir, Status: session.StatusRunning}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{Items: []MenuItem{{Type: MenuItemTypeSession, Session: sess}}}}})

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{IntegrationDir: "x", Env: "qa", Category: "Other"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-Prismatic project, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticCurls_MissingCategoryIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-badreq-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration-5")
	srv := newCurlsServer(t, root, "curls-badreq-integration")

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{IntegrationDir: "curls-badreq-integration", Env: "qa"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing category, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticCurls_InvalidEnvIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "curls-badenv-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-integration-6")
	srv := newCurlsServer(t, root, "curls-badenv-integration")

	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{IntegrationDir: "curls-badenv-integration", Env: "staging", Category: "Other"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid env, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticCurls_RequiresAuth(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		MenuData:   &fakeMenuData{snapshot: &MenuSnapshot{}},
	})
	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{IntegrationDir: "x", Env: "qa", Category: "Other"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticCurls_MutationsDisabledRejects(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: false,
		MenuData:     &fakeMenuData{snapshot: &MenuSnapshot{}},
	})
	rr := doCurlsRequest(t, srv, prismaticCurlsRequest{IntegrationDir: "x", Env: "qa", Category: "Other"})
	if rr.Code == http.StatusOK {
		t.Fatalf("POST succeeded with WebMutations=false, want it rejected (body=%s)", rr.Body.String())
	}
}

func TestSessionPrismaticCurls_RejectsNonPOST(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/prismatic/curls", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismaticCurls(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 for GET, body=%s", rr.Code, rr.Body.String())
	}
}
