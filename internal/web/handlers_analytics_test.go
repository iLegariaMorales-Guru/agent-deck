package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// fakeMenuData is a minimal MenuDataLoader for tests that need a specific
// MenuSnapshot without going through storage.
type fakeMenuData struct {
	snapshot *MenuSnapshot
}

func (f *fakeMenuData) LoadMenuSnapshot() (*MenuSnapshot, error) {
	return f.snapshot, nil
}

// writeClaudeFixtureJSONL writes a minimal transcript at the path Claude
// would use for (projectPath, claudeSessionID) under configDir, so
// session.ResolveClaudeTranscriptPath (and therefore claudeTranscriptAnalytics)
// finds it. model is optional (raw model ID form, e.g. "claude-opus-4-6");
// pass "" to omit it from the fixture line.
func writeClaudeFixtureJSONL(t *testing.T, configDir, projectPath, claudeSessionID string, inputTokens, cacheReadTokens int, model string) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", session.ConvertToClaudeDirName(projectPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	modelField := ""
	if model != "" {
		modelField = `"model":"` + model + `",`
	}
	line := `{"type":"assistant","message":{` + modelField + `"usage":{"input_tokens":` +
		strconv.Itoa(inputTokens) + `,"output_tokens":10,"cache_read_input_tokens":` + strconv.Itoa(cacheReadTokens) + `}}}`
	path := filepath.Join(dir, claudeSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write fixture jsonl: %v", err)
	}
}

func TestContextBatch_ClaudeSessionParsesTranscript(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	const projectPath = "/srv/parity"
	const claudeSessionID = "11111111-2222-3333-4444-555555555555"
	// 180000 current-context tokens on the default 200k window = 90%.
	writeClaudeFixtureJSONL(t, configDir, projectPath, claudeSessionID, 180000, 0, "")

	snapshot := &MenuSnapshot{
		Items: []MenuItem{
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:              "claude-sess",
					Tool:            "claude",
					ProjectPath:     projectPath,
					ClaudeSessionID: claudeSessionID,
				},
			},
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:   "shell-sess",
					Tool: "shell",
				},
			},
		},
	}

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/context/batch?ids=claude-sess,shell-sess,missing", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Context       map[string]float64 `json:"context"`
		EstimatedCost map[string]float64 `json:"estimatedCost"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got := resp.Context["claude-sess"]; got < 89 || got > 91 {
		t.Errorf("claude-sess context = %v, want ~90", got)
	}
	if _, ok := resp.Context["shell-sess"]; ok {
		t.Errorf("shell-sess should be absent (non-claude tool), got %v", resp.Context["shell-sess"])
	}
	if _, ok := resp.Context["missing"]; ok {
		t.Errorf("unknown id should be absent, got %v", resp.Context["missing"])
	}

	// The transcript has real token usage, so a price-table cost estimate
	// should come along for the sidebar's "$0.00" fallback (see
	// contextCacheEntry.cost) — even though no model was set on the
	// fixture, CalculateCost falls back to default (Sonnet) pricing.
	if got := resp.EstimatedCost["claude-sess"]; got <= 0 {
		t.Errorf("claude-sess estimatedCost = %v, want > 0", got)
	}
	if _, ok := resp.EstimatedCost["shell-sess"]; ok {
		t.Errorf("shell-sess should have no estimatedCost (non-claude tool), got %v", resp.EstimatedCost["shell-sess"])
	}
}

// TestContextBatch_LiveModelFallback covers the "Tool default" case: a
// session launched with no explicit model has an empty MenuSession.Model,
// so the sidebar chip would otherwise show nothing even once Claude has
// actually run and said what model it used.
func TestContextBatch_LiveModelFallback(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	const noModelProject = "/srv/no-model"
	const noModelSessionID = "22222222-3333-4444-5555-666666666666"
	writeClaudeFixtureJSONL(t, configDir, noModelProject, noModelSessionID, 1000, 0, "claude-opus-4-6")

	const explicitProject = "/srv/explicit-model"
	const explicitSessionID = "33333333-4444-5555-6666-777777777777"
	writeClaudeFixtureJSONL(t, configDir, explicitProject, explicitSessionID, 1000, 0, "claude-haiku-4-5")

	snapshot := &MenuSnapshot{
		Items: []MenuItem{
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:              "no-model-sess",
					Tool:            "claude",
					ProjectPath:     noModelProject,
					ClaudeSessionID: noModelSessionID,
					// Model intentionally empty — "Tool default" at launch.
				},
			},
			{
				Type: MenuItemTypeSession,
				Session: &MenuSession{
					ID:              "explicit-model-sess",
					Tool:            "claude",
					ProjectPath:     explicitProject,
					ClaudeSessionID: explicitSessionID,
					Model:           "Claude Sonnet", // launched with a specific model
				},
			},
		},
	}

	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/context/batch?ids=no-model-sess,explicit-model-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		LiveModel map[string]liveModel `json:"liveModel"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	got, ok := resp.LiveModel["no-model-sess"]
	if !ok {
		t.Fatal("expected a liveModel fallback for the Tool-default session")
	}
	if got.Model != "Claude Opus" {
		t.Errorf("liveModel.Model = %q, want %q", got.Model, "Claude Opus")
	}

	if _, ok := resp.LiveModel["explicit-model-sess"]; ok {
		t.Error("session launched with an explicit model should not get a liveModel override")
	}
}

