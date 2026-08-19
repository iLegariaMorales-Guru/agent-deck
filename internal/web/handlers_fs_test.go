package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFSBrowse_ListsSubdirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "project-a"))
	mustMkdir(t, filepath.Join(root, "project-b"))
	mustMkdir(t, filepath.Join(root, ".hidden"))
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/fs/browse?path="+root, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp FSBrowseResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Path != root {
		t.Errorf("Path = %q, want %q", resp.Path, root)
	}
	if resp.Parent != filepath.Dir(root) {
		t.Errorf("Parent = %q, want %q", resp.Parent, filepath.Dir(root))
	}
	names := make([]string, len(resp.Entries))
	for i, e := range resp.Entries {
		names[i] = e.Name
	}
	if len(names) != 2 || names[0] != "project-a" || names[1] != "project-b" {
		t.Fatalf("Entries = %v, want [project-a project-b] (files and dotdirs excluded)", names)
	}
}

func TestFSBrowse_DefaultsToHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/fs/browse", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp FSBrowseResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Path != home {
		t.Errorf("Path = %q, want home dir %q", resp.Path, home)
	}
}

func TestFSBrowse_RejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/fs/browse?path="+file, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFSBrowse_MethodNotAllowed(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/browse", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFSBrowse_Unauthorized(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", Token: "secret-token"})
	req := httptest.NewRequest(http.MethodGet, "/api/fs/browse", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
