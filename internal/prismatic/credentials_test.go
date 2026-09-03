package prismatic

import (
	"os"
	"path/filepath"
	"testing"
)

// resetCredentials clears every env/kind combination before a test runs.
// IsolateHome() (testmain_test.go) gives the whole package ONE shared temp
// HOME for its entire run, not a fresh one per test function, so a test
// asserting "nothing configured yet" must not rely on being first — it must
// clear its own slate. Errors are ignored: clearing an already-absent value
// is defined as a no-op (see TestCredentials_ClearingUnsetEnvIsNotAnError).
func resetCredentials(t *testing.T) {
	t.Helper()
	for env := range ValidEnvs {
		_ = ClearPrismToken(env)
		_ = ClearGuruCreds(env)
	}
}

func TestCredentials_SetLoadStatusClear_PrismToken(t *testing.T) {
	resetCredentials(t)

	status, err := LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus (empty): %v", err)
	}
	if status.Prism["qa"] || status.Prism["prod"] {
		t.Fatalf("expected no configured Prism tokens after reset, got %+v", status.Prism)
	}

	if err := SetPrismToken("qa", "qa-refresh-token"); err != nil {
		t.Fatalf("SetPrismToken(qa): %v", err)
	}
	status, err = LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if !status.Prism["qa"] {
		t.Errorf("Prism[qa] = false after Set, want true")
	}
	if status.Prism["prod"] {
		t.Errorf("Prism[prod] = true, want false (never set)")
	}

	if err := ClearPrismToken("qa"); err != nil {
		t.Fatalf("ClearPrismToken(qa): %v", err)
	}
	status, err = LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus after clear: %v", err)
	}
	if status.Prism["qa"] {
		t.Errorf("Prism[qa] = true after Clear, want false")
	}
}

func TestCredentials_GuruCredsRequireColonFormat(t *testing.T) {
	resetCredentials(t)

	if err := SetGuruCreds("qa", "not-a-valid-format"); err == nil {
		t.Fatalf("SetGuruCreds accepted a value with no ':', want an error")
	}
	if err := SetGuruCreds("qa", "user@getguru.com:token123"); err != nil {
		t.Fatalf("SetGuruCreds with valid user:token format: %v", err)
	}
	status, err := LoadStatus()
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if !status.Guru["qa"] {
		t.Errorf("Guru[qa] = false after a valid Set, want true")
	}
}

func TestCredentials_UnknownEnvIsRejected(t *testing.T) {
	resetCredentials(t)

	if err := SetPrismToken("staging", "token"); err == nil {
		t.Fatalf("SetPrismToken accepted an unknown env, want an error")
	}
	if err := SetGuruCreds("staging", "user:token"); err == nil {
		t.Fatalf("SetGuruCreds accepted an unknown env, want an error")
	}
	if err := ClearPrismToken("staging"); err == nil {
		t.Fatalf("ClearPrismToken accepted an unknown env, want an error")
	}
}

func TestCredentials_FileIsPrivateAndNeverEmptyValue(t *testing.T) {
	resetCredentials(t)

	if err := SetPrismToken("prod", "secret-token"); err != nil {
		t.Fatalf("SetPrismToken: %v", err)
	}
	path, err := credentialsPath()
	if err != nil {
		t.Fatalf("credentialsPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file perm = %o, want 0600", perm)
	}

	if err := SetPrismToken("qa", ""); err == nil {
		t.Fatalf("SetPrismToken accepted an empty token, want an error")
	}
}

func TestCredentials_ClearingUnsetEnvIsNotAnError(t *testing.T) {
	resetCredentials(t)
	if err := ClearPrismToken("qa"); err != nil {
		t.Fatalf("ClearPrismToken on a never-set env: %v", err)
	}
	if err := ClearGuruCreds("prod"); err != nil {
		t.Fatalf("ClearGuruCreds on a never-set env: %v", err)
	}
}

func TestCredentials_ParentDirCreatedOnFirstWrite(t *testing.T) {
	resetCredentials(t)
	if err := SetPrismToken("qa", "tok"); err != nil {
		t.Fatalf("SetPrismToken: %v", err)
	}
	path, err := credentialsPath()
	if err != nil {
		t.Fatalf("credentialsPath: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent dir to be created: %v", err)
	}
}
