package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// writeTimelineFixtureJSONL writes raw JSONL lines at the path Claude would
// use for (projectPath, claudeSessionID) under configDir, mirroring
// writeClaudeFixtureJSONL in handlers_analytics_test.go but for arbitrary
// lines shaped for ParseTranscriptTimeline rather than token/usage fields.
func writeTimelineFixtureJSONL(t *testing.T, configDir, projectPath, claudeSessionID string, lines ...string) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", session.ConvertToClaudeDirName(projectPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	path := filepath.Join(dir, claudeSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture jsonl: %v", err)
	}
}

func newTimelineServer(t *testing.T, ms *MenuSession) *Server {
	t.Helper()
	snapshot := &MenuSnapshot{Items: []MenuItem{{Type: MenuItemTypeSession, Session: ms}}}
	// Each test needs its own cache entry — sessions get random-ish ids in
	// these tests already, but be defensive since sharedTimelineCache is a
	// package-level singleton across the whole test binary.
	sharedTimelineCache.mu.Lock()
	delete(sharedTimelineCache.entries, ms.ID)
	sharedTimelineCache.mu.Unlock()
	return NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})
}

func TestSessionTimeline_GitCommitsAndPushes(t *testing.T) {
	origin := t.TempDir()
	runGitCmd(t, origin, "init", "--bare", "-b", "main", ".")

	repo := initTestGitRepo(t)
	runGitCmd(t, repo, "remote", "add", "origin", origin)
	runGitCmd(t, repo, "push", "-u", "origin", "main")

	ms := &MenuSession{ID: "git-sess", Tool: "claude", ProjectPath: repo, Branch: "main"}
	srv := newTimelineServer(t, ms)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/git-sess/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp sessionTimelineResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var sawCommit, sawPush bool
	for _, ev := range resp.Events {
		if ev.Kind == "commit" && ev.Text == "initial" {
			sawCommit = true
		}
		if ev.Kind == "push" {
			sawPush = true
		}
	}
	if !sawCommit {
		t.Errorf("expected a commit event, got %+v", resp.Events)
	}
	if !sawPush {
		t.Errorf("expected a push event, got %+v", resp.Events)
	}
}

func TestSessionTimeline_TranscriptEvents(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	const projectPath = "/srv/timeline-parity"
	const claudeSessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeTimelineFixtureJSONL(t, configDir, projectPath, claudeSessionID,
		`{"type":"user","timestamp":"2026-08-19T13:00:00Z","message":{"content":"Add health badges please"}}`,
		`{"type":"assistant","timestamp":"2026-08-19T13:00:05Z","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"health.go"}}]}}`,
		`{"type":"user","isCompactSummary":true,"timestamp":"2026-08-19T14:00:00Z","message":{"content":"This session is being continued from a previous conversation..."}}`,
	)

	ms := &MenuSession{
		ID: "transcript-sess", Tool: "claude", ProjectPath: projectPath, ClaudeSessionID: claudeSessionID,
	}
	srv := newTimelineServer(t, ms)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/transcript-sess/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp sessionTimelineResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 3 {
		t.Fatalf("expected 3 events (prompt, edit, compact), got %+v", resp.Events)
	}

	kinds := map[string]bool{}
	for _, ev := range resp.Events {
		kinds[ev.Kind] = true
	}
	for _, want := range []string{"prompt", "edit", "compact"} {
		if !kinds[want] {
			t.Errorf("expected a %q event, got %+v", want, resp.Events)
		}
	}

	// Newest first.
	for i := 1; i < len(resp.Events); i++ {
		if resp.Events[i].Time.After(resp.Events[i-1].Time) {
			t.Errorf("events not sorted newest-first: %+v", resp.Events)
		}
	}
}

func TestSessionTimeline_SSHSessionSkipsEverything(t *testing.T) {
	ms := &MenuSession{ID: "ssh-sess", Tool: "claude", ProjectPath: "/some/remote/path", SSHHost: "remote-box"}
	srv := newTimelineServer(t, ms)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/ssh-sess/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp sessionTimelineResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 0 {
		t.Errorf("expected no events for an SSH session, got %+v", resp.Events)
	}
}

func TestSessionTimeline_UnknownSessionReturns404(t *testing.T) {
	srv := newTimelineServer(t, &MenuSession{ID: "some-sess", Tool: "claude"})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/does-not-exist/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestSessionTimeline_MethodNotAllowed(t *testing.T) {
	srv := newTimelineServer(t, &MenuSession{ID: "some-sess", Tool: "claude"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/some-sess/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestSessionTimeline_HasMoreWhenTruncated(t *testing.T) {
	// A single source (git.RecentCommits, git.RecentPushes) is itself capped
	// at maxTimelineEvents, so exceeding the response's overall cap needs
	// two sources combined — commits AND pushes here, ~80 of each, comfortably
	// over 150 together even though neither alone would be.
	origin := t.TempDir()
	runGitCmd(t, origin, "init", "--bare", "-b", "main", ".")

	repo := initTestGitRepo(t)
	runGitCmd(t, repo, "remote", "add", "origin", origin)
	runGitCmd(t, repo, "push", "-u", "origin", "main")

	const n = 80
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte{byte(i % 256)}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runGitCmd(t, repo, "add", ".")
		runGitCmd(t, repo, "commit", "-m", "commit")
		runGitCmd(t, repo, "push", "origin", "main")
	}

	ms := &MenuSession{ID: "truncate-sess", Tool: "claude", ProjectPath: repo, Branch: "main"}
	srv := newTimelineServer(t, ms)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/truncate-sess/timeline", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp sessionTimelineResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasMore {
		t.Errorf("expected HasMore=true with %d+ commits", maxTimelineEvents)
	}
	if len(resp.Events) != maxTimelineEvents {
		t.Errorf("expected events capped at %d, got %d", maxTimelineEvents, len(resp.Events))
	}
}
