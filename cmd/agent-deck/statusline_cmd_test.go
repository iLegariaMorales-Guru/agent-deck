package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// captureStdout (defined in cursor_hooks_cmd_test.go) temporarily redirects
// os.Stdout for the duration of fn and returns whatever it wrote —
// chainToOriginalStatusLine writes straight to os.Stdout (it must, since
// that's what Claude Code's statusLine mechanism actually renders), so
// that's the only way to observe it from a test.

func TestPersistStatusLineContext_WritesWhenInstanceIDSet(t *testing.T) {
	t.Setenv("AGENTDECK_INSTANCE_ID", "test-instance-1")
	const sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	t.Cleanup(func() {
		if path := session.StatusLineContextFilePath(sessionID); path != "" {
			_ = os.Remove(path)
		}
	})

	payload := []byte(`{
		"session_id": "` + sessionID + `",
		"model": {"id": "claude-sonnet-5"},
		"context_window": {"context_window_size": 1000000, "used_percentage": 7.2},
		"cost": {"total_cost_usd": 0.59}
	}`)

	persistStatusLineContext(payload)

	ctx, ok := session.ReadStatusLineContext(sessionID)
	if !ok {
		t.Fatal("expected a persisted context, found none")
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
}

func TestPersistStatusLineContext_NoOpWithoutInstanceID(t *testing.T) {
	t.Setenv("AGENTDECK_INSTANCE_ID", "")
	const sessionID = "ffffffff-0000-1111-2222-333333333333"
	t.Cleanup(func() {
		if path := session.StatusLineContextFilePath(sessionID); path != "" {
			_ = os.Remove(path)
		}
	})

	payload := []byte(`{"session_id": "` + sessionID + `", "context_window": {"used_percentage": 50}}`)
	persistStatusLineContext(payload)

	if _, ok := session.ReadStatusLineContext(sessionID); ok {
		t.Error("expected no context to be persisted for a session agent-deck doesn't manage")
	}
}

func TestPersistStatusLineContext_NoOpOnMalformedPayload(t *testing.T) {
	t.Setenv("AGENTDECK_INSTANCE_ID", "test-instance-2")
	// Neither of these should panic or write anything.
	persistStatusLineContext([]byte("not json"))
	persistStatusLineContext([]byte(`{"model": {"id": "claude-sonnet-5"}}`)) // no session_id
}

func TestChainToOriginalStatusLine_ForwardsStdinAndPrintsStdout(t *testing.T) {
	configDir := t.TempDir()
	settingsPath := filepath.Join(configDir, "settings.json")
	// `cat` echoes stdin back to stdout unchanged — a deterministic stand-in
	// for a real statusLine script (Scape's bridge.sh, the user's own, etc.)
	// that lets this test assert the wrapper forwards stdin faithfully.
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine": {"type": "command", "command": "cat"}}`), 0o644); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}
	if _, err := session.InjectClaudeStatusLine(configDir); err != nil {
		t.Fatalf("InjectClaudeStatusLine: %v", err)
	}
	t.Cleanup(func() {
		_, _ = session.RemoveClaudeStatusLine(configDir) // also deletes the saved original
	})

	out := captureStdout(t, func() {
		chainToOriginalStatusLine([]byte("hello from claude"))
	})
	if out != "hello from claude" {
		t.Errorf("chained stdout = %q, want %q", out, "hello from claude")
	}
}

func TestChainToOriginalStatusLine_NoOriginalPrintsNothing(t *testing.T) {
	// OriginalStatusLineCommand reads a single fixed path under the
	// package-shared hooks dir (see testmain_test.go — HOME is sandboxed
	// once per package, not per test), independent of any configDir. Delete
	// it explicitly rather than relying on no other test having left one
	// behind, so this test doesn't depend on run order.
	if cmd, exists := session.OriginalStatusLineCommand(); exists {
		t.Fatalf("test setup invariant violated: an original statusLine command (%q) is already present", cmd)
	}

	out := captureStdout(t, func() {
		chainToOriginalStatusLine([]byte("hello from claude"))
	})
	if out != "" {
		t.Errorf("chained stdout = %q, want empty (no original command configured)", out)
	}
}
