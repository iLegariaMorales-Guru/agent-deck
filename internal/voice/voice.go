// Package voice provides local speech-to-text for the web terminal's
// microphone button. Audio recorded in the browser (any container/codec
// MediaRecorder produces — mp4/AAC on Safari, webm/opus on Chrome, etc.) is
// normalized to 16kHz mono WAV via ffmpeg, then transcribed by a local
// whisper.cpp CLI (`whisper-cli`, or the older `whisper-cpp`/`main` binary
// names). No audio ever leaves the machine — this deliberately avoids a
// cloud STT API and its recurring cost/API-key/privacy tradeoffs.
//
// Neither ffmpeg nor whisper.cpp is a Go dependency; both are shelled out
// to, matching how this repo already integrates every other external tool
// (docker, git, jj, tmux) rather than linking a cgo binding.
package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotConfigured is returned (and reported via Capabilities.Reason) when
// ffmpeg or a whisper.cpp binary/model could not be found.
var ErrNotConfigured = errors.New("voice: whisper.cpp/ffmpeg not installed or not found")

// ErrEmptyAudio is returned when transcription completed but produced no
// text (silence, or audio too short/quiet for whisper to detect speech).
// Callers should treat this as a normal outcome, not a failure.
var ErrEmptyAudio = errors.New("voice: no speech detected")

// maxRecordingSeconds caps how much of the uploaded audio ffmpeg will
// process, as defense-in-depth alongside the client's own recording-length
// safeguard and the HTTP handler's body-size cap.
const maxRecordingSeconds = 60

// Config selects explicit binary/model paths. Any empty field is
// auto-detected by Detect. Comes from `[web] whisper_binary` /
// `whisper_model` in config.toml (session.GetWebWhisperBinary/Model) — see
// cmd/agent-deck/web_cmd.go.
type Config struct {
	WhisperBinary string
	WhisperModel  string
	FFmpegBinary  string
}

// Capabilities is the result of Detect: what was found (or not) at server
// startup. Cached for the process lifetime — installed binaries/models
// don't change without a restart.
type Capabilities struct {
	Available     bool
	WhisperBinary string
	WhisperModel  string
	FFmpegBinary  string
	// Reason explains why Available is false (empty when true). Surfaced to
	// the web UI via GET /api/settings so a missing install shows an
	// actionable message instead of a silent disappearing mic button.
	Reason string
}

// lookPathFn/statFn are test seams — voice_test.go swaps these to point at
// stub scripts/fake directories instead of requiring a real whisper.cpp
// install.
var (
	lookPathFn = exec.LookPath
	statFn     = os.Stat
)

// whisperBinaryNames covers every name the Homebrew formula and upstream
// whisper.cpp project have shipped the CLI under across versions.
var whisperBinaryNames = []string{"whisper-cli", "whisper-cpp", "main"}

// whisperBinaryDirs are checked directly (not just via PATH) since Homebrew
// installs to different prefixes on Apple Silicon vs Intel Macs, and a
// launchd-managed process's PATH is frequently a stripped-down default that
// doesn't include either.
var whisperBinaryDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// whisperModelDirs are searched, in order, for a ggml model file when
// Config.WhisperModel is empty.
var whisperModelDirs = []string{
	"~/.local/share/whisper",
	"/opt/homebrew/share/whisper-cpp",
	"/usr/local/share/whisper-cpp",
}

// preferredModelNames prefers multilingual models (small, then base) over
// the English-only ".en" variants: this server's user dictates in Spanish
// as well as English, and an ".en" model doesn't just transcribe Spanish
// speech poorly, it was trained only on English audio and effectively
// can't understand it at all. ".en" names are kept at the end purely as a
// fallback for an existing install that predates this — Transcribe always
// passes `-l auto`, so a multilingual model handles either language
// without the caller needing to know which one was spoken.
var preferredModelNames = []string{"ggml-small.bin", "ggml-base.bin", "ggml-small.en.bin", "ggml-base.en.bin"}

// Detect resolves cfg into concrete, verified binary/model paths, searching
// common install locations for anything left empty. Call once at server
// startup (internal/web.NewServer) and cache the result — this does real
// filesystem/PATH probing and isn't cheap enough to call per-request.
func Detect(cfg Config) Capabilities {
	ffmpeg := cfg.FFmpegBinary
	if ffmpeg == "" {
		if found, err := lookPathFn("ffmpeg"); err == nil {
			ffmpeg = found
		}
	}
	if ffmpeg == "" {
		return Capabilities{Reason: "ffmpeg not found on PATH (brew install ffmpeg)"}
	}

	whisper := cfg.WhisperBinary
	if whisper == "" {
		whisper = findWhisperBinary()
	} else if _, err := statFn(whisper); err != nil {
		return Capabilities{Reason: fmt.Sprintf("configured whisper_binary not found: %s", whisper)}
	}
	if whisper == "" {
		return Capabilities{Reason: "whisper.cpp CLI not found (brew install whisper-cpp)"}
	}

	model := cfg.WhisperModel
	if model == "" {
		model = findWhisperModel()
	} else if _, err := statFn(model); err != nil {
		return Capabilities{Reason: fmt.Sprintf("configured whisper_model not found: %s", model)}
	}
	if model == "" {
		return Capabilities{Reason: "no whisper.cpp model found (see CLAUDE.md voice input setup)"}
	}

	return Capabilities{
		Available:     true,
		WhisperBinary: whisper,
		WhisperModel:  model,
		FFmpegBinary:  ffmpeg,
	}
}