// TestContextBatch_PrefersStatusLineOverTranscript covers the fix for a real
// bug report: a Sonnet 5 session's own /context said 7% while the sidebar
// (deriving context% from a transcript re-parse + agent-deck's own
// model-context-window table) said 32% for the same session — the table had
// no entry for the new model family (see analytics.go's
// modelContextWindowPrefixes, #1963). When Claude Code's own statusLine
// data (internal/session/claude_statusline.go) is available for a session,
// it must win over the transcript-derived estimate entirely, including for
// cost and model — not just supplement it.
func TestContextBatch_PrefersStatusLineOverTranscript(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	const projectPath = "/srv/statusline-pref"
	const claudeSessionID = "44444444-5555-6666-7777-888888888888"
	// Transcript alone (no window-size hint) would compute against the 200k
	// default: 180000/200000 = 90%. The statusLine value below (7.2%, sourced
	// from Claude's real ~1M window) must win instead.
	writeClaudeFixtureJSONL(t, configDir, projectPath, claudeSessionID, 180000, 0, "claude-sonnet-5")

	if err := session.WriteStatusLineContext(session.StatusLineContext{
		SessionID:         claudeSessionID,
		Model:             "claude-sonnet-5",
		ContextWindowSize: 1_000_000,
		UsedPercentage:    7.2,
		CostUSD:           0.59,
	}); err != nil {
		t.Fatalf("seed statusline context: %v", err)
	}
	t.Cleanup(func() {
		if path := session.StatusLineContextFilePath(claudeSessionID); path != "" {
			_ = os.Remove(path)
		}
	})

	snapshot := &MenuSnapshot{
		Items: []MenuItem{{
			Type: MenuItemTypeSession,
			Session: &MenuSession{
				ID:              "claude-sess",
				Tool:            "claude",
				ProjectPath:     projectPath,
				ClaudeSessionID: claudeSessionID,
			},
		}},
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: snapshot}})

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/context/batch?ids=claude-sess", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Context       map[string]float64 `json:"context"`
		EstimatedCost map[string]float64 `json:"estimatedCost"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp.Context["claude-sess"]; got < 7.1 || got > 7.3 {
		t.Errorf("context = %v, want 7.2 (from statusLine, not the 90%% transcript-derived estimate)", got)
	}
	if got := resp.EstimatedCost["claude-sess"]; got != 0.59 {
		t.Errorf("estimatedCost = %v, want 0.59 (from statusLine, not a price-table estimate)", got)
	}
}

func TestContextBatch_EmptyIDs(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/context/batch?ids=", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Context map[string]float64 `json:"context"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Context) != 0 {
		t.Errorf("expected empty context map, got %v", resp.Context)
	}
}

func TestContextBatch_MethodNotAllowed(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})

	req := httptest.NewRequest(http.MethodPut, "/api/analytics/context/batch", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestContextBatch_Unauthorized(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		MenuData:   &fakeMenuData{snapshot: &MenuSnapshot{}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/context/batch?ids=sess1", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGeminiSessionAnalytics_ContextPercent(t *testing.T) {
	a := &session.GeminiSessionAnalytics{CurrentContextTokens: 500000}
	if got := a.ContextPercent(); got < 49.99 || got > 50.01 {
		t.Errorf("ContextPercent() = %v, want 50", got)
	}

	capped := &session.GeminiSessionAnalytics{CurrentContextTokens: 5_000_000}
	if got := capped.ContextPercent(); got != 100 {
		t.Errorf("ContextPercent() over window = %v, want capped at 100", got)
	}

	var nilAnalytics *session.GeminiSessionAnalytics
	if got := nilAnalytics.ContextPercent(); got != 0 {
		t.Errorf("nil receiver ContextPercent() = %v, want 0", got)
	}
}
