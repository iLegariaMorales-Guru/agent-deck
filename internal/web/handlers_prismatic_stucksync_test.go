package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/prismatic"
)

func doStuckSyncRequest(t *testing.T, srv *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/prismatic/curls/stuck-sync", &buf)
	rr := httptest.NewRecorder()
	srv.handlePrismaticCurlsStuckSync(rr, req)
	return rr
}

func TestPrismaticCurlsStuckSync_BuildsOnePutCurlPerRow(t *testing.T) {
	resetPrismaticCredentials(t)
	if err := prismatic.SetGuruCreds("prod", "puser:ptoken"); err != nil {
		t.Fatalf("SetGuruCreds: %v", err)
	}
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})

	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env:      "prod",
		SourceID: "9ed63f8fb6fe4b9393917a3c4de8f1b4",
		Rows: []prismaticStuckSyncRowRequest{
			{ObjectTypeID: "02ddb657-60c4-4011-9434-106f6bcafe39", SyncNumber: 1, StatusReason: "API_ERROR"},
			{ObjectTypeID: "Folder", SyncNumber: 1, StatusReason: "JOB_TIMEOUT"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp prismaticStuckSyncResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Curls) != 2 {
		t.Fatalf("len(Curls) = %d, want 2", len(resp.Curls))
	}
	if !bytesContain([]byte(resp.Curls[0].Curl), "puser:ptoken") {
		t.Errorf("curl should embed the stored prod Guru credentials, got: %s", resp.Curls[0].Curl)
	}
	if !bytesContain([]byte(resp.Curls[0].Curl), "9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4") {
		t.Errorf("curl should embed the dash-normalized sourceId, got: %s", resp.Curls[0].Curl)
	}
}

func TestPrismaticCurlsStuckSync_MissingSourceIDIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env:  "qa",
		Rows: []prismaticStuckSyncRowRequest{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing sourceId, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_NoRowsIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{Env: "qa", SourceID: "sc-1"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for zero rows, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_InvalidStatusReasonIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env: "qa", SourceID: "sc-1",
		Rows: []prismaticStuckSyncRowRequest{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "INVALID_AUTHENTICATION"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-FAILED-mapping statusReason, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_InvalidEnvIsBadRequest(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env: "staging", SourceID: "sc-1",
		Rows: []prismaticStuckSyncRowRequest{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid env, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_RequiresAuth(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", Token: "secret-token", MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env: "qa", SourceID: "sc-1",
		Rows: []prismaticStuckSyncRowRequest{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}},
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_MutationsDisabledRejects(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: false, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	rr := doStuckSyncRequest(t, srv, prismaticStuckSyncRequest{
		Env: "qa", SourceID: "sc-1",
		Rows: []prismaticStuckSyncRowRequest{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}},
	})
	if rr.Code == http.StatusOK {
		t.Fatalf("POST succeeded with WebMutations=false, want it rejected (body=%s)", rr.Body.String())
	}
}

func TestPrismaticCurlsStuckSync_RejectsNonPOST(t *testing.T) {
	resetPrismaticCredentials(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", WebMutations: true, MenuData: &fakeMenuData{snapshot: &MenuSnapshot{}}})
	req := httptest.NewRequest(http.MethodGet, "/api/prismatic/curls/stuck-sync", nil)
	rr := httptest.NewRecorder()
	srv.handlePrismaticCurlsStuckSync(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 for GET, body=%s", rr.Code, rr.Body.String())
	}
}
