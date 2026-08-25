package web

// Voice input: POST /api/sessions/{id}/transcribe.
//
// The web terminal's mic button (VoiceRecorder.js) uploads a recorded audio
// blob here; the response text is then injected into the session's terminal
// input by the client, over the *existing* WebSocket `{type:"input"}` path
// (same mechanism paste already uses) — this handler never touches
// terminal_bridge.go itself.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/logging"
	"github.com/asheshgoplani/agent-deck/internal/voice"
)

// maxVoiceUploadBytes caps the recorded-audio request body. Generous
// headroom over a realistic 60s webm/opus or mp4/aac clip (a few hundred KB
// to ~2MB) — this is a safety cap, not a tuned limit.
const maxVoiceUploadBytes = 10 << 20 // 10MB

// VoiceTranscriber is the seam between the HTTP handler and internal/voice,
// so tests can inject a fake instead of requiring a real whisper.cpp/ffmpeg
// install. SetVoiceTranscriber wires the production implementation at
// startup (internal/web.NewServer); nil falls back to a "not configured"
// response rather than panicking.
type VoiceTranscriber interface {
	Transcribe(ctx context.Context, audio []byte, contentType string) (string, error)
	Capabilities() voice.Capabilities
}

// defaultVoiceTranscriber delegates straight to internal/voice, using the
// Capabilities computed once at server startup (voice.Detect is real
// filesystem/PATH probing, not something to redo per request).
type defaultVoiceTranscriber struct {
	caps voice.Capabilities
}

func (v defaultVoiceTranscriber) Transcribe(ctx context.Context, audio []byte, contentType string) (string, error) {
	return voice.Transcribe(ctx, v.caps, audio, contentType)
}

func (v defaultVoiceTranscriber) Capabilities() voice.Capabilities {
	return v.caps
}

// SetVoiceTranscriber injects an alternate VoiceTranscriber (used by tests).
func (s *Server) SetVoiceTranscriber(v VoiceTranscriber) {
	s.voice = v
}

type transcribeResponse struct {
	Text string `json:"text"`
}

// handleSessionTranscribe serves POST /api/sessions/{id}/transcribe.
func (s *Server) handleSessionTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}
	// This endpoint's sole purpose is producing terminal input, so it is
	// gated purely by ReadOnly — same as the WS `input` message type
	// (handlers_ws.go) — not by WebMutations (which governs persisted-state
	// mutations like sessions/groups/mcps, a different concern).
	if s.cfg.ReadOnly {
		writeAPIError(w, http.StatusForbidden, ErrCodeReadOnly, "input is disabled in read-only mode")
		return
	}

	transcriber := s.voice
	if transcriber == nil {
		transcriber = defaultVoiceTranscriber{caps: voice.Capabilities{Reason: "voice input is not configured on this server"}}
	}
	caps := transcriber.Capabilities()
	if !caps.Available {
		writeAPIError(w, http.StatusServiceUnavailable, ErrCodeVoiceNotConfigured, caps.Reason)
		return
	}

	// A single in-flight transcription at a time: whisper.cpp is CPU-heavy,
	// and two concurrent recordings (e.g. laptop tab + phone) would thrash
	// this machine rather than usefully parallelize. Reject immediately
	// (non-blocking) instead of queuing.
	select {
	case s.voiceSemaphore <- struct{}{}:
		defer func() { <-s.voiceSemaphore }()
	default:
		writeAPIError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "another transcription is already in progress")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxVoiceUploadBytes))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "audio upload too large or unreadable")
		return
	}
	if len(body) == 0 {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "no audio data received")
		return
	}

	// Generous ceiling covering both the ffmpeg and whisper.cpp subprocess
	// stages (20s + 60s internally); this is the outer bound in case either
	// hangs beyond its own timeout for some reason.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	text, err := transcriber.Transcribe(ctx, body, r.Header.Get("Content-Type"))
	if err != nil {
		if errors.Is(err, voice.ErrEmptyAudio) {
			writeJSON(w, http.StatusOK, transcribeResponse{Text: ""})
			return
		}
		logging.ForComponent(logging.CompWeb).Error("voice_transcribe_failed",
			slog.String("error", logging.SanitizeValue(err.Error())))
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "transcription failed")
		return
	}

	writeJSON(w, http.StatusOK, transcribeResponse{Text: text})
}
