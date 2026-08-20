package git

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CommitInfo is one entry from `git log` — cheap, local, no network. See
// RecentCommits.
type CommitInfo struct {
	Hash         string
	Subject      string
	Time         time.Time
	FilesChanged int
}

// commitBlockRE splits `git log --format=@@%h\x1f%ct\x1f%s --shortstat`
// output into one block per commit. The literal "@@" prefix is there
// specifically so a commit subject that happens to contain the 0x1f field
// separator (extremely unlikely, but not impossible for pasted text) can
// never be mistaken for a block boundary — only a line git itself emitted
// via %h at the very start of the format can start with it.
var commitBlockRE = regexp.MustCompile(`(?m)^@@`)

// shortstatRE pulls the file count out of a --shortstat summary line, e.g.
// " 4 files changed, 27 insertions(+), 49 deletions(-)" or the singular
// " 1 file changed, 2 insertions(+)".
var shortstatRE = regexp.MustCompile(`(\d+) files? changed`)

// RecentCommits returns up to limit most-recent commits reachable from
// branchName in dir (or HEAD if branchName is empty), newest first. Local
// `git log` only — no network, same "safe to call on every open" cost
// profile as CheckWorktreeHealth. Returns nil (not an error) for a dir
// that isn't a git repo, a branch that doesn't exist, or any other git
// failure — callers treat "nothing to show" as the normal empty case,
// same convention as WorktreeHealth's zero value.
func RecentCommits(dir, branchName string, limit int) []CommitInfo {
	ref := branchName
	if ref == "" {
		ref = "HEAD"
	}
	if limit <= 0 {
		limit = 20
	}
	out, err := exec.Command("git", "-C", dir, "log", ref,
		"-n", strconv.Itoa(limit),
		"--format=@@%h\x1f%ct\x1f%s", "--shortstat").Output()
	if err != nil {
		return nil
	}
	return parseCommitBlocks(string(out))
}

func parseCommitBlocks(raw string) []CommitInfo {
	var commits []CommitInfo
	// Each block starts with "@@" on its own format line; split keeps that
	// marker as the first two bytes of everything after index 0.
	idxs := commitBlockRE.FindAllStringIndex(raw, -1)
	for i, loc := range idxs {
		end := len(raw)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := raw[loc[1]:end] // past the "@@" marker itself
		nl := strings.IndexByte(block, '\n')
		headerLine := block
		rest := ""
		if nl >= 0 {
			headerLine = block[:nl]
			rest = block[nl+1:]
		}
		fields := strings.SplitN(headerLine, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		epoch, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		c := CommitInfo{
			Hash:    fields[0],
			Time:    time.Unix(epoch, 0),
			Subject: fields[2],
		}
		if m := shortstatRE.FindStringSubmatch(rest); m != nil {
			c.FilesChanged, _ = strconv.Atoi(m[1])
		}
		commits = append(commits, c)
	}
	return commits
}

// PushInfo is one local record of branchName having been pushed to its
// upstream — reconstructed from the remote-tracking ref's OWN reflog, not
// any network call. Git already writes an "update by push" reflog entry
// on refs/remotes/<remote>/<branch> every time a `git push` from this
// machine moves it (verified against this repo's own history — see
// timeline_test.go). This catches a push made from a terminal or an IDE
// just as well as one a coding agent ran via its Bash tool, and needs
// nothing beyond what CheckWorktreeHealth already reads.
type PushInfo struct {
	Hash string
	Time time.Time
}

// pushReflogSubject is the exact string git writes for a push-caused
// update (as opposed to e.g. "fetch" moving the same ref).
const pushReflogSubject = "update by push"

// RecentPushes returns up to limit most-recent pushes of branchName's
// upstream, newest first. Returns nil when branchName has no configured
// upstream, dir isn't a git repo, or there's no reflog for it yet (a
// remote-tracking ref only grows a reflog once something has actually
// updated it locally).
func RecentPushes(dir, branchName string, limit int) []PushInfo {
	if branchName == "" {
		return nil
	}
	ref := resolveUpstreamRef(dir, branchName)
	if ref == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	out, err := exec.Command("git", "-C", dir, "log", "-g",
		"--format=%H\x1f%gs\x1f%ct", "-n", strconv.Itoa(limit*4), ref).Output()
	if err != nil {
		return nil
	}
	var pushes []PushInfo
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		if strings.TrimSpace(fields[1]) != pushReflogSubject {
			continue // a "fetch" or other reflog action on the same ref, not a push
		}
		epoch, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		pushes = append(pushes, PushInfo{Hash: fields[0], Time: time.Unix(epoch, 0)})
		if len(pushes) >= limit {
			break
		}
	}
	return pushes
}

// resolveUpstreamRef returns the full ref (e.g.
// "refs/remotes/origin/feature-x") branchName's upstream points at, or ""
// if branchName has none configured. Mirrors health.go's
// branchUpstreamGone, which needs the same lookup but only cares whether
// it resolves — kept separate rather than shared so each file's git
// invocation stays a single, obviously-correct command.
func resolveUpstreamRef(dir, branchName string) string {
	out, err := exec.Command("git", "-C", dir, "for-each-ref",
		"--format=%(upstream)", "refs/heads/"+branchName).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
