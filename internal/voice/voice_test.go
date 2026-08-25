package voice

import (
	"os"
	"path/filepath"
	"testing"
)

// withStatFn/withLookPathFn swap the package's test seams for the duration
// of one test and restore them on cleanup, so tests never depend on the
// real machine actually having whisper.cpp/ffmpeg installed.
func withStatFn(t *testing.T, exists map[string]bool) {
	t.Helper()
	orig := statFn
	statFn = func(name string) (os.FileInfo, error) {
		if exists[name] {
			return os.Stat(os.DevNull) // any real FileInfo works, callers only check err
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { statFn = orig })
}

func withLookPathFn(t *testing.T, found map[string]string) {
	t.Helper()
	orig := lookPathFn
	lookPathFn = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { lookPathFn = orig })
}

func TestDetect_AllExplicitPathsValid(t *testing.T) {
	withStatFn(t, map[string]bool{
		"/opt/whisper-cli":      true,
		"/opt/ggml-base.en.bin": true,
	})
	caps := Detect(Config{
		FFmpegBinary:  "/opt/ffmpeg", // explicit; Detect trusts it without stat-ing (matches PATH-found ffmpeg behavior)
		WhisperBinary: "/opt/whisper-cli",
		WhisperModel:  "/opt/ggml-base.en.bin",
	})
	if !caps.Available {
		t.Fatalf("expected Available=true, got Reason=%q", caps.Reason)
	}
	if caps.WhisperBinary != "/opt/whisper-cli" || caps.WhisperModel != "/opt/ggml-base.en.bin" {
		t.Fatalf("unexpected resolved paths: %+v", caps)
	}
}

func TestDetect_ConfiguredWhisperBinaryMissing(t *testing.T) {
	withStatFn(t, map[string]bool{})
	caps := Detect(Config{FFmpegBinary: "/opt/ffmpeg", WhisperBinary: "/opt/does-not-exist"})
	if caps.Available {
		t.Fatal("expected Available=false for a missing configured binary")
	}
	if caps.Reason == "" {
		t.Fatal("expected a non-empty Reason")
	}
}

func TestDetect_NoFFmpeg(t *testing.T) {
	withLookPathFn(t, map[string]string{})
	caps := Detect(Config{})
	if caps.Available {
		t.Fatal("expected Available=false with no ffmpeg on PATH")
	}
}

func TestDetect_AutoFindsBinaryOnPATH(t *testing.T) {
	withLookPathFn(t, map[string]string{
		"ffmpeg":      "/usr/bin/ffmpeg",
		"whisper-cli": "/usr/bin/whisper-cli",
	})
	withStatFn(t, map[string]bool{})

	// Model auto-search walks real directories (~/.local/share/whisper etc.)
	// via os.ReadDir, not statFn — point it at a throwaway HOME so the test
	// is hermetic regardless of what's actually installed on the machine
	// running it.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	modelDir := filepath.Join(tmpHome, ".local", "share", "whisper")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(modelDir, "ggml-base.en.bin")
	if err := os.WriteFile(modelPath, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}

	caps := Detect(Config{})
	if !caps.Available {
		t.Fatalf("expected Available=true, got Reason=%q", caps.Reason)
	}
	if caps.WhisperBinary != "/usr/bin/whisper-cli" {
		t.Fatalf("expected auto-found whisper-cli, got %q", caps.WhisperBinary)
	}
	if caps.WhisperModel != modelPath {
		t.Fatalf("expected auto-found model %q, got %q", modelPath, caps.WhisperModel)
	}
}

func TestDetect_NoModelFound(t *testing.T) {
	withLookPathFn(t, map[string]string{
		"ffmpeg":      "/usr/bin/ffmpeg",
		"whisper-cli": "/usr/bin/whisper-cli",
	})
	withStatFn(t, map[string]bool{})
	t.Setenv("HOME", t.TempDir()) // empty — no model dirs exist

	caps := Detect(Config{})
	if caps.Available {
		t.Fatal("expected Available=false with no model file found")
	}
}

func TestParseWhisperJSON_ConcatenatesAndTrims(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.json")
	fixture := `{"transcription":[{"text":" revisa el "},{"text":"bug del login "}]}`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	text, err := parseWhisperJSON(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "revisa el bug del login"
	if text != want {
		t.Fatalf("got %q, want %q", text, want)
	}
}

func TestParseWhisperJSON_EmptyTranscriptionYieldsEmptyString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.json")
	if err := os.WriteFile(path, []byte(`{"transcription":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	text, err := parseWhisperJSON(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty string, got %q", text)
	}
}

func TestExtensionForContentType(t *testing.T) {
	cases := map[string]string{
		"audio/mp4":                ".m4a",
		"audio/webm":               ".webm",
		"audio/webm;codecs=opus":   ".webm",
		"audio/ogg;codecs=opus":    ".ogg",
		"audio/wav":                ".wav",
		"":                         ".bin",
		"application/octet-stream": ".bin",
	}
	for in, want := range cases {
		if got := extensionForContentType(in); got != want {
			t.Errorf("extensionForContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranscribe_NotConfiguredWhenUnavailable(t *testing.T) {
	_, err := Transcribe(nil, Capabilities{Available: false}, []byte("audio"), "audio/webm")
	if err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestTranscribe_EmptyAudioRejectedBeforeShellingOut(t *testing.T) {
	caps := Capabilities{Available: true, WhisperBinary: "x", WhisperModel: "y", FFmpegBinary: "z"}
	_, err := Transcribe(nil, caps, nil, "audio/webm")
	if err != ErrEmptyAudio {
		t.Fatalf("expected ErrEmptyAudio, got %v", err)
	}
}
