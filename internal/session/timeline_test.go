package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTimelineTranscript writes lines (already-marshaled JSONL) to a temp file and
// returns its path.
func writeTimelineTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestParseTranscriptTimeline_PromptEvent(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"user","timestamp":"2026-08-19T13:00:00Z","message":{"content":"Add health badges please"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 || events[0].Kind != TimelineKindPrompt {
		t.Fatalf("expected 1 prompt event, got %+v", events)
	}
	if events[0].Text != "Add health badges please" {
		t.Errorf("Text = %q", events[0].Text)
	}
}

func TestParseTranscriptTimeline_ToolResultEntryIsNotAPrompt(t *testing.T) {
	// A user-role entry whose content is a tool_result block (Claude's way
	// of threading a tool's output back into the conversation) must NOT be
	// mistaken for something the human typed.
	path := writeTimelineTranscript(t,
		`{"type":"user","timestamp":"2026-08-19T13:00:05Z","message":{"content":[{"type":"tool_result","content":"ok"}]},"toolUseResult":{"stdout":"ok"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events (tool result, not a prompt), got %+v", events)
	}
}

func TestParseTranscriptTimeline_MetaEntryIgnored(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"user","isMeta":true,"timestamp":"2026-08-19T13:00:00Z","message":{"content":"<system-reminder>...</system-reminder>"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected meta entries excluded, got %+v", events)
	}
}

func TestParseTranscriptTimeline_CompactSummaryEvent(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"user","isCompactSummary":true,"timestamp":"2026-08-19T14:00:00Z","message":{"content":"This session is being continued from a previous conversation..."}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 || events[0].Kind != TimelineKindCompact {
		t.Fatalf("expected 1 compact event, got %+v", events)
	}
	// The compact summary's own continuation text must not ALSO surface as
	// a prompt — it's the same entry, not two.
	if events[0].Text != "" {
		t.Errorf("expected no prompt text alongside the compact marker, got %q", events[0].Text)
	}
}

func TestParseTranscriptTimeline_EditEvent(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"assistant","timestamp":"2026-08-19T13:05:00Z","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"internal/git/health.go","old_string":"a","new_string":"b"}}]}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 || events[0].Kind != TimelineKindEdit {
		t.Fatalf("expected 1 edit event, got %+v", events)
	}
	if events[0].Path != "internal/git/health.go" {
		t.Errorf("Path = %q", events[0].Path)
	}
}

func TestParseTranscriptTimeline_NonEditToolCallIgnored(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"assistant","timestamp":"2026-08-19T13:05:00Z","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"foo.go"}}]}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected Read calls to not appear on the timeline, got %+v", events)
	}
}

func TestParseTranscriptTimeline_GHPRCreateEventPicksUpPRNumberFromStdout(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"assistant","timestamp":"2026-08-19T13:10:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"gh pr create --title x --body y"}}]}}`,
		`{"type":"user","timestamp":"2026-08-19T13:10:02Z","message":{"content":[{"type":"tool_result","content":"https://github.com/org/repo/pull/6"}]},"toolUseResult":{"stdout":"https://github.com/org/repo/pull/6"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 || events[0].Kind != TimelineKindPR {
		t.Fatalf("expected 1 pr event, got %+v", events)
	}
	if events[0].Text != "opened PR · PR #6" {
		t.Errorf("Text = %q, want it to include the PR number pulled from stdout", events[0].Text)
	}
}

func TestParseTranscriptTimeline_GHPRMergeEventWithoutStdoutStillEmits(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"assistant","timestamp":"2026-08-19T13:10:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"gh pr merge --squash"}}]}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 || events[0].Kind != TimelineKindPR || events[0].Text != "merged PR" {
		t.Fatalf("expected 1 best-effort pr event, got %+v", events)
	}
}

func TestParseTranscriptTimeline_OtherGHCommandsIgnored(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"assistant","timestamp":"2026-08-19T13:10:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"gh pr view 6"}}]}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected non create/merge gh pr commands to not appear, got %+v", events)
	}
}

func TestParseTranscriptTimeline_SlashCommandScaffoldingFiltered(t *testing.T) {
	// All three of these are real entries seen on a live session running
	// /compact — none of them is something the human actually asked for.
	path := writeTimelineTranscript(t,
		`{"type":"user","timestamp":"2026-08-19T21:58:57Z","message":{"content":"/compact"}}`,
		`{"type":"user","timestamp":"2026-08-19T21:58:58Z","message":{"content":"<command-name>/compact</command-name>\n            <command-message>compact</command-message>"}}`,
		`{"type":"user","timestamp":"2026-08-19T22:01:53Z","message":{"content":"<local-command-stdout>Compacted</local-command-stdout>"}}`,
		`{"type":"user","timestamp":"2026-08-19T22:44:24Z","message":{"content":"Puedes hacer commit y push al pr"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected only the real prompt to survive, got %+v", events)
	}
	if events[0].Text != "Puedes hacer commit y push al pr" {
		t.Errorf("Text = %q", events[0].Text)
	}
}

func TestParseTranscriptTimeline_LongPromptTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)
	path := writeTimelineTranscript(t,
		`{"type":"user","timestamp":"2026-08-19T13:00:00Z","message":{"content":"`+long+`"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if len(events[0].Text) > maxTimelinePromptLen+len("…") {
		t.Errorf("expected prompt text truncated to ~%d chars, got %d", maxTimelinePromptLen, len(events[0].Text))
	}
	if !strings.HasSuffix(events[0].Text, "…") {
		t.Errorf("expected truncated text to end with an ellipsis, got %q", events[0].Text[len(events[0].Text)-10:])
	}
}

func TestParseTranscriptTimeline_ChronologicalOrderPreserved(t *testing.T) {
	path := writeTimelineTranscript(t,
		`{"type":"user","timestamp":"2026-08-19T13:00:00Z","message":{"content":"first"}}`,
		`{"type":"assistant","timestamp":"2026-08-19T13:00:05Z","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","timestamp":"2026-08-19T13:01:00Z","message":{"content":"second"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %+v", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Time.Before(events[i-1].Time) {
			t.Errorf("events out of order at index %d: %+v", i, events)
		}
	}
}

func TestParseTranscriptTimeline_MissingFileReturnsError(t *testing.T) {
	_, err := ParseTranscriptTimeline(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestParseTranscriptTimeline_MalformedLinesSkipped(t *testing.T) {
	path := writeTimelineTranscript(t,
		`not json at all`,
		`{"type":"user","timestamp":"2026-08-19T13:00:00Z","message":{"content":"ok"}}`,
	)
	events, err := ParseTranscriptTimeline(path)
	if err != nil {
		t.Fatalf("ParseTranscriptTimeline: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected the malformed line skipped and the valid one kept, got %+v", events)
	}
}
