package web

import "github.com/asheshgoplani/agent-deck/internal/session"

// Error code constants for API error responses.
const (
	ErrCodeUnauthorized     = "UNAUTHORIZED"
	ErrCodeForbidden        = "MUTATIONS_DISABLED"
	ErrCodeCSRF             = "CROSS_ORIGIN_BLOCKED"
	ErrCodeNotFound         = "NOT_FOUND"
	ErrCodeBadRequest       = "INVALID_REQUEST"
	ErrCodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	ErrCodeRateLimited      = "RATE_LIMITED"
	ErrCodeInternalError    = "INTERNAL_ERROR"
	ErrCodeNotImplemented   = "NOT_IMPLEMENTED"
	ErrCodeReadOnly         = "READ_ONLY"
)

// CreateSessionRequest is the body for POST /api/sessions. Fields below
// Title/Tool/ProjectPath mirror what the TUI's New Session dialog
// (internal/ui/newdialog.go) has always offered — the web dialog grew a
// matching UI for them (Sidebar/CreateSessionDialog.js) so the two stay
// at parity instead of the web being a stripped-down subset.
type CreateSessionRequest struct {
	Title           string `json:"title"`
	Tool            string `json:"tool"`
	ProjectPath     string `json:"projectPath"`
	GroupPath       string `json:"groupPath,omitempty"`
	ModelID         string `json:"modelId,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`

	// Worktree: create the session in an isolated git/jj worktree on its own
	// branch instead of the shared working directory. Branch is required
	// when Worktree is true; a non-repo ProjectPath falls back to a normal
	// session (same #1185 behavior the TUI has) rather than erroring.
	Worktree bool   `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`

	// Sandbox runs the session inside a Docker container (session.SandboxConfig
	// defaults — no per-request image/limit overrides yet, matching the TUI
	// dialog's own plain on/off checkbox).
	Sandbox bool `json:"sandbox,omitempty"`

	// MultiRepo attaches AdditionalPaths to ProjectPath as one multi-repo
	// session (symlinked, or worktree-cloned per-repo when Worktree is also
	// set). Ignored when AdditionalPaths is empty.
	MultiRepo       bool     `json:"multiRepo,omitempty"`
	AdditionalPaths []string `json:"additionalPaths,omitempty"`

	// Claude carries Claude-specific launch options. Ignored for other tools.
	Claude *ClaudeSessionOptions `json:"claude,omitempty"`
}

// ClaudeSessionOptions mirrors session.ClaudeOptions' launch-time fields —
// see internal/ui/claudeoptions.go for the TUI panel this parallels.
//
// The four mode toggles are *bool, not bool: a plain bool cannot
// distinguish "the user left this untouched" from "the user explicitly
// turned it off," and the dialog only ever sends a toggle the user actually
// interacted with (see CreateSessionDialog.js's per-toggle `touched`
// tracking). A nil pointer here means "use the config-derived default from
// session.NewClaudeOptions" (internal/ui/web_mutator.go
// buildCreateSessionToolOptions) instead of silently overriding it with
// false — see the web session permission-defaults fix.
type ClaudeSessionOptions struct {
	// SessionMode: "new" (default), "continue", or "resume".
	SessionMode string `json:"sessionMode,omitempty"`
	// ResumeSessionID is required when SessionMode is "resume".
	ResumeSessionID string `json:"resumeSessionId,omitempty"`
	SkipPermissions *bool  `json:"skipPermissions,omitempty"`
	AutoMode        *bool  `json:"autoMode,omitempty"`
	UseChrome       *bool  `json:"useChrome,omitempty"`
	UseTeammateMode *bool  `json:"useTeammateMode,omitempty"`
	// ExtraArgs is whitespace-split into CLI tokens, same convention as the
	// TUI's extra-args input (internal/ui/claudeoptions.go GetExtraArgs).
	ExtraArgs string `json:"extraArgs,omitempty"`
	// StartQuery is Claude Code's positional startup query — held as one
	// string and NEVER split on spaces.
	StartQuery string `json:"startQuery,omitempty"`
}

// CreateGroupRequest is the body for POST /api/groups.
type CreateGroupRequest struct {
	Name       string `json:"name"`
	ParentPath string `json:"parentPath,omitempty"`
}

// RenameGroupRequest is the body for PATCH /api/groups/:path.
type RenameGroupRequest struct {
	Name string `json:"name"`
}

