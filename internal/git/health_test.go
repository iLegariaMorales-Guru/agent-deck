package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckWorktreeHealth_WorktreeMissing(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "feature-a")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "feature-a"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	h := CheckWorktreeHealth(worktreePath, "feature-a", true)
	if !h.WorktreeMissing {
		t.Errorf("WorktreeMissing = false, want true")
	}
	if h.UncommittedChanges || h.Ahead != 0 || h.Behind != 0 || h.UpstreamGone {
		t.Errorf("expected only WorktreeMissing set, got %+v", h)
	}
}

func TestCheckWorktreeHealth_CleanWorktree(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "feature-clean")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "feature-clean"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	h := CheckWorktreeHealth(worktreePath, "feature-clean", true)
	if !h.IsClean() {
		t.Errorf("expected clean health, got %+v", h)
	}
}

func TestCheckWorktreeHealth_UncommittedChanges(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "feature-dirty")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "feature-dirty"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	// Modify a TRACKED file (README.md, committed by createTestRepo) without
	// committing — this is the real signal UncommittedChanges is meant to
	// catch.
	if err := os.WriteFile(filepath.Join(worktreePath, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}

	h := CheckWorktreeHealth(worktreePath, "feature-dirty", true)
	if !h.UncommittedChanges {
		t.Errorf("UncommittedChanges = false, want true")
	}
}

// TestCheckWorktreeHealth_UntrackedFilesIgnored is the regression test for a
// real false-positive hit live: a repo often has untracked scratch/local
// files that are never meant to be committed (and aren't gitignored
// either). Counting those made the badge permanently "dirty" even right
// after a commit — this pins that untracked-only changes must NOT flip
// UncommittedChanges.
func TestCheckWorktreeHealth_UntrackedFilesIgnored(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "feature-untracked")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "feature-untracked"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "scratch.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("write untracked scratch file: %v", err)
	}

	h := CheckWorktreeHealth(worktreePath, "feature-untracked", true)
	if h.UncommittedChanges {
		t.Errorf("UncommittedChanges = true, want false (untracked-only files must not flag)")
	}
}

func TestCheckWorktreeHealth_AheadBehind(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "feature-diverge")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "feature-diverge"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// One new commit on the branch (ahead)...
	if err := os.WriteFile(filepath.Join(worktreePath, "branch-only.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, worktreePath, "add", ".")
	runGit(t, worktreePath, "commit", "-m", "branch commit")

	// ...and one new commit on main (behind), made from the original repo dir.
	if err := os.WriteFile(filepath.Join(repo, "main-only.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "main commit")

	h := CheckWorktreeHealth(worktreePath, "feature-diverge", true)
	if h.Ahead != 1 || h.Behind != 1 {
		t.Errorf("Ahead/Behind = %d/%d, want 1/1", h.Ahead, h.Behind)
	}
}

func TestCheckWorktreeHealth_UpstreamGone(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main", ".")

	repo := t.TempDir()
	createTestRepo(t, repo)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "origin", "main")
	createBranch(t, repo, "feature-merged")
	runGit(t, repo, "push", "-u", "origin", "feature-merged")

	// Simulate the branch's PR having been merged + the remote branch
	// deleted, then the user's next fetch --prune noticing.
	runGit(t, origin, "branch", "-D", "feature-merged")
	runGit(t, repo, "fetch", "--prune", "origin")

	h := CheckWorktreeHealth(repo, "feature-merged", false)
	if !h.UpstreamGone {
		t.Errorf("UpstreamGone = false, want true")
	}
}

func TestCheckWorktreeHealth_NoBranchNoOpinion(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)

	h := CheckWorktreeHealth(repo, "", false)
	if h.Ahead != 0 || h.Behind != 0 || h.UpstreamGone {
		t.Errorf("expected zero ahead/behind/upstreamGone with no branch, got %+v", h)
	}
}
