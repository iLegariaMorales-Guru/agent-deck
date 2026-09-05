package prismatic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchExistingSourceDefinition_ParsesSuccessResponse(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"abc","name":"Jira Issues","type":"JIRA_ISSUES","category":"CRM","availability":"GENERAL","config":{"ipaasIntegrationId":"ipaas-1"}}`))
	}))
	defer srv.Close()
	orig := apiBaseForEnv
	apiBaseForEnv = func(string) string { return srv.URL }
	defer func() { apiBaseForEnv = orig }()

	def, err := FetchExistingSourceDefinition(context.Background(), "qa", "JIRA_ISSUES", "user:tok")
	if err != nil {
		t.Fatalf("FetchExistingSourceDefinition: %v", err)
	}
	if def["name"] != "Jira Issues" {
		t.Errorf("name = %v, want Jira Issues", def["name"])
	}
	if def["id"] != "abc" {
		t.Errorf("id = %v, want abc (raw map should keep it; BuildUpdateCurl is what strips it)", def["id"])
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("Authorization header = %q, want Basic auth", gotAuth)
	}
	if gotPath != "/api/v1/admin/sources/definitions/JIRA_ISSUES" {
		t.Errorf("path = %q, want the admin sources/definitions path", gotPath)
	}
}

func TestFetchExistingSourceDefinition_404IsNotFoundSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	orig := apiBaseForEnv
	apiBaseForEnv = func(string) string { return srv.URL }
	defer func() { apiBaseForEnv = orig }()

	_, err := FetchExistingSourceDefinition(context.Background(), "qa", "NEW_THING", "user:tok")
	if err != ErrSourceDefNotFound {
		t.Fatalf("err = %v, want ErrSourceDefNotFound", err)
	}
}

func TestFetchExistingSourceDefinition_OtherErrorIncludesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()
	orig := apiBaseForEnv
	apiBaseForEnv = func(string) string { return srv.URL }
	defer func() { apiBaseForEnv = orig }()

	_, err := FetchExistingSourceDefinition(context.Background(), "qa", "X", "user:tok")
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to mention 500 and the body", err)
	}
}

func TestFetchExistingSourceDefinition_RejectsMalformedCredentials(t *testing.T) {
	_, err := FetchExistingSourceDefinition(context.Background(), "qa", "X", "no-colon-here")
	if err == nil {
		t.Fatal("expected an error for credentials with no ':', got nil")
	}
}

func TestBuildUpdateCurl_StripsIdAndUsesType(t *testing.T) {
	def := map[string]any{
		"id":           "should-not-appear",
		"type":         "JIRA_ISSUES",
		"name":         "Jira Issues",
		"category":     "CRM",
		"availability": "GENERAL",
	}
	step, err := BuildUpdateCurl(def, "prod", "u:t")
	if err != nil {
		t.Fatalf("BuildUpdateCurl: %v", err)
	}
	if strings.Contains(step.Curl, "should-not-appear") {
		t.Errorf("curl should not include the id field, got: %s", step.Curl)
	}
	if !strings.Contains(step.Curl, "https://api.getguru.com/api/v1/admin/sources/definitions/JIRA_ISSUES") {
		t.Errorf("curl should PUT to the type-scoped URL, got: %s", step.Curl)
	}
	if !strings.Contains(step.Curl, "-X PUT") {
		t.Errorf("expected a PUT, got: %s", step.Curl)
	}
	if !strings.Contains(step.Curl, "-u u:t") {
		t.Errorf("expected stored credentials embedded, got: %s", step.Curl)
	}
}

func TestBuildUpdateCurl_MissingTypeIsError(t *testing.T) {
	_, err := BuildUpdateCurl(map[string]any{"name": "no type here"}, "qa", "u:t")
	if err == nil {
		t.Fatal("expected an error for a definition with no type field, got nil")
	}
}

func TestBuildUpdateCurl_EmptyCredentialsFallsBackToPlaceholder(t *testing.T) {
	step, err := BuildUpdateCurl(map[string]any{"type": "X"}, "qa", "")
	if err != nil {
		t.Fatalf("BuildUpdateCurl: %v", err)
	}
	if !strings.Contains(step.Curl, "-u user:token") {
		t.Errorf("expected the qa placeholder credential, got: %s", step.Curl)
	}
}