// UpdateSessionRequest is the body for PATCH /api/sessions/{id}. Every field
// is optional; only the fields present in the request body are updated.
// Pointer types let the handler distinguish "not supplied" from "set to zero
// value" — important for booleans, where a missing field must not silently
// clear the flag.
//
// Field names mirror session.Field* constants so the handler can dispatch
// directly through session.SetField without a translation table.
type UpdateSessionRequest struct {
	Title           *string `json:"title,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	Color           *string `json:"color,omitempty"`
	Tool            *string `json:"tool,omitempty"`
	ExtraArgs       *string `json:"extraArgs,omitempty"`
	Plugins         *string `json:"plugins,omitempty"`
	Channels        *string `json:"channels,omitempty"`
	SkipPermissions *bool   `json:"skipPermissions,omitempty"`
	AutoMode        *bool   `json:"autoMode,omitempty"`
}

// UpdateSessionResponse confirms a PATCH succeeded. RestartRequired is true
// when any updated field only takes effect on next launch (tool, extra-args,
// plugins, skip-permissions, auto-mode). Clients use it to prompt before/after
// issuing a separate POST .../restart.
type UpdateSessionResponse struct {
	SessionID       string   `json:"sessionId"`
	UpdatedFields   []string `json:"updatedFields"`
	RestartRequired bool     `json:"restartRequired"`
}

// SessionActionResponse is returned by session action endpoints.
type SessionActionResponse struct {
	SessionID string         `json:"sessionId"`
	Status    session.Status `json:"status"`
}

// WorktreeFinishRequest is the body for POST /api/sessions/{id}/worktree/finish.
// All fields are optional. Mirrors `agent-deck worktree finish` CLI flags.
// See issue #1126.
type WorktreeFinishRequest struct {
	Into       string `json:"into,omitempty"`
	NoMerge    bool   `json:"noMerge,omitempty"`
	KeepBranch bool   `json:"keepBranch,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

// WorktreeFinishResponse is returned by POST /api/sessions/{id}/worktree/finish.
type WorktreeFinishResponse struct {
	SessionID     string `json:"sessionId"`
	Branch        string `json:"branch"`
	MergedInto    string `json:"mergedInto,omitempty"`
	Merged        bool   `json:"merged"`
	BranchDeleted bool   `json:"branchDeleted"`
}

// SettingsResponse is returned by GET /api/settings.
type SettingsResponse struct {
	Profile      string `json:"profile"`
	ReadOnly     bool   `json:"readOnly"`
	WebMutations bool   `json:"webMutations"`
	Version      string `json:"version"`

	// show_only_installed_tools filter (issue #1259). ToolFilter reports the
	// flag is on; VisibleTools lists the tool names that resolved on PATH (the
	// web dialog intersects its static list against this); ToolFilterFallback
	// reports the empty-fallback so the dialog shows a "showing all" hint. With
	// the flag off ToolFilter is false and the dialog ignores the other fields.
	ToolFilter         bool     `json:"toolFilter"`
	VisibleTools       []string `json:"visibleTools"`
	ToolFilterFallback bool     `json:"toolFilterFallback"`

	// hidden_tools denylist from [ui]. HiddenTools is the configured list;
	// PickerTools is the ordered new-session picker after hidden_tools and
	// show_only_installed_tools ("" mapped to "shell" for web).
	HiddenTools []string `json:"hiddenTools"`
	PickerTools []string `json:"pickerTools"`

	// Link-open policy for the web terminal (issue #1682). TrustedDomains
	// are normalized hosts whose links open without a confirm;
	// ConfirmLinkOpen reports whether every other host still confirms.
	TrustedDomains  []string `json:"trustedDomains"`
	ConfirmLinkOpen bool     `json:"confirmLinkOpen"`

	// ClaudeDefaults mirrors config.toml's [claude] launch defaults, the
	// same values the TUI's New Session dialog seeds itself with
	// (internal/ui/claudeoptions.go ClaudeOptionsPanel.SetDefaults). The web
	// dialog (CreateSessionDialog.js) reads this to seed its own toggles
	// instead of hardcoding them to false — see #(web session permission
	// defaults) fix.
	ClaudeDefaults ClaudeDefaults `json:"claudeDefaults"`
}

// ClaudeDefaults is the config-derived subset of SettingsResponse used to
// seed the New Session dialog's Claude option toggles.
type ClaudeDefaults struct {
	SkipPermissions      bool `json:"skipPermissions"`
	AllowSkipPermissions bool `json:"allowSkipPermissions"`
	AutoMode             bool `json:"autoMode"`
	UseChrome            bool `json:"useChrome"`
	UseTeammateMode      bool `json:"useTeammateMode"`
}

// ProfilesResponse is returned by GET /api/profiles.
type ProfilesResponse struct {
	Current  string   `json:"current"`
	Profiles []string `json:"profiles"`
}

// SSESessionEvent is emitted on session:created and session:updated events.
type SSESessionEvent struct {
	EventType string       `json:"eventType"`
	Session   *MenuSession `json:"session"`
}

// SSEDeleteEvent is emitted on session:deleted events.
type SSEDeleteEvent struct {
	EventType string `json:"eventType"`
	ID        string `json:"id"`
}

// SSEGroupEvent is emitted on group:created and group:updated events.
type SSEGroupEvent struct {
	EventType string     `json:"eventType"`
	Group     *MenuGroup `json:"group"`
}

// SSEGroupDeleteEvent is emitted on group:deleted events.
type SSEGroupDeleteEvent struct {
	EventType string `json:"eventType"`
	Path      string `json:"path"`
}

// SSECostEvent is emitted on cost:updated events.
type SSECostEvent struct {
	EventType string  `json:"eventType"`
	SessionID string  `json:"sessionId"`
	Cost      float64 `json:"cost"`
}
