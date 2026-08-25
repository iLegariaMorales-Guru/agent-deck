package web

import (
	"bytes"
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
	if resp.IsHome {
		t.Error("IsHome = true for an ordinary tempdir, want false")
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
	// #issue: CreateSessionDialog.js's FolderBrowser relies on this flag to
	// keep "Use this folder" disabled on the browser's default (home-dir)
	// listing -- confirming the bare home dir as a session's working
	// directory reliably kills the launched tool within ~300ms of spawn.
	if !resp.IsHome {
		t.Error("IsHome = false for the default (no-path) listing, want true")
	}
}

func TestFSBrowse_ExplicitHomePathAlsoMarksIsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/fs/browse?path="+home, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp FSBrowseResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.IsHome {
		t.Error("IsHome = false when explicitly navigating to the home dir, want true")
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

// #issue: lets the New Session dialog's folder browser start a brand-new
// project from a blank directory (the natural next step once the home-dir
// crash fix stops it from pointing a session straight at $HOME) instead of
// forcing a trip out to a terminal first.
func TestFSMkdir_CreatesFolderUnderParent(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})

	payload, err := json.Marshal(map[string]string{"parentPath": root, "name": "my-new-project"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp FSMkdirResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantPath := filepath.Join(root, "my-new-project")
	if resp.Path != wantPath {
		t.Errorf("Path = %q, want %q", resp.Path, wantPath)
	}
	info, err := os.Stat(wantPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("folder was not actually created on disk: %v", err)
	}
}

func TestFSMkdir_RejectsPathSeparatorInName(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})

	payload, _ := json.Marshal(map[string]string{"parentPath": root, "name": "../escape"})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Fatal("mkdir escaped the parent directory")
	}
}

func TestFSMkdir_ConflictWhenAlreadyExists(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "taken"))
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true})

	payload, _ := json.Marshal(map[string]string{"parentPath": root, "name": "taken"})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFSMkdir_MutationsDisabled(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: false})

	payload, _ := json.Marshal(map[string]string{"parentPath": root, "name": "nope"})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "nope")); err == nil {
		t.Fatal("folder was created despite mutations being disabled")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
