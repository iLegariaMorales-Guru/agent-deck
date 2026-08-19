package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// statusLinePayload decodes the subset of Claude Code's statusLine JSON
// (https://code.claude.com/docs/en/statusline#available-data) agent-deck
// cares about. Unknown fields are ignored; every field here is optional in
// practice (e.g. context_window is absent before the first API response),
// so callers must tolerate zero values.
type statusLinePayload struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID string `json:"id"`
	} `json:"model"`
	ContextWindow struct {
		ContextWindowSize int     `json:"context_window_size"`
		UsedPercentage    float64 `json:"used_percentage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

// statusLineChainTimeout bounds how long the wrapper waits on a chained
// (pre-existing) statusLine command. Claude Code invokes this after every
// turn — a hung downstream script must not freeze the visible status bar
// indefinitely.
const statusLineChainTimeout = 5 * time.Second

// handleStatusLineHook is the `agent-deck statusline` entrypoint, installed
// as Claude Code's `statusLine` command by `agent-deck hooks install`
// (internal/session/claude_statusline.go's InjectClaudeStatusLine).
//
// It does two independent things with the same stdin bytes:
//  1. If this is an agent-deck-managed session (AGENTDECK_INSTANCE_ID set),
//     persist Claude's own context-window%/cost/model for this Claude
//     session so the web sidebar can read it directly instead of
//     re-deriving an estimate from the transcript.
//  2. Unconditionally chain to whatever statusLine command was configured
//     before agent-deck's (a user's own script, or a third-party tool's —
//     see OriginalStatusLineCommand), forwarding the same stdin and
//     printing its stdout verbatim, so the visible status bar in EVERY
//     Claude Code session on this machine (agent-deck-managed or not)
//     keeps working exactly as it did before this was installed.
func handleStatusLineHook() {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookPayloadSize))
	if err == nil && len(data) > 0 {
		persistStatusLineContext(data)
	}
	chainToOriginalStatusLine(data)
}

// persistStatusLineContext writes the agent-deck-facing subset of the
// payload to disk. Silently no-ops for a session agent-deck doesn't manage,
// an unparseable payload, or one with no session_id — none of those are
// errors worth surfacing on a hot path that runs after every single turn.
func persistStatusLineContext(data []byte) {
	if os.Getenv("AGENTDECK_INSTANCE_ID") == "" {
		return
	}
	var payload statusLinePayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.SessionID == "" {
		return
	}
	_ = session.WriteStatusLineContext(session.StatusLineContext{
		SessionID:         payload.SessionID,
		Model:             payload.Model.ID,
		ContextWindowSize: payload.ContextWindow.ContextWindowSize,
		UsedPercentage:    payload.ContextWindow.UsedPercentage,
		CostUSD:           payload.Cost.TotalCostUSD,
	})
}

// chainToOriginalStatusLine forwards stdin to whatever statusLine command
// was configured before agent-deck's and prints its stdout unchanged. With
// no prior command (nothing was configured), it prints nothing — a blank
// status line rather than inventing a default display the user never asked
// for.
func chainToOriginalStatusLine(stdin []byte) {
	original, ok := session.OriginalStatusLineCommand()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusLineChainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", original)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return
	}
	os.Stdout.Write(bytes.TrimRight(out, "\n"))
}

// installStatusLine and uninstallStatusLine are called from
// handleHooksInstall/Uninstall (hook_handler.go) so a single `agent-deck
// hooks install|uninstall` covers both the event hooks and the statusLine
// wrapper — one setup command for full integration.

func installStatusLine(configDir string) {
	installed, err := session.InjectClaudeStatusLine(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error installing statusLine: %v\n", err)
		return
	}
	if installed {
		fmt.Println("Claude Code statusLine installed (context %, cost, and model now come from Claude itself).")
	} else {
		fmt.Println("Claude Code statusLine already installed.")
	}
}

func uninstallStatusLine(configDir string) {
	removed, err := session.RemoveClaudeStatusLine(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing statusLine: %v\n", err)
		return
	}
	if removed {
		fmt.Println("Claude Code statusLine removed (restored whatever was configured before, if anything).")
	} else {
		fmt.Println("No agent-deck statusLine found to remove.")
	}
}
