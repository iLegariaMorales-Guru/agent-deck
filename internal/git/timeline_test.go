package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecentCommits_ReturnsNewestFirstWithFileCounts(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo) // "Initial commit", 1 file (README.md)

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add two files")

	commits := RecentCommits(repo, "", 10)
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d: %+v", len(commits), commits)
	}
	if commits[0].Subject != "add two files" {
		t.Errorf("commits[0].Subject = %q, want newest-first ordering", commits[0].Subject)
	}
	if commits[0].FilesChanged != 2 {
		t.Errorf("commits[0].FilesChanged = %d, want 2", commits[0].FilesChanged)
	}
	if commits[1].Subject != "Initial commit" {
		t.Errorf("commits[1].Subject = %q, want %q", commits[1].Subject, "Initial commit")
	}
	if commits[0].Hash == "" || commits[0].Time.IsZero() {
		t.Errorf("expected hash and time populated, got %+v", commits[0])
	}
}

func TestRecentCommits_RespectsLimit(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte{byte(i)}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runGit(t, repo, "add", ".")
		runGit(t, repo, "commit", "-m", "commit")
	}
	commits := RecentCommits(repo, "", 3)
	if len(commits) != 3 {
		t.Errorf("expected 3 commits (limit), got %d", len(commits))
	}
}

func TestRecentCommits_NonRepoReturnsNil(t *testing.T) {
	dir := t.TempDir() // never git-inited
	if commits := RecentCommits(dir, "", 10); commits != nil {
		t.Errorf("expected nil for non-repo dir, got %+v", commits)
	}
}

func TestRecentPushes_DetectsRealPushViaReflog(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main", ".")

	repo := t.TempDir()
	createTestRepo(t, repo)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-u", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "second commit")
	runGit(t, repo, "push", "origin", "main")

	pushes := RecentPushes(repo, "main", 10)
	if len(pushes) != 2 {
		t.Fatalf("expected 2 pushes (initial -u push + second push), got %d: %+v", len(pushes), pushes)
	}
	if pushes[0].Time.Before(pushes[1].Time) {
		t.Errorf("expected newest-first ordering, got %+v", pushes)
	}
}

func TestRecentPushes_NoUpstreamReturnsNil(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	createBranch(t, repo, "no-upstream")

	if pushes := RecentPushes(repo, "no-upstream", 10); pushes != nil {
		t.Errorf("expected nil with no configured upstream, got %+v", pushes)
	}
}

func TestRecentPushes_EmptyBranchReturnsNil(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	if pushes := RecentPushes(repo, "", 10); pushes != nil {
		t.Errorf("expected nil for empty branch name, got %+v", pushes)
	}
}
