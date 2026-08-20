package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

type loginRequest struct {
	Token string `json:"token"`
}

type loginResponse struct {
	OK bool `json:"ok"`
}

// handleLogin exchanges the server's configured bearer token for a
// session cookie (see session_cookie.go). This is the real fix for the iOS
// Home Screen storage-isolation problem: instead of a token that has to
// travel via URL/localStorage into a context that may not share storage
// with the tab it came from, each context logs in for itself.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	// Reuse the mutation rate limiter as basic brute-force throttling on the
	// one endpoint that accepts a guessable secret over and over.
	if !s.checkMutationRateLimit(w) {
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid login payload")
		return
	}

	if !secureEqual(strings.TrimSpace(req.Token), s.cfg.Token) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "incorrect access token")
		return
	}

	s.issueSessionCookie(w, r)
	writeJSON(w, http.StatusOK, loginResponse{OK: true})
}

// handleLogout clears the session cookie for the current browser context.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, loginResponse{OK: true})
}
