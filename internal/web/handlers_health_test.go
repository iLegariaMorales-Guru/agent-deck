package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/git"
)

// runGitCmd is a tiny local helper (internal/git's test helpers of the same
// name are unexported to that package) for setting up real repos/worktrees
// in these handler tests.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitCmd(t, dir, "-c", "init.defaultBranch=main", "init")
	runGitCmd(t, dir, "config", "user.email", "test@test.com")
	runGitCmd(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")
	return dir
}

func TestSessionHealthBatch_DirtyWorktreeFlagged(t *testing.T) {
	repo := initTestGitRepo(t)
	runGitCmd(t, repo, "branch", "feature-x")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := git.CreateWorktree(repo, worktreePath, "feature-x"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "scratch.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("write scratch file: %v", err)
	}

	snapshot := &MenuSnapshot{Items: []MenuItem{
		{Type: MenuItemTypeSession, Session: &MenuSession{
			ID:               "dirty-sess",
			Tool:             "claude",
			ProjectPath:      repo,
			WorktreePath:     worktreePath,
			WorktreeRepoRoot: repo,
			WorktreeBranch:   "feature-x",
		}},
	}}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/health/batch?ids=dirty-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Health map[string]git.WorktreeHealth `json:"health"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	h, ok := resp.Health["dirty-sess"]
	if !ok {
		t.Fatalf("expected dirty-sess in health map, got %+v", resp.Health)
	}
	if !h.UncommittedChanges {
		t.Errorf("UncommittedChanges = false, want true: %+v", h)
	}
	if h.WorktreeMissing {
		t.Errorf("WorktreeMissing = true, want false: %+v", h)
	}
}

func TestSessionHealthBatch_MissingWorktreeFlagged(t *testing.T) {
	repo := initTestGitRepo(t)
	runGitCmd(t, repo, "branch", "feature-gone")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := git.CreateWorktree(repo, worktreePath, "feature-gone"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	snapshot := &MenuSnapshot{Items: []MenuItem{
		{Type: MenuItemTypeSession, Session: &MenuSession{
			ID:               "gone-sess",
			Tool:             "claude",
			ProjectPath:      repo,
			WorktreePath:     worktreePath,
			WorktreeRepoRoot: repo,
			WorktreeBranch:   "feature-gone",
		}},
	}}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/health/batch?ids=gone-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp struct {
		Health map[string]git.WorktreeHealth `json:"health"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	h, ok := resp.Health["gone-sess"]
	if !ok || !h.WorktreeMissing {
		t.Errorf("expected WorktreeMissing=true for gone-sess, got %+v (present=%v)", h, ok)
	}
}

func TestSessionHealthBatch_CleanSessionAbsent(t *testing.T) {
	repo := initTestGitRepo(t)

	snapshot := &MenuSnapshot{Items: []MenuItem{
		{Type: MenuItemTypeSession, Session: &MenuSession{
			ID:          "clean-sess",
			Tool:        "claude",
			ProjectPath: repo,
			Branch:      "main",
		}},
	}}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/health/batch?ids=clean-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp struct {
		Health map[string]git.WorktreeHealth `json:"health"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp.Health["clean-sess"]; ok {
		t.Errorf("expected clean-sess absent from health map (nothing to badge), got %+v", resp.Health["clean-sess"])
	}
}

func TestSessionHealthBatch_SSHSessionSkipped(t *testing.T) {
	snapshot := &MenuSnapshot{Items: []MenuItem{
		{Type: MenuItemTypeSession, Session: &MenuSession{
			ID:          "ssh-sess",
			Tool:        "claude",
			ProjectPath: "/some/remote/path",
			SSHHost:     "remote-box",
		}},
	}}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/health/batch?ids=ssh-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp struct {
		Health map[string]git.WorktreeHealth `json:"health"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp.Health["ssh-sess"]; ok {
		t.Errorf("expected ssh-sess absent (not checkable locally), got %+v", resp.Health["ssh-sess"])
	}
}

func TestSessionHealthBatch_EmptyIDs(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/health/batch", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestSessionHealthBatch_MethodNotAllowed(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/health/batch", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}
