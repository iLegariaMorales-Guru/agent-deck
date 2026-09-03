package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func writePrismaticFixtureIntegration(t *testing.T, root, dir string) {
	t.Helper()
	base := filepath.Join(root, dir, "src", "flows")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pkg := `{"name":"` + dir + `","dependencies":{"@prismatic-io/spectral":"^9.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "sync.ts"), []byte("export {}"), 0o644); err != nil {
		t.Fatalf("write flow: %v", err)
	}
}

func newPrismaticServer(t *testing.T, ms *MenuSession) *Server {
	t.Helper()
	snapshot := &MenuSnapshot{Items: []MenuItem{{Type: MenuItemTypeSession, Session: ms}}}
	return NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})
}

func TestSessionPrismatic_DetectsMonorepoAndListsIntegrations(t *testing.T) {
	root := t.TempDir()
	writePrismaticFixtureIntegration(t, root, "jira-integration")
	writePrismaticFixtureIntegration(t, root, "confluence")

	// Session opened one level deep, same as a real "cd jira-integration && claude".
	sess := &MenuSession{ID: "s1", Title: "jira-integration", Tool: "claude",
		ProjectPath: filepath.Join(root, "jira-integration"), Status: session.StatusRunning}
	srv := newPrismaticServer(t, sess)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/prismatic", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismatic(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Supported {
		t.Fatalf("Supported = false, want true (body=%s)", rr.Body.String())
	}
	if resp.Root != root {
		t.Errorf("Root = %q, want %q", resp.Root, root)
	}
	if len(resp.Integrations) != 2 {
		t.Errorf("Integrations = %+v, want 2 entries", resp.Integrations)
	}
}

func TestSessionPrismatic_NonPrismaticProjectReportsUnsupported(t *testing.T) {
	dir := t.TempDir() // empty -- not a Prismatic checkout at all
	sess := &MenuSession{ID: "s1", Title: "plain-repo", Tool: "claude",
		ProjectPath: dir, Status: session.StatusRunning}
	srv := newPrismaticServer(t, sess)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/prismatic", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismatic(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Supported {
		t.Fatalf("Supported = true for a plain non-Prismatic dir, want false")
	}
	if resp.Root != "" || len(resp.Integrations) != 0 {
		t.Errorf("expected empty Root/Integrations when unsupported, got %+v", resp)
	}
}

func TestSessionPrismatic_UnknownSessionReturns404(t *testing.T) {
	srv := newPrismaticServer(t, &MenuSession{ID: "other", ProjectPath: t.TempDir(), Status: session.StatusRunning})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/missing/prismatic", nil)
	req.SetPathValue("id", "missing")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismatic(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismatic_RequiresAuth(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		MenuData:   &fakeMenuData{snapshot: &MenuSnapshot{}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/prismatic", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismatic(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionPrismatic_RejectsNonGET(t *testing.T) {
	srv := newPrismaticServer(t, &MenuSession{ID: "s1", ProjectPath: t.TempDir(), Status: session.StatusRunning})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/prismatic", nil)
	req.SetPathValue("id", "s1")
	rr := httptest.NewRecorder()
	srv.handleSessionPrismatic(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 for POST, body=%s", rr.Code, rr.Body.String())
	}
}
