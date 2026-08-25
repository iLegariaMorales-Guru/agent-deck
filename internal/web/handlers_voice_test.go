package web

// Tests for POST /api/sessions/{id}/transcribe (the web terminal's mic
// button backend). Per ~/.agent-deck/skills/pool/agent-deck-tdd-feature/SKILL.md
// convention (see handlers_skills_test.go): happy path, failure modes,
// boundary cases.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/voice"
)

// fakeVoiceTranscriber implements VoiceTranscriber without touching real
// whisper.cpp/ffmpeg. called records whether Transcribe was actually
// invoked, so tests can assert early-rejection paths (read-only, not
// configured) never reach the transcription call.
type fakeVoiceTranscriber struct {
	caps     voice.Capabilities
	text     string
	err      error
	called   bool
	lastBody []byte
	lastCT   string
}

func (f *fakeVoiceTranscriber) Capabilities() voice.Capabilities { return f.caps }

func (f *fakeVoiceTranscriber) Transcribe(_ context.Context, audio []byte, contentType string) (string, error) {
	f.called = true
	f.lastBody = audio
	f.lastCT = contentType
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

func newVoiceTestServer(t *testing.T, transcriber VoiceTranscriber) *Server {
	t.Helper()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.SetVoiceTranscriber(transcriber)
	return srv
}

func TestHandleSessionTranscribe_Happy(t *testing.T) {
	fake := &fakeVoiceTranscriber{
		caps: voice.Capabilities{Available: true},
		text: "revisa el bug del login",
	}
	srv := newVoiceTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("fake-audio-bytes")))
	req.Header.Set("Content-Type", "audio/webm;codecs=opus")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "revisa el bug del login") {
		t.Fatalf("response missing transcript: %s", rr.Body.String())
	}
	if !fake.called {
		t.Fatal("expected Transcribe to be called")
	}
	if fake.lastCT != "audio/webm;codecs=opus" {
		t.Fatalf("Content-Type not forwarded: got %q", fake.lastCT)
	}
	if string(fake.lastBody) != "fake-audio-bytes" {
		t.Fatalf("audio body not forwarded: got %q", fake.lastBody)
	}
}

func TestHandleSessionTranscribe_Unauthorized(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}}
	srv := newVoiceTestServer(t, fake)
	srv.cfg.Token = "secret-token"

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("audio")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rr.Code, rr.Body.String())
	}
	if fake.called {
		t.Fatal("expected Transcribe NOT to be called when unauthorized")
	}
}

func TestHandleSessionTranscribe_ReadOnlyRejectsWithoutCallingTranscriber(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}}
	srv := newVoiceTestServer(t, fake)
	srv.cfg.ReadOnly = true

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("audio")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeReadOnly) {
		t.Fatalf("expected %s in body: %s", ErrCodeReadOnly, rr.Body.String())
	}
	if fake.called {
		t.Fatal("expected Transcribe NOT to be called in read-only mode")
	}
}

func TestHandleSessionTranscribe_NotConfiguredSurfacesReason(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: false, Reason: "whisper.cpp CLI not found (brew install whisper-cpp)"}}
	srv := newVoiceTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("audio")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "brew install whisper-cpp") {
		t.Fatalf("expected reason surfaced in body: %s", rr.Body.String())
	}
	if fake.called {
		t.Fatal("expected Transcribe NOT to be called when not configured")
	}
}

func TestHandleSessionTranscribe_OversizedBodyRejected(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}}
	srv := newVoiceTestServer(t, fake)

	oversized := bytes.Repeat([]byte("a"), maxVoiceUploadBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader(oversized))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if fake.called {
		t.Fatal("expected Transcribe NOT to be called for an oversized body")
	}
}

func TestHandleSessionTranscribe_EmptyAudioReturns200WithEmptyText(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}, err: voice.ErrEmptyAudio}
	srv := newVoiceTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("silence")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"text":""`) {
		t.Fatalf("expected empty text field: %s", rr.Body.String())
	}
}

func TestHandleSessionTranscribe_TranscriptionErrorReturns500Sanitized(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}, err: errors.New("boom: /secret/path/leaked stderr detail")}
	srv := newVoiceTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("audio")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "/secret/path") {
		t.Fatalf("raw error detail leaked to client: %s", rr.Body.String())
	}
}

func TestHandleSessionTranscribe_NilTranscriberTreatedAsNotConfigured(t *testing.T) {
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0"})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.voice = nil // simulate a test-constructed Server that never wired one

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/transcribe", bytes.NewReader([]byte("audio")))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rr.Code, rr.Body.String())
	}
}

// Settings integration: /api/settings reports VoiceInputAvailable from the
// wired VoiceTranscriber's Capabilities.
func TestHandleSettings_ReportsVoiceInputAvailable(t *testing.T) {
	fake := &fakeVoiceTranscriber{caps: voice.Capabilities{Available: true}}
	srv := newVoiceTestServer(t, fake)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"voiceInputAvailable":true`) {
		t.Fatalf("expected voiceInputAvailable:true in settings: %s", rr.Body.String())
	}
}
