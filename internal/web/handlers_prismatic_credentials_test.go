package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

// resetPrismaticCredentials clears every env/kind before a test — see
// internal/prismatic/credentials_test.go's resetCredentials for why: HOME is
// isolated once per package (testmain_test.go), not once per test.
func resetPrismaticCredentials(t *testing.T) {
	t.Helper()
	for env := range prismatic.ValidEnvs {
		_ = prismatic.ClearPrismToken(env)
		_ = prismatic.ClearGuruCreds(env)
	}
}

func newCredentialsServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
}

func doCredentialsRequest(t *testing.T, srv *Server, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, "/api/prismatic/credentials", &buf)
	rr := httptest.NewRecorder()
	srv.handlePrismaticCredentials(rr, req)
	return rr
}

func TestPrismaticCredentials_SetThenStatusThenClear(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := newCredentialsServer(t)

	rr := doCredentialsRequest(t, srv, http.MethodPost, prismaticCredentialRequest{Kind: "prism", Env: "qa", Value: "qa-token"})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST set status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	rr = doCredentialsRequest(t, srv, http.MethodGet, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var status prismatic.CredentialStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Prism["qa"] {
		t.Errorf("Prism[qa] = false after Set, want true")
	}
	// The value itself must never appear anywhere in the response body.
	if bytesContain(rr.Body.Bytes(), "qa-token") {
		t.Fatalf("GET response leaked the raw secret value: %s", rr.Body.String())
	}

	rr = doCredentialsRequest(t, srv, http.MethodDelete, prismaticCredentialRequest{Kind: "prism", Env: "qa"})
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	rr = doCredentialsRequest(t, srv, http.MethodGet, nil)
	var status2 prismatic.CredentialStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status2); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status2.Prism["qa"] {
		t.Errorf("Prism[qa] = true after Clear, want false")
	}
}

func bytesContain(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}

func TestPrismaticCredentials_GuruCredsWrongFormatRejected(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := newCredentialsServer(t)

	rr := doCredentialsRequest(t, srv, http.MethodPost, prismaticCredentialRequest{Kind: "guru", Env: "prod", Value: "not-user-colon-token"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a value with no ':', body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCredentials_UnknownKindRejected(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := newCredentialsServer(t)

	rr := doCredentialsRequest(t, srv, http.MethodPost, prismaticCredentialRequest{Kind: "aws", Env: "qa", Value: "x"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown kind, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCredentials_RequiresAuth(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
		MenuData:   &fakeMenuData{snapshot: &MenuSnapshot{}},
	})
	rr := doCredentialsRequest(t, srv, http.MethodGet, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCredentials_MutationsDisabledRejectsWrite(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: false,
		MenuData:     &fakeMenuData{snapshot: &MenuSnapshot{}},
	})
	rr := doCredentialsRequest(t, srv, http.MethodPost, prismaticCredentialRequest{Kind: "prism", Env: "qa", Value: "x"})
	if rr.Code == http.StatusOK {
		t.Fatalf("POST succeeded with WebMutations=false, want it rejected (body=%s)", rr.Body.String())
	}
}

func TestPrismaticCredentials_RejectsUnsupportedMethod(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := newCredentialsServer(t)
	rr := doCredentialsRequest(t, srv, http.MethodPatch, nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 for PATCH, body=%s", rr.Code, rr.Body.String())
	}
}
