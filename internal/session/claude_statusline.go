package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/atomicfile"
)

// This file wires agent-deck into Claude Code's statusLine mechanism
// (https://code.claude.com/docs/en/statusline) as an authoritative source
// for context-window usage and cost — Claude Code computes both itself on
// every turn and hands them to the configured statusLine command via
// stdin JSON. That is a real number from Claude, not agent-deck's own
// transcript-derived estimate (see analytics.go's contextWindowForModel,
// which has repeatedly gone stale the moment a new model family ships —
// see #1963).
//
// Two independent pieces live here:
//   - InjectClaudeStatusLine / RemoveClaudeStatusLine /
//     CheckClaudeStatusLineInstalled: settings.json wiring, mirroring
//     claude_hooks.go's read-preserve-modify-write pattern. Unlike hooks
//     (additive per-event arrays), statusLine is a single top-level command,
//     so installing it must preserve whatever was configured before (the
//     user's own script, or a third-party tool's) to a side file the
//     runtime wrapper chains to — see statusLineOriginalFilePath.
//   - StatusLineContext / WriteStatusLineContext / ReadStatusLineContext:
//     the small per-Claude-session file the actual wrapper (cmd/agent-deck's
//     `agent-deck statusline` entrypoint) writes on every invocation, and
//     that internal/web's context-batch endpoint reads back.

// agentDeckStatusLineCommand is the marker command used to identify
// agent-deck's statusLine wrapper in settings.json.
const agentDeckStatusLineCommand = "agent-deck statusline"

// claudeStatusLineConfig mirrors the "statusLine" key's shape in
// settings.json: {"type": "command", "command": "..."}.
type claudeStatusLineConfig struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// statusLineOriginalFilePath stores whatever statusLine config existed
// before agent-deck installed its own (the exact settings.json fragment),
// so the wrapper can chain to it and RemoveClaudeStatusLine can restore it
// byte-for-byte. Absent means "nothing was configured before".
func statusLineOriginalFilePath() string {
	return filepath.Join(GetHooksDir(), "statusline-original.json")
}

// InjectClaudeStatusLine installs agent-deck's statusLine wrapper into
// Claude Code's settings.json. Returns true if newly installed, false if
// agent-deck's statusLine is already in place (matching InjectClaudeHooks'
// no-op-when-already-installed semantics).
func InjectClaudeStatusLine(configDir string) (bool, error) {
	settingsPath := filepath.Join(configDir, "settings.json")

	var rawSettings map[string]json.RawMessage
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("read settings.json: %w", err)
		}
		rawSettings = make(map[string]json.RawMessage)
	} else if err := json.Unmarshal(data, &rawSettings); err != nil {
		return false, fmt.Errorf("parse settings.json: %w", err)
	}

	if existing, ok := rawSettings["statusLine"]; ok {
		var cfg claudeStatusLineConfig
		if err := json.Unmarshal(existing, &cfg); err == nil && strings.Contains(cfg.Command, agentDeckStatusLineCommand) {
			return false, nil // already installed
		}
		// Preserve whatever was configured (the user's own script, Scape,
		// ccusage, etc.) so the wrapper keeps it working unchanged.
		if err := os.MkdirAll(GetHooksDir(), 0o700); err != nil {
			return false, fmt.Errorf("create hooks dir: %w", err)
		}
		if err := atomicfile.WriteFile(statusLineOriginalFilePath(), existing, 0o600); err != nil {
			return false, fmt.Errorf("save original statusLine: %w", err)
		}
	}

	newCfg := claudeStatusLineConfig{Type: "command", Command: agentDeckStatusLineCommand}
	newRaw, err := json.Marshal(newCfg)
	if err != nil {
		return false, fmt.Errorf("marshal statusLine: %w", err)
	}
	rawSettings["statusLine"] = newRaw

	finalData, err := json.MarshalIndent(rawSettings, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal settings: %w", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	if err := atomicfile.WriteFile(settingsPath, finalData, 0o644); err != nil {
		return false, fmt.Errorf("write settings.json: %w", err)
	}

	sessionLog.Info("claude_statusline_installed", slog.String("config_dir", configDir))
	return true, nil
}

