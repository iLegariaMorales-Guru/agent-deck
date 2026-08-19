package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// cleanupStatusLineFiles removes the side files a statusLine test may have
// touched under the package-shared GetHooksDir() (see testmain_test.go's
// isolatePackageHome — HOME is sandboxed per-package, not per-test, so
// leftover files here would otherwise leak between tests in this file).
func cleanupStatusLineFiles(t *testing.T, extraSessionIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		_ = os.Remove(statusLineOriginalFilePath())
		for _, id := range extraSessionIDs {
			if path := StatusLineContextFilePath(id); path != "" {
				_ = os.Remove(path)
			}
		}
	})
}

func TestInjectClaudeStatusLine_Fresh(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	installed, err := InjectClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("InjectClaudeStatusLine failed: %v", err)
	}
	if !installed {
		t.Error("expected newly installed")
	}
	if !CheckClaudeStatusLineInstalled(tmpDir) {
		t.Error("CheckClaudeStatusLineInstalled = false, want true")
	}
	if _, exists := OriginalStatusLineCommand(); exists {
		t.Error("expected no original statusLine command when nothing existed before")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	var cfg claudeStatusLineConfig
	if err := json.Unmarshal(settings["statusLine"], &cfg); err != nil {
		t.Fatalf("parse statusLine: %v", err)
	}
	if cfg.Command != agentDeckStatusLineCommand || cfg.Type != "command" {
		t.Errorf("statusLine = %+v, want command=%q", cfg, agentDeckStatusLineCommand)
	}
}

func TestInjectClaudeStatusLine_PreservesExisting(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	existing := `{"statusLine": {"type": "command", "command": "bash ~/.scape/bridge.sh"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	installed, err := InjectClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("InjectClaudeStatusLine failed: %v", err)
	}
	if !installed {
		t.Error("expected newly installed")
	}
	if !CheckClaudeStatusLineInstalled(tmpDir) {
		t.Error("CheckClaudeStatusLineInstalled = false, want true")
	}

	cmd, exists := OriginalStatusLineCommand()
	if !exists {
		t.Fatal("expected the pre-existing statusLine command to be preserved")
	}
	if cmd != "bash ~/.scape/bridge.sh" {
		t.Errorf("OriginalStatusLineCommand = %q, want %q", cmd, "bash ~/.scape/bridge.sh")
	}
}

func TestInjectClaudeStatusLine_Idempotent(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	if _, err := InjectClaudeStatusLine(tmpDir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	installed, err := InjectClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if installed {
		t.Error("expected second call to be a no-op (already installed)")
	}
}

func TestRemoveClaudeStatusLine_RestoresOriginal(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	existing := `{"statusLine": {"type": "command", "command": "bash ~/.scape/bridge.sh"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}
	if _, err := InjectClaudeStatusLine(tmpDir); err != nil {
		t.Fatalf("install: %v", err)
	}

	removed, err := RemoveClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("RemoveClaudeStatusLine failed: %v", err)
	}
	if !removed {
		t.Error("expected removal")
	}
	if CheckClaudeStatusLineInstalled(tmpDir) {
		t.Error("expected agent-deck's statusLine to be gone")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	var cfg claudeStatusLineConfig
	if err := json.Unmarshal(settings["statusLine"], &cfg); err != nil {
		t.Fatalf("parse restored statusLine: %v", err)
	}
	if cfg.Command != "bash ~/.scape/bridge.sh" {
		t.Errorf("restored statusLine command = %q, want %q", cfg.Command, "bash ~/.scape/bridge.sh")
	}
	if _, exists := OriginalStatusLineCommand(); exists {
		t.Error("expected the original-command side file to be cleaned up after restore")
	}
}

func TestRemoveClaudeStatusLine_NoOriginal(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	if _, err := InjectClaudeStatusLine(tmpDir); err != nil {
		t.Fatalf("install: %v", err)
	}
	removed, err := RemoveClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("RemoveClaudeStatusLine failed: %v", err)
	}
	if !removed {
		t.Error("expected removal")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	if _, ok := settings["statusLine"]; ok {
		t.Error("expected statusLine key to be removed entirely when nothing preceded it")
	}
}

func TestRemoveClaudeStatusLine_LeavesNonAgentDeckAlone(t *testing.T) {
	cleanupStatusLineFiles(t)
	tmpDir := t.TempDir()

	existing := `{"statusLine": {"type": "command", "command": "bash ~/.scape/bridge.sh"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	removed, err := RemoveClaudeStatusLine(tmpDir)
	if err != nil {
		t.Fatalf("RemoveClaudeStatusLine failed: %v", err)
	}
	if removed {
		t.Error("expected no-op: this statusLine was never agent-deck's")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if string(data) != existing {
		// json round-trip may reformat whitespace; compare parsed instead.
		var got, want map[string]json.RawMessage
		_ = json.Unmarshal(data, &got)
		_ = json.Unmarshal([]byte(existing), &want)
		if string(got["statusLine"]) != string(want["statusLine"]) {
			t.Errorf("statusLine changed: got %s, want %s", got["statusLine"], want["statusLine"])
		}
	}
}

func TestCheckClaudeStatusLineInstalled_AbsentAndForeign(t *testing.T) {
	tmpDir := t.TempDir()
	if CheckClaudeStatusLineInstalled(tmpDir) {
		t.Error("expected false for a config dir with no settings.json at all")
	}

	existing := `{"statusLine": {"type": "command", "command": "some-other-tool"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}
	if CheckClaudeStatusLineInstalled(tmpDir) {
		t.Error("expected false for a foreign statusLine command")
	}
}

func TestWriteReadStatusLineContext_RoundTrip(t *testing.T) {
	sessionID := "11111111-2222-3333-4444-555555555555"
	cleanupStatusLineFiles(t, sessionID)

	err := WriteStatusLineContext(StatusLineContext{
		SessionID:         sessionID,
		Model:             "claude-sonnet-5",
		ContextWindowSize: 1_000_000,
		UsedPercentage:    7.2,
		CostUSD:           0.59,
	})
	if err != nil {
		t.Fatalf("WriteStatusLineContext failed: %v", err)
	}

	ctx, ok := ReadStatusLineContext(sessionID)
	if !ok {
		t.Fatal("ReadStatusLineContext = not found, want found")
	}
	if ctx.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want claude-sonnet-5", ctx.Model)
	}
	if ctx.ContextWindowSize != 1_000_000 {
		t.Errorf("ContextWindowSize = %d, want 1000000", ctx.ContextWindowSize)
	}
	if ctx.UsedPercentage != 7.2 {
		t.Errorf("UsedPercentage = %v, want 7.2", ctx.UsedPercentage)
	}
	if ctx.CostUSD != 0.59 {
		t.Errorf("CostUSD = %v, want 0.59", ctx.CostUSD)
	}
	if ctx.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be stamped by WriteStatusLineContext")
	}
}

func TestReadStatusLineContext_Absent(t *testing.T) {
	if _, ok := ReadStatusLineContext("99999999-9999-9999-9999-999999999999"); ok {
		t.Error("expected not-found for a session that was never written")
	}
}

func TestStatusLineContextFilePath_RejectsPathTraversal(t *testing.T) {
	cases := []string{"../../etc/passwd", "..", "", "foo/../bar", "foo/bar"}
	for _, c := range cases {
		if path := StatusLineContextFilePath(c); path != "" {
			t.Errorf("StatusLineContextFilePath(%q) = %q, want empty (rejected)", c, path)
		}
	}
}
