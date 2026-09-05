package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

func doExistingRequest(t *testing.T, srv *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/prismatic/existing", &buf)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismaticExisting(rr, req)
	return rr
}

func TestSessionPrismaticExisting_NoCredsReturnsFoundFalseWithReason(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "existing-nocreds-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-existing-1")
	srv := newCurlsServer(t, root, "existing-nocreds-integration")

	rr := doExistingRequest(t, srv, prismaticExistingRequest{IntegrationDir: "existing-nocreds-integration", Env: "qa"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticExistingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Found {
		t.Fatalf("Found = true with no creds configured, want false")
	}
	if resp.Reason == "" {
		t.Error("Reason should explain that no creds are configured")
	}
}

func TestSessionPrismaticExisting_UnknownIntegrationDirReturns404(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "existing-known-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-existing-2")
	srv := newCurlsServer(t, root, "existing-known-integration")

	rr := doExistingRequest(t, srv, prismaticExistingRequest{IntegrationDir: "not-real", Env: "qa"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticExisting_InvalidEnvIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	root := t.TempDir()
	writePrismaticCurlsFixture(t, root, "existing-badenv-integration")
	writePrismaticFixtureIntegration(t, root, "sibling-existing-3")
	srv := newCurlsServer(t, root, "existing-badenv-integration")

	rr := doExistingRequest(t, srv, prismaticExistingRequest{IntegrationDir: "existing-badenv-integration", Env: "nope"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticExisting_RequiresAuth(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", Token: "secret-token", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doExistingRequest(t, srv, prismaticExistingRequest{IntegrationDir: "x", Env: "qa"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismaticExisting_MutationsDisabledRejects(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: false, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doExistingRequest(t, srv, prismaticExistingRequest{IntegrationDir: "x", Env: "qa"})
	if rr.Code == http.StatusOK {
		t.Fatalf("succeeded with WebMutations=false, want rejected (body=%s)", rr.Body.String())
	}
}

func TestSessionPrismaticExisting_RejectsNonPOST(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/prismatic/existing", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismaticExisting(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405, body=%s", rr.Code, rr.Body.String())
	}
}

// --- POST /api/prismatic/curls/update ---

func doUpdateCurlRequest(t *testing.T, srv *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/prismatic/curls/update", &buf)
	rr := httptest.NewRecorder()
	srv.handlePrismaticCurlsUpdate(rr, req)
	return rr
}

func TestPrismaticCurlsUpdate_BuildsSinglePutCurl(t *testing.T) {
	resetPrismaticCredentials(t)
	if err := prismatic.SetGuruCreds("prod", "produser:prodtoken"); err != nil {
		t.Fatalf("SetGuruCreds: %v", err)
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})

	rr := doUpdateCurlRequest(t, srv, prismaticUpdateCurlRequest{
		Env: "prod",
		Definition: map[string]any{
			"id":           "existing-id-should-be-stripped",
			"type":         "JIRA_ISSUES",
			"name":         "Jira Issues",
			"category":     "CRM",
			"availability": "GENERAL",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticUpdateCurlResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Curls) != 1 {
		t.Fatalf("len(Curls) = %d, want 1", len(resp.Curls))
	}
	curl := resp.Curls[0].Curl
	if !bytesContain([]byte(curl), "produser:prodtoken") {
		t.Errorf("curl should embed the stored prod Guru credentials, got: %s", curl)
	}
	if bytesContain([]byte(curl), "existing-id-should-be-stripped") {
		t.Errorf("curl should not include the id field, got: %s", curl)
	}
	if !bytesContain([]byte(curl), "-X PUT") {
		t.Errorf("expected a PUT curl, got: %s", curl)
	}
}

func TestPrismaticCurlsUpdate_MissingDefinitionIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doUpdateCurlRequest(t, srv, prismaticUpdateCurlRequest{Env: "qa"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing definition, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsUpdate_MissingTypeInDefinitionIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doUpdateCurlRequest(t, srv, prismaticUpdateCurlRequest{Env: "qa", Definition: map[string]any{"name": "no type"}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a definition with no type, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsUpdate_RequiresAuth(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", Token: "secret-token", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doUpdateCurlRequest(t, srv, prismaticUpdateCurlRequest{Env: "qa", Definition: map[string]any{"type": "X"}})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsUpdate_MutationsDisabledRejects(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: false, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doUpdateCurlRequest(t, srv, prismaticUpdateCurlRequest{Env: "qa", Definition: map[string]any{"type": "X"}})
	if rr.Code == http.StatusOK {
		t.Fatalf("succeeded with WebMutations=false, want rejected (body=%s)", rr.Body.String())
	}
}

func TestPrismaticCurlsUpdate_RejectsNonPOST(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodGet, "/api/prismatic/curls/update", nil)
	rr := httptest.NewRecorder()
	srv.handlePrismaticCurlsUpdate(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405, body=%s", rr.Code, rr.Body.String())
	}
}