// RemoveClaudeStatusLine removes agent-deck's statusLine wrapper from
// settings.json, restoring whatever was configured before it (if
// anything). Leaves a non-agent-deck statusLine untouched. Returns true if
// agent-deck's entry was removed.
func RemoveClaudeStatusLine(configDir string) (bool, error) {
	settingsPath := filepath.Join(configDir, "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read settings.json: %w", err)
	}

	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		return false, fmt.Errorf("parse settings.json: %w", err)
	}

	existing, ok := rawSettings["statusLine"]
	if !ok {
		return false, nil
	}
	var cfg claudeStatusLineConfig
	if err := json.Unmarshal(existing, &cfg); err != nil || !strings.Contains(cfg.Command, agentDeckStatusLineCommand) {
		return false, nil // not ours; leave it alone
	}

	origPath := statusLineOriginalFilePath()
	if origData, readErr := os.ReadFile(origPath); readErr == nil {
		rawSettings["statusLine"] = json.RawMessage(origData)
		_ = os.Remove(origPath)
	} else {
		delete(rawSettings, "statusLine")
	}

	finalData, err := json.MarshalIndent(rawSettings, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal settings: %w", err)
	}
	if err := atomicfile.WriteFile(settingsPath, finalData, 0o644); err != nil {
		return false, fmt.Errorf("write settings.json: %w", err)
	}

	sessionLog.Info("claude_statusline_removed", slog.String("config_dir", configDir))
	return true, nil
}

// CheckClaudeStatusLineInstalled reports whether agent-deck's statusLine
// wrapper is currently the one configured in settings.json.
func CheckClaudeStatusLineInstalled(configDir string) bool {
	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		return false
	}
	raw, ok := rawSettings["statusLine"]
	if !ok {
		return false
	}
	var cfg claudeStatusLineConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	return strings.Contains(cfg.Command, agentDeckStatusLineCommand)
}

// OriginalStatusLineCommand returns the statusLine command that was
// configured before agent-deck installed its own (empty, exists=false if
// there wasn't one), for the runtime wrapper to chain to so it keeps
// working exactly as before.
func OriginalStatusLineCommand() (cmd string, exists bool) {
	data, err := readStatusFileNoFollow(statusLineOriginalFilePath())
	if err != nil {
		return "", false
	}
	var cfg claudeStatusLineConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Command == "" {
		return "", false
	}
	return cfg.Command, true
}

// validStatusLineSessionID matches Claude's session_id (a UUID in
// practice) — mirrors cmd/agent-deck's validInstanceID guard against path
// traversal via a crafted/malformed id before it reaches a file path.
var validStatusLineSessionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// StatusLineContext is what the `agent-deck statusline` CLI entrypoint
// persists per Claude session on every statusLine invocation (i.e. after
// every turn) — sourced directly from Claude Code's own payload fields
// (model.id, context_window.*, cost.total_cost_usd), not recomputed.
type StatusLineContext struct {
	SessionID         string    `json:"session_id"`
	Model             string    `json:"model,omitempty"` // raw model.id, e.g. "claude-sonnet-5"
	ContextWindowSize int       `json:"context_window_size,omitempty"`
	UsedPercentage    float64   `json:"used_percentage"`
	CostUSD           float64   `json:"cost_usd,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// StatusLineContextFilePath returns the file a given Claude session's
// context gets written to / read from. Empty for an invalid session id
// (caller should treat that as "no data").
func StatusLineContextFilePath(claudeSessionID string) string {
	if !validStatusLineSessionID.MatchString(claudeSessionID) || strings.Contains(claudeSessionID, "..") {
		return ""
	}
	return filepath.Join(GetHooksDir(), "statusline-"+claudeSessionID+".json")
}

// WriteStatusLineContext atomically persists ctx, stamping UpdatedAt. Called
// only by the `agent-deck statusline` CLI entrypoint — never by the
// web/TUI processes, which only read.
func WriteStatusLineContext(ctx StatusLineContext) error {
	path := StatusLineContextFilePath(ctx.SessionID)
	if path == "" {
		return fmt.Errorf("invalid session id %q", ctx.SessionID)
	}
	ctx.UpdatedAt = time.Now()
	data, err := json.Marshal(ctx)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o600)
}

// ReadStatusLineContext reads back a previously-written context for a
// Claude session. ok is false when the file is absent (statusLine not
// installed yet, or no turn has completed since install), unreadable, or
// the session id is invalid. No time-based staleness check: the persisted
// percentage/cost stay true until the NEXT turn writes a fresh value —
// there is no "went stale while idle" the way there is for a cached
// transcript re-parse.
func ReadStatusLineContext(claudeSessionID string) (StatusLineContext, bool) {
	path := StatusLineContextFilePath(claudeSessionID)
	if path == "" {
		return StatusLineContext{}, false
	}
	data, err := readStatusFileNoFollow(path)
	if err != nil {
		return StatusLineContext{}, false
	}
	var ctx StatusLineContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return StatusLineContext{}, false
	}
	return ctx, true
}
