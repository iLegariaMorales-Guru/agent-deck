package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName    = "agentdeck_session"
	sessionCookieVersion = "v1"
	sessionCookieTTL     = 30 * 24 * time.Hour
)

// newSessionSecret generates a random signing key for session cookies, kept
// in memory only — not persisted to disk. A server restart invalidates
// existing sessions, which is an acceptable trade: login is a one-field
// form submit, and this sidesteps key-file persistence bugs entirely.
func newSessionSecret() []byte {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret) // crypto/rand.Read does not fail on supported platforms
	return secret
}

// signSessionValue produces a signed cookie value binding an expiry to the
// server's in-memory secret: "v1.<expiryUnix>.<hmac>".
func signSessionValue(secret []byte, expiry int64) string {
	payload := sessionCookieVersion + "." + strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// verifySessionValue checks the signature and expiry of a cookie value
// produced by signSessionValue.
func verifySessionValue(secret []byte, value string) bool {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) != 3 || parts[0] != sessionCookieVersion {
		return false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiry {
		return false
	}
	expected := signSessionValue(secret, expiry)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(value)) == 1
}

// issueSessionCookie sets a signed, HttpOnly session cookie authenticating
// this browser context on its own — the fix for iOS's storage isolation
// between a Safari tab and an installed Home Screen web app (see the
// push-notif-mobile-auth memory): each context logs in for itself instead of
// depending on a token shared via localStorage or a URL. Secure is only set
// when the request arrived over TLS; this server also runs over plain HTTP
// on Tailscale (see CLAUDE.md), where a Secure-only cookie would silently
// never be sent back.
func (s *Server) issueSessionCookie(w http.ResponseWriter, r *http.Request) {
	expiry := time.Now().Add(sessionCookieTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSessionValue(s.sessionSecret, expiry),
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionCookieTTL.Seconds()),
	})
}

// clearSessionCookie logs the current browser context out.
func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// checkSessionCookie reports whether the request carries a valid signed
// session cookie.
func (s *Server) checkSessionCookie(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return verifySessionValue(s.sessionSecret, c.Value)
}
