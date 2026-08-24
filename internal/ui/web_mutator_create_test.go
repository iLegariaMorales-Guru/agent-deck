package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/docker"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/web"
)

// TestBuildCreateSessionToolOptions_Claude covers how a web CreateSessionRequest's
// tool-specific fields (session mode, checkboxes, extra args, start query,
// reasoning effort) get folded into the same ClaudeOptions JSON the TUI's
// newdialog.go GetClaudeOptions produces.
func TestBuildCreateSessionToolOptions_Claude(t *testing.T) {
	req := web.CreateSessionRequest{
		Tool:            "claude",
		ReasoningEffort: "high",
		Claude: &web.ClaudeSessionOptions{
			SessionMode:     "resume",
			ResumeSessionID: "11111111-2222-3333-4444-555555555555",
			SkipPermissions: boolPtr(true),
			AutoMode:        boolPtr(false),
			UseChrome:       boolPtr(true),
			UseTeammateMode: boolPtr(false),
			ExtraArgs:       "--agent reviewer --model opus",
			StartQuery:      "fix the failing test",
		},
	}

	raw, extraArgs, startQuery, err := buildCreateSessionToolOptions(req)
	if err != nil {
		t.Fatalf("buildCreateSessionToolOptions: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty tool options JSON for claude")
	}

	opts, err := session.UnmarshalClaudeOptions(raw)
	if err != nil {
		t.Fatalf("unmarshal claude options: %v", err)
	}
	if opts.SessionMode != "resume" {
		t.Errorf("SessionMode = %q, want resume", opts.SessionMode)
	}
	if opts.ResumeSessionID != req.Claude.ResumeSessionID {
		t.Errorf("ResumeSessionID = %q, want %q", opts.ResumeSessionID, req.Claude.ResumeSessionID)
	}
	if !opts.SkipPermissions {
		t.Error("SkipPermissions = false, want true")
	}
	if !opts.UseChrome {
		t.Error("UseChrome = false, want true")
	}
	if opts.Effort != "high" {
		t.Errorf("Effort = %q, want high", opts.Effort)
	}
	wantArgs := []string{"--agent", "reviewer", "--model", "opus"}
	if len(extraArgs) != len(wantArgs) {
		t.Fatalf("extraArgs = %v, want %v", extraArgs, wantArgs)
	}
	for i, a := range wantArgs {
		if extraArgs[i] != a {
			t.Errorf("extraArgs[%d] = %q, want %q", i, extraArgs[i], a)
		}
	}
	if startQuery != "fix the failing test" {
		t.Errorf("startQuery = %q, want %q", startQuery, "fix the failing test")
	}
}

// TestBuildCreateSessionToolOptions_Claude_PreservesUntouchedDefaults covers
// the web session-creation permission-mode bug: picking any one Claude field
// (here, SessionMode) must not silently reset the OTHER four mode toggles to
// false. Before ClaudeSessionOptions' bool fields became *bool, a plain bool
// could not distinguish "the user left this untouched" from "explicitly
// false," so buildCreateSessionToolOptions unconditionally overwrote all four
// with their Go zero value the moment req.Claude was non-nil for any reason
// — clobbering a configured dangerous_mode=true default with false.
func TestBuildCreateSessionToolOptions_Claude_PreservesUntouchedDefaults(t *testing.T) {
	req := web.CreateSessionRequest{
		Tool: "claude",
		Claude: &web.ClaudeSessionOptions{
			SessionMode:     "resume",
			ResumeSessionID: "11111111-2222-3333-4444-555555555555",
			// SkipPermissions/AutoMode/UseChrome/UseTeammateMode left nil:
			// the user only touched SessionMode.
		},
	}

	userConfig, _ := session.LoadUserConfig()
	want := session.NewClaudeOptions(userConfig)

	raw, _, _, err := buildCreateSessionToolOptions(req)
	if err != nil {
		t.Fatalf("buildCreateSessionToolOptions: %v", err)
	}
	got, err := session.UnmarshalClaudeOptions(raw)
	if err != nil {
		t.Fatalf("unmarshal claude options: %v", err)
	}

	if got.SkipPermissions != want.SkipPermissions {
		t.Errorf("SkipPermissions = %v, want %v (config default, untouched by request)", got.SkipPermissions, want.SkipPermissions)
	}
	if got.AutoMode != want.AutoMode {
		t.Errorf("AutoMode = %v, want %v (config default, untouched by request)", got.AutoMode, want.AutoMode)
	}
	if got.UseChrome != want.UseChrome {
		t.Errorf("UseChrome = %v, want %v (config default, untouched by request)", got.UseChrome, want.UseChrome)
	}
	if got.UseTeammateMode != want.UseTeammateMode {
		t.Errorf("UseTeammateMode = %v, want %v (config default, untouched by request)", got.UseTeammateMode, want.UseTeammateMode)
	}
}

// TestBuildCreateSessionToolOptions_ClaudeDefaultsToNewSession covers the
// plain "New session" case (no req.Claude payload at all — the web dialog
// only sends the `claude` object when at least one option differs from
// default) still gets SessionMode "new" so the launch command doesn't
// silently pick up "-c"/"-r".
func TestBuildCreateSessionToolOptions_ClaudeDefaultsToNewSession(t *testing.T) {
	raw, extraArgs, startQuery, err := buildCreateSessionToolOptions(web.CreateSessionRequest{Tool: "claude"})
	if err != nil {
		t.Fatalf("buildCreateSessionToolOptions: %v", err)
	}
	opts, err := session.UnmarshalClaudeOptions(raw)
	if err != nil {
		t.Fatalf("unmarshal claude options: %v", err)
	}
	if opts.SessionMode != "new" {
		t.Errorf("SessionMode = %q, want new", opts.SessionMode)
	}
	if extraArgs != nil {
		t.Errorf("extraArgs = %v, want nil", extraArgs)
	}
	if startQuery != "" {
		t.Errorf("startQuery = %q, want empty", startQuery)
	}
}

