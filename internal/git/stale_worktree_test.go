package git

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetWorktreeForBranch_StaleEntryIsIgnoredAndPruned is the regression
// test for a real bug hit live: a session's worktree directory got removed
// with plain `rm -rf` (not `git worktree remove`), leaving git's own
// worktree registry (.git/worktrees/<id>) still claiming the branch was
// checked out there. GetWorktreeForBranch used to trust that blindly, so
// the create-session flow "reused" the dead path instead of making a fresh
// worktree — the session then failed to start with
// internal/tmux/workdir_guard.go's "does not exist... was deleted or
// renamed", well after the point where the user could tell what went
// wrong.
func TestGetWorktreeForBranch_StaleEntryIsIgnoredAndPruned(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "sharepoint-pr174")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "sharepoint-pr174"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Simulate the real-world cause: the directory is deleted directly,
	// never through `git worktree remove`, so git's administrative entry
	// for it survives.
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	// Before the fix, this returned worktreePath (stale) instead of "".
	got, err := GetWorktreeForBranch(repo, "sharepoint-pr174")
	if err != nil {
		t.Fatalf("GetWorktreeForBranch: %v", err)
	}
	if got != "" {
		t.Errorf("GetWorktreeForBranch = %q, want empty (stale entry must not be reused)", got)
	}

	// The stale entry must actually be pruned, not just hidden from this
	// one caller — otherwise a second `git worktree add` for the same
	// branch at a fresh path would fail with "already checked out".
	worktrees, err := ListWorktrees(repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	for _, wt := range worktrees {
		if wt.Branch == "sharepoint-pr174" {
			t.Errorf("stale worktree entry for the branch is still registered: %+v", wt)
		}
	}

	// And the real end-to-end proof: creating a fresh worktree for the same
	// branch at a NEW path must now succeed (this is exactly what the
	// create-session flow does next once GetWorktreeForBranch reports "no
	// existing worktree").
	freshPath := filepath.Join(t.TempDir(), "worktree-fresh")
	if err := CreateWorktree(repo, freshPath, "sharepoint-pr174"); err != nil {
		t.Fatalf("CreateWorktree at fresh path after pruning stale entry: %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh worktree directory was not created: %v", err)
	}
}

// TestGetWorktreeForBranch_PrunesStaleEntryForADifferentBranch is the exact
// real-world shape of the bug: agent-deck names worktree directories after
// the branch (.claude/worktrees/<branch>), and the directory that was
// rm -rf'd by hand had a DIFFERENT branch checked out than the one about to
// be created at that same path (e.g. the branch was renamed, or a session
// was recreated with a differently-named branch pointed at the same PR).
// GetWorktreeForBranch("new-branch-name") correctly reports "not found"
// either way, but without pruning the OLD branch's dead registration at
// that path first, the subsequent `git worktree add <path> new-branch-name`
// fails against it anyway.
func TestGetWorktreeForBranch_PrunesStaleEntryForADifferentBranch(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "old-branch-name")
	createBranch(t, repo, "new-branch-name")

	sharedPath := filepath.Join(t.TempDir(), "shared-worktree-slot")
	if err := CreateWorktree(repo, sharedPath, "old-branch-name"); err != nil {
		t.Fatalf("CreateWorktree (old branch): %v", err)
	}
	if err := os.RemoveAll(sharedPath); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	got, err := GetWorktreeForBranch(repo, "new-branch-name")
	if err != nil {
		t.Fatalf("GetWorktreeForBranch: %v", err)
	}
	if got != "" {
		t.Fatalf("GetWorktreeForBranch(new-branch-name) = %q, want empty", got)
	}

	// The real proof: creating the NEW branch's worktree at the SAME path
	// the old (now-deleted) branch used must succeed — it would fail with
	// "already checked out" style errors against the old branch's orphaned
	// registration if GetWorktreeForBranch hadn't pruned unconditionally.
	if err := CreateWorktree(repo, sharedPath, "new-branch-name"); err != nil {
		t.Fatalf("CreateWorktree (new branch, same path as stale old entry): %v", err)
	}
}

// TestGetWorktreeForBranch_LiveEntryStillReturned pins the unchanged happy
// path: a worktree whose directory genuinely exists must still be reported
// (reused), not skipped.
func TestGetWorktreeForBranch_LiveEntryStillReturned(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "still-here")

	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := CreateWorktree(repo, worktreePath, "still-here"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	got, err := GetWorktreeForBranch(repo, "still-here")
	if err != nil {
		t.Fatalf("GetWorktreeForBranch: %v", err)
	}
	resolvedGot, _ := filepath.EvalSymlinks(got)
	resolvedWant, _ := filepath.EvalSymlinks(worktreePath)
	if resolvedGot != resolvedWant {
		t.Errorf("GetWorktreeForBranch = %q, want %q", got, worktreePath)
	}
}