func findWhisperBinary() string {
	for _, name := range whisperBinaryNames {
		if found, err := lookPathFn(name); err == nil && found != "" {
			return found
		}
	}
	for _, dir := range whisperBinaryDirs {
		for _, name := range whisperBinaryNames {
			candidate := filepath.Join(dir, name)
			if _, err := statFn(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func findWhisperModel() string {
	home, _ := os.UserHomeDir()
	for _, dir := range whisperModelDirs {
		if strings.HasPrefix(dir, "~/") && home != "" {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
		for _, name := range preferredModelNames {
			candidate := filepath.Join(dir, name)
			if _, err := statFn(candidate); err == nil {
				return candidate
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && strings.HasPrefix(name, "ggml-") && strings.HasSuffix(name, ".bin") {
				return filepath.Join(dir, name)
			}
		}
	}
	return ""
}

// runCommand is a test seam for Transcribe's two subprocess calls.
var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Transcribe normalizes audioBytes (whatever container/codec the browser's
// MediaRecorder produced, identified by contentType) to 16kHz mono WAV via
// ffmpeg, runs it through the whisper.cpp CLI, and returns the transcript
// text. Returns ErrEmptyAudio (not an error condition for callers) when
// whisper produced no text. Scratch files are written under a per-call
// temp directory that is always removed before returning.
func Transcribe(ctx context.Context, caps Capabilities, audioBytes []byte, contentType string) (string, error) {
	if !caps.Available {
		return "", ErrNotConfigured
	}
	if len(audioBytes) == 0 {
		return "", ErrEmptyAudio
	}

	dir, err := os.MkdirTemp("", "agent-deck-voice-*")
	if err != nil {
		return "", fmt.Errorf("voice: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	inPath := filepath.Join(dir, "input"+extensionForContentType(contentType))
	if err := os.WriteFile(inPath, audioBytes, 0o600); err != nil {
		return "", fmt.Errorf("voice: write input audio: %w", err)
	}

	wavPath := filepath.Join(dir, "audio.wav")
	ffmpegCtx, cancelFFmpeg := context.WithTimeout(ctx, 20*time.Second)
	defer cancelFFmpeg()
	if out, err := runCommand(ffmpegCtx, caps.FFmpegBinary,
		"-y", "-nostdin", "-loglevel", "error",
		"-i", inPath,
		"-t", fmt.Sprintf("%d", maxRecordingSeconds),
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
		wavPath,
	); err != nil {
		return "", fmt.Errorf("voice: ffmpeg conversion failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	outPrefix := filepath.Join(dir, "transcript")
	whisperCtx, cancelWhisper := context.WithTimeout(ctx, 60*time.Second)
	defer cancelWhisper()
	if out, err := runCommand(whisperCtx, caps.WhisperBinary,
		"-m", caps.WhisperModel,
		"-f", wavPath,
		"-l", "auto", // auto-detect spoken language (multilingual models only --
		// an ".en" model ignores this and only ever understands English)
		"-nt", // no timestamps in output
		"-oj", // JSON output
		"-of", outPrefix,
	); err != nil {
		return "", fmt.Errorf("voice: whisper.cpp transcription failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	text, err := parseWhisperJSON(outPrefix + ".json")
	if err != nil {
		return "", fmt.Errorf("voice: parse transcript: %w", err)
	}
	if text == "" {
		return "", ErrEmptyAudio
	}
	return text, nil
}

// whisperJSONOutput mirrors whisper.cpp's `-oj` output shape:
// {"transcription": [{"text": "..."}, ...]}. Only the field we need is
// modeled; whisper.cpp's JSON also carries per-segment timestamps/offsets
// that are irrelevant here since transcription used `-nt`.
type whisperJSONOutput struct {
	Transcription []struct {
		Text string `json:"text"`
	} `json:"transcription"`
}

func parseWhisperJSON(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var parsed whisperJSONOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, seg := range parsed.Transcription {
		b.WriteString(seg.Text)
	}
	return strings.TrimSpace(b.String()), nil
}

// extensionForContentType picks a file extension matching the browser's
// MediaRecorder MIME type, so ffmpeg's demuxer probe has a hint instead of
// guessing purely from bytes.
func extensionForContentType(contentType string) string {
	base, _, _ := strings.Cut(contentType, ";")
	switch strings.TrimSpace(strings.ToLower(base)) {
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return ".wav"
	default:
		return ".bin"
	}
}