// TestBuildCreateSessionToolOptions_Codex covers the reasoning-effort ->
// CodexOptions.ReasoningEffort mapping, matching home.go's own codex branch.
func TestBuildCreateSessionToolOptions_Codex(t *testing.T) {
	raw, _, _, err := buildCreateSessionToolOptions(web.CreateSessionRequest{Tool: "codex", ReasoningEffort: "xhigh"})
	if err != nil {
		t.Fatalf("buildCreateSessionToolOptions: %v", err)
	}
	opts, err := session.UnmarshalCodexOptions(raw)
	if err != nil {
		t.Fatalf("unmarshal codex options: %v", err)
	}
	if opts.ReasoningEffort != "xhigh" {
		t.Errorf("ReasoningEffort = %q, want xhigh", opts.ReasoningEffort)
	}
	if opts.YoloMode == nil || *opts.YoloMode {
		t.Error("YoloMode should default to a set-but-false pointer for codex")
	}
}

// TestBuildCreateSessionToolOptions_OtherToolsNoOp covers that a non-claude,
// non-codex tool (e.g. plain shell) gets no tool-options payload at all.
func TestBuildCreateSessionToolOptions_OtherToolsNoOp(t *testing.T) {
	raw, extraArgs, startQuery, err := buildCreateSessionToolOptions(web.CreateSessionRequest{Tool: "gemini"})
	if err != nil {
		t.Fatalf("buildCreateSessionToolOptions: %v", err)
	}
	if raw != nil {
		t.Errorf("toolOptionsJSON = %s, want nil for gemini", raw)
	}
	if extraArgs != nil || startQuery != "" {
		t.Errorf("expected no extraArgs/startQuery for gemini, got %v / %q", extraArgs, startQuery)
	}
}

// findCreatedInstance reloads storage to find the instance CreateSession just
// persisted. WebMutator.CreateSession deliberately does not also update
// Home's in-memory instances/instanceByID maps (see its doc comment on the
// storage.SaveWithGroups call): in real headless usage the NEXT request's
// beginHeadlessTx re-hydrates from storage, so nothing needs that map updated
// synchronously here — but it means a bare &Home{} test can't read the new
// session straight back out of h.instanceByID like the live TUI can.
func findCreatedInstance(t *testing.T, h *Home, id string) *session.Instance {
	t.Helper()
	storage, err := session.NewStorageWithProfile(h.profile)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer storage.Close()
	instances, err := storage.Load()
	if err != nil {
		t.Fatalf("load storage: %v", err)
	}
	for _, inst := range instances {
		if inst.ID == id {
			return inst
		}
	}
	t.Fatalf("created session %q not found in storage", id)
	return nil
}

// TestWebMutatorCreateSession_GroupAndSandbox exercises the full CreateSession
// path (the shared TUI closure, invoked synchronously) with a group and the
// sandbox flag set, using a harmless "sleep" command instead of a real tool
// binary — same pattern as TestIssue1607_CreateSessionTitleLock.
func TestWebMutatorCreateSession_GroupAndSandbox(t *testing.T) {
	if !docker.IsDockerAvailable() || !docker.IsDaemonRunning(context.Background()) {
		t.Skip("docker daemon not available")
	}
	t.Setenv("HOME", t.TempDir())
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	h := &Home{}
	m := NewWebMutator(h)

	id, err := m.CreateSession(web.CreateSessionRequest{
		Title:       "web-create-test",
		Tool:        "sleep 30",
		ProjectPath: t.TempDir(),
		GroupPath:   "web-tests",
		Sandbox:     true,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty session id")
	}

	inst := findCreatedInstance(t, h, id)
	t.Cleanup(func() {
		if err := inst.KillAndWait(); err != nil {
			t.Errorf("cleanup session: %v", err)
		}
	})

	if inst.GroupPath != "web-tests" {
		t.Errorf("GroupPath = %q, want web-tests", inst.GroupPath)
	}
	if !inst.IsSandboxed() {
		t.Error("IsSandboxed() = false, want true (Sandbox: true in request)")
	}
}

// TestWebMutatorCreateSession_Worktree exercises the worktree branch of
// CreateSession end-to-end against a real temp git repo, verifying the
// session lands on the new worktree/branch instead of the shared checkout.
func TestWebMutatorCreateSession_Worktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir())
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "initial")

	h := &Home{}
	m := NewWebMutator(h)

	id, err := m.CreateSession(web.CreateSessionRequest{
		Title:       "worktree-test",
		Tool:        "sleep 30",
		ProjectPath: repo,
		Worktree:    true,
		Branch:      "feature/web-worktree-test",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	inst := findCreatedInstance(t, h, id)
	t.Cleanup(func() {
		if err := inst.KillAndWait(); err != nil {
			t.Errorf("cleanup session: %v", err)
		}
	})

	if inst.WorktreeBranch != "feature/web-worktree-test" {
		t.Errorf("WorktreeBranch = %q, want feature/web-worktree-test", inst.WorktreeBranch)
	}
	if inst.WorktreePath == "" {
		t.Error("WorktreePath is empty, want a resolved worktree directory")
	}
	if inst.ProjectPath == repo {
		t.Error("ProjectPath still points at the shared repo, want the worktree path")
	}
}
