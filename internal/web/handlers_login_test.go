package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginWithCorrectTokenIssuesSessionCookie(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "s3cr3t",
	})

	body, _ := json.Marshal(loginRequest{Token: "s3cr3t"})
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName {
		t.Fatalf("expected a %s cookie to be set, got: %v", sessionCookieName, cookies)
	}
	if !cookies[0].HttpOnly {
		t.Fatalf("expected session cookie to be HttpOnly")
	}

	// The cookie alone (no Authorization header, no ?token=) must now
	// authorize a plain API GET — this is the whole point: a browser
	// context that never received the bearer token directly.
	req2 := httptest.NewRequest(http.MethodGet, "/api/menu", nil)
	req2.AddCookie(cookies[0])
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req2)
	if rr2.Code == http.StatusUnauthorized {
		t.Fatalf("expected session cookie to authorize the request, got 401: %s", rr2.Body.String())
	}
}

func TestLoginWithWrongTokenIsRejected(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "s3cr3t",
	})

	body, _ := json.Marshal(loginRequest{Token: "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("expected no cookie to be set on failed login")
	}
}

func TestLogoutClearsSessionCookie(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "s3cr3t",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.Header.Set("Origin", "http://"+req.Host)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("expected an expiring session cookie, got: %v", cookies)
	}
}

func TestSessionCookieDoesNotBypassCSRFOnMutations(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "s3cr3t",
	})

	body, _ := json.Marshal(loginRequest{Token: "s3cr3t"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	loginReq.Header.Set("Origin", "http://"+loginReq.Host)
	loginRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRR, loginReq)
	cookies := loginRR.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected login to set a cookie, got: %v", cookies)
	}

	// A mutation carrying the valid session cookie but a cross-origin
	// Origin header must still be rejected — cookie auth does not exempt a
	// request from the existing CSRF Origin/Referer check.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/undelete", nil)
	req.AddCookie(cookies[0])
	req.Header.Set("Origin", "http://evil.example")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected cross-origin mutation to be blocked despite valid session cookie, got %d: %s", rr.Code, rr.Body.String())
	}
}
