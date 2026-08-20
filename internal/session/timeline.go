package session

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"
)

// TimelineEvent is one item in a session's activity timeline: what the user
// asked for, and what happened as a result (files edited, a PR opened/
// merged, a compact boundary). See ParseTranscriptTimeline for how these are
// derived from the Claude transcript; internal/git.RecentCommits and
// RecentPushes supply the other two kinds ("commit"/"push") from git
// directly — internal/web/handlers_timeline.go merges all four into one
// sorted feed.
type TimelineEvent struct {
	Kind string    `json:"kind"` // prompt | edit | pr | compact
	Time time.Time `json:"time"`
	Text string    `json:"text,omitempty"` // prompt text (truncated) / PR summary
	Path string    `json:"path,omitempty"` // file path, "edit" events only
}

const (
	TimelineKindPrompt  = "prompt"
	TimelineKindEdit    = "edit"
	TimelineKindPR      = "pr"
	TimelineKindCompact = "compact"
)

// maxTimelinePromptLen caps how much of a pasted prompt gets stored/sent —
// the UI clamps to ~2 lines visually anyway, this just keeps a giant paste
// from bloating the response.
const maxTimelinePromptLen = 300

// timelineEntry is a second, independent decode of the same JSONL format
// ParseSessionJSONL reads (see analytics.go's jsonlEntry) — kept as its own
// type rather than extending that one so a change here can't perturb the
// token/cost math analytics.go is responsible for.
type timelineEntry struct {
	Type             string          `json:"type"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Timestamp        time.Time       `json:"timestamp"`
	ToolUseResult    struct {
		Stdout string `json:"stdout"`
	} `json:"toolUseResult"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock covers the shapes actually used here: assistant tool_use
// blocks (Name + Input) and plain text blocks. tool_result blocks are
// matched by Type alone — their payload isn't needed, only that seeing one
// means this entry is a tool result being threaded back, not something a
// human typed.
type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ghPRCommandRE matches a Bash tool call running `gh pr create` or
// `gh pr merge` — the only two shapes this timeline treats as a "pr" event.
// Deliberately narrow: a coding agent runs plenty of other `gh` commands
// (view, checks, comment), and surfacing all of them would just be
// bash-command noise, not a real project-history event.
var ghPRCommandRE = regexp.MustCompile(`(?i)^\s*gh\s+pr\s+(create|merge)\b`)

// prURLRE pulls a PR number out of `gh pr create`/`gh pr merge`'s own
// stdout (e.g. "https://github.com/org/repo/pull/42") so the event can say
// "PR #42" instead of just echoing the command.
var prURLRE = regexp.MustCompile(`/pull/(\d+)`)

// ParseTranscriptTimeline reads a Claude session JSONL transcript and
// returns prompt/edit/pr/compact events in the order they occurred. Same
// append-only file ParseSessionJSONL already parses for cost/context on
// every poll — this is a separate, lazy (only-when-the-panel-is-open)
// pass over it, not part of that hot path. compacting a session's context
// does NOT remove any of the history this reads: /compact appends a
// summary turn (flagged IsCompactSummary here) rather than rewriting
// anything earlier in the file.
func ParseTranscriptTimeline(path string) ([]TimelineEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var events []TimelineEvent
	pendingPRIdx := -1 // index into events of a "pr" event still waiting for its command's stdout

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		var entry timelineEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // malformed line — skip, same tolerance as ParseSessionJSONL
		}

		switch entry.Type {
		case "assistant":
			for _, b := range decodeContentBlocks(entry.Message.Content) {
				if b.Type != "tool_use" {
					continue
				}
				switch b.Name {
				case "Edit", "Write", "NotebookEdit", "MultiEdit":
					if p := extractFilePath(b.Input); p != "" {
						events = append(events, TimelineEvent{Kind: TimelineKindEdit, Time: entry.Timestamp, Path: p})
					}
				case "Bash":
					cmd := extractCommand(b.Input)
					if m := ghPRCommandRE.FindStringSubmatch(cmd); m != nil {
						events = append(events, TimelineEvent{Kind: TimelineKindPR, Time: entry.Timestamp, Text: summarizeGHPRAction(m[1])})
						pendingPRIdx = len(events) - 1
					}
				}
			}

		case "user":
			if entry.IsCompactSummary {
				events = append(events, TimelineEvent{Kind: TimelineKindCompact, Time: entry.Timestamp})
				continue
			}
			// A pending "pr" event's command result lands as the very next
			// tool-result entry — patch in the real PR number if we can find
			// one, then stop waiting either way (only one shot at it).
			if pendingPRIdx >= 0 {
				if m := prURLRE.FindStringSubmatch(entry.ToolUseResult.Stdout); m != nil {
					events[pendingPRIdx].Text += " · PR #" + m[1]
				}
				pendingPRIdx = -1
			}
			if entry.IsMeta {
				continue
			}
			if text := extractPromptText(entry.Message.Content); text != "" && !isSlashCommandNoise(text) {
				events = append(events, TimelineEvent{Kind: TimelineKindPrompt, Time: entry.Timestamp, Text: truncatePrompt(text)})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func decodeContentBlocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil // content was a plain string, not a block array — nothing tool-shaped here
	}
	return blocks
}

// extractPromptText returns the human-typed text of a user-turn entry, or
// "" if this entry isn't one — either because it's a tool_result being
// threaded back (content contains a tool_result block) or because it
// carries no text at all.
func extractPromptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	blocks := decodeContentBlocks(raw)
	if blocks == nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return "" // this entry is a tool result, not a human prompt
		}
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// bareSlashCommandRE matches a lone slash command with no arguments, e.g.
// "/compact" or "/clear" — the literal text Claude Code threads through as
// the user turn when a slash command runs.
var bareSlashCommandRE = regexp.MustCompile(`^/\S+$`)

// isSlashCommandNoise reports whether text is Claude Code's own
// slash-command scaffolding rather than something a human actually typed:
// a bare "/command" invocation, the <command-name>/<command-message>
// wrapper it expands to, or the "<local-command-stdout>" confirmation
// echoed back afterward (see /compact in particular — running it produces
// all three of these as separate user-turn entries). Caught against a
// live session before this filter existed: see
// TestParseTranscriptTimeline_SlashCommandScaffoldingFiltered.
func isSlashCommandNoise(text string) bool {
	if bareSlashCommandRE.MatchString(text) {
		return true
	}
	return strings.HasPrefix(text, "<command-name>") || strings.HasPrefix(text, "<local-command-stdout>")
}

func extractFilePath(raw json.RawMessage) string {
	var v struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.FilePath
}

func extractCommand(raw json.RawMessage) string {
	var v struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.Command
}

func summarizeGHPRAction(action string) string {
	switch strings.ToLower(action) {
	case "create":
		return "opened PR"
	case "merge":
		return "merged PR"
	default:
		return "gh pr " + action
	}
}

func truncatePrompt(s string) string {
	if len(s) <= maxTimelinePromptLen {
		return s
	}
	return strings.TrimSpace(s[:maxTimelinePromptLen]) + "…"
}
