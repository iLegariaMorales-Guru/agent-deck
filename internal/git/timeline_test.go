package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// commitAtDate makes a commit in repo with an explicit author/committer
// date (RFC3339), so tests can control commit timestamps precisely instead
// of racing real wall-clock time.
func commitAtDate(t *testing.T, repo, date, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(msg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, repo, "add", ".")
	cmd := exec.Command("git", "commit", "-m", msg)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit at %s: %v\n%s", date, err, out)
	}
}

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

	commits := RecentCommits(repo, "", 10, time.Time{})
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
	commits := RecentCommits(repo, "", 3, time.Time{})
	if len(commits) != 3 {
		t.Errorf("expected 3 commits (limit), got %d", len(commits))
	}
}

// TestRecentCommits_SinceCutoffExcludesOlderCommits is the regression test
// for a real bug hit live: without a since cutoff, every session's
// timeline showed the branch's ENTIRE commit history, including commits
// made long before that session (or its worktree) ever existed. Callers
// pass the session's own creation time as since (handlers_timeline.go).
func TestRecentCommits_SinceCutoffExcludesOlderCommits(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "-c", "init.defaultBranch=main", "init")
	runGit(t, repo, "config", "user.email", "test@test.com")
	runGit(t, repo, "config", "user.name", "Test User")

	commitAtDate(t, repo, "2020-01-01T00:00:00Z", "old commit, before the session existed")
	commitAtDate(t, repo, "2026-06-01T00:00:00Z", "new commit, made during the session")

	since, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}

	commits := RecentCommits(repo, "", 10, since)
	if len(commits) != 1 {
		t.Fatalf("expected only the post-cutoff commit, got %d: %+v", len(commits), commits)
	}
	if commits[0].Subject != "new commit, made during the session" {
		t.Errorf("Subject = %q, want only the new commit to survive the cutoff", commits[0].Subject)
	}
}

func TestRecentCommits_NonRepoReturnsNil(t *testing.T) {
	dir := t.TempDir() // never git-inited
	if commits := RecentCommits(dir, "", 10, time.Time{}); commits != nil {
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

	pushes := RecentPushes(repo, "main", 10, time.Time{})
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

	if pushes := RecentPushes(repo, "no-upstream", 10, time.Time{}); pushes != nil {
		t.Errorf("expected nil with no configured upstream, got %+v", pushes)
	}
}

// TestRecentPushes_SinceCutoffExcludesOlderPushes mirrors
// TestRecentCommits_SinceCutoffExcludesOlderCommits for pushes: a branch
// pushed long before a given session existed shouldn't have that old push
// show up on the session's timeline either.
func TestRecentPushes_SinceCutoffExcludesOlderPushes(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main", ".")

	repo := t.TempDir()
	runGit(t, repo, "-c", "init.defaultBranch=main", "init")
	runGit(t, repo, "config", "user.email", "test@test.com")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "remote", "add", "origin", origin)

	commitAtDate(t, repo, "2020-01-01T00:00:00Z", "old commit, before the session existed")
	runGit(t, repo, "push", "-u", "origin", "main")

	commitAtDate(t, repo, "2026-06-01T00:00:00Z", "new commit, made during the session")
	runGit(t, repo, "push", "origin", "main")

	since, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}

	pushes := RecentPushes(repo, "main", 10, since)
	if len(pushes) != 1 {
		t.Fatalf("expected only the post-cutoff push, got %d: %+v", len(pushes), pushes)
	}
}

func TestRecentPushes_EmptyBranchReturnsNil(t *testing.T) {
	repo := t.TempDir()
	createTestRepo(t, repo)
	if pushes := RecentPushes(repo, "", 10, time.Time{}); pushes != nil {
		t.Errorf("expected nil for empty branch name, got %+v", pushes)
	}
}
