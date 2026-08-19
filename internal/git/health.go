package git

import (
	"os"
	"os/exec"
	"strings"
)

// WorktreeHealth summarizes cheap, local-only git signals for a session's
// working directory — surfaced by the sidebar as small badges so a user
// notices trouble (missing worktree, dirty tree, diverged/merged branch)
// before it turns into a hard failure trying to create/attach/finish a
// session (see stale_worktree_test.go for the exact "does not exist"
// failure this is meant to warn about earlier).
//
// Every check here shells out to git once and only reads local refs/index —
// no network calls — so CheckWorktreeHealth is safe to run on every sidebar
// poll (see internal/web/handlers_health.go's TTL cache, which exists to
// keep that "safe" honest under many sessions rather than because any one
// check is expensive).
type WorktreeHealth struct {
	// WorktreeMissing is true when a worktree was expected at workDir (the
	// session has a WorktreePath) but the directory no longer exists on
	// disk. This is the exact stale-worktree shape GetWorktreeForBranch now
	// prunes for NEW sessions (stale_worktree_test.go) — but a session
	// that's already pinned to the dead path needs its own signal, since
	// pruning git's registry doesn't move that path back to life.
	WorktreeMissing bool `json:"worktreeMissing,omitempty"`
	// UncommittedChanges is true when a file git ALREADY TRACKS has a
	// modified/staged change in workDir — untracked files are deliberately
	// excluded (see hasUncommittedChanges) so stray local/scratch files
	// that were never meant to be committed don't leave this permanently
	// true. Always false when WorktreeMissing (nothing to check).
	UncommittedChanges bool `json:"uncommittedChanges,omitempty"`
	// UpstreamGone is true when branchName has a configured upstream whose
	// remote-tracking ref no longer exists locally — the same signal `git
	// branch -vv` renders as "[gone]". In practice this almost always means
	// the branch's PR was merged (or otherwise closed) and the remote
	// branch got deleted. It's a proxy, not a live GitHub check: it's only
	// as fresh as the last `git fetch --prune`, and a branch that was never
	// pushed at all reads as UpstreamGone=false (no upstream configured),
	// not true.
	UpstreamGone bool `json:"upstreamGone,omitempty"`
}

// Deliberately NOT tracked here: ahead/behind commit counts vs the default
// branch. An earlier version of this badge included them, but being N
// commits behind main is completely normal for any active branch — it's
// not a problem to warn about, so surfacing it was pure noise (real user
// feedback: it stayed lit on healthy, actively-worked sessions with nothing
// wrong). Only genuinely actionable signals belong on this struct.

// IsClean reports whether h found nothing worth badging — used by callers
// to skip emitting an entry at all rather than sending an all-zero object.
func (h WorktreeHealth) IsClean() bool {
	return h == WorktreeHealth{}
}

// CheckWorktreeHealth runs the local checks described on WorktreeHealth for
// a session whose effective working directory is workDir and whose current
// branch is branchName. worktreeExpected should be true when the session is
// pinned to a specific worktree path (MenuSession.WorktreePath set) — that's
// the only case where a missing directory is itself the finding, rather
// than "not a git repo, skip".
//
// branchName may be empty (non-git session, or branch lookup failed
// upstream) — UpstreamGone is simply left at its zero value in that case
// rather than guessing.
func CheckWorktreeHealth(workDir, branchName string, worktreeExpected bool) WorktreeHealth {
	var h WorktreeHealth
	if workDir == "" {
		return h
	}
	if worktreeExpected {
		if _, err := os.Stat(workDir); err != nil {
			h.WorktreeMissing = true
			return h // directory doesn't exist: nothing else here is checkable
		}
	}

	h.UncommittedChanges = hasUncommittedChanges(workDir)

	if branchName != "" {
		h.UpstreamGone = branchUpstreamGone(workDir, branchName)
	}
	return h
}

// hasUncommittedChanges checks TRACKED files only (--untracked-files=no) —
// deliberately, not the plain `git status --porcelain`. A real repo tends
// to accumulate untracked scratch/local files that are never meant to be
// committed (and not gitignored either); counting those made the badge
// permanently "dirty" regardless of actual commits, which is exactly the
// false-positive a user hit live. Modified/staged changes to files git
// already tracks are the actual risk (lost work, surprising merge/finish),
// so that's what this flags.
func hasUncommittedChanges(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// branchUpstreamGone reports whether branchName has a configured upstream
// ref that git already knows is gone (pruned on the last fetch).
func branchUpstreamGone(dir, branchName string) bool {
	out, err := exec.Command("git", "-C", dir, "for-each-ref",
		"--format=%(upstream)%00%(upstream:track)", "refs/heads/"+branchName).Output()
	if err != nil {
		return false
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 2)
	if len(parts) != 2 {
		return false
	}
	upstream, track := parts[0], parts[1]
	return upstream != "" && strings.Contains(track, "gone")
}
