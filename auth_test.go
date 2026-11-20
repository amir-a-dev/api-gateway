package gogateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerify(t *testing.T) {
	t.Parallel()

	const secret = "my-secret"

	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"malformed: single segment", "asfasdfefw.fwferewrwer", false},
		{"malformed: too many segments", "asfasdfefw.fwferewrwer.asdfweuifgakweuygfvbiww", false},
		{"valid token", "1234.5678.-neoLu54OHO14yx01eiY8GWFNqN1LQT-PmBb8K9HgEo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Verify(tt.value, secret)
			if got != tt.expected {
				t.Errorf("Verify(%q, secret) = %v; want %v", tt.value, got, tt.expected)
			}
		})
	}
}

// helper: build a token signed with the given secret, so tests never hardcode
// a "valid" token that could drift if the signing logic changes.
func signToken(header, payload, secret string) string {
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func TestHS256Verifier(t *testing.T) {
	t.Parallel()

	const (
		secret      = "my-secret"
		wrongSecret = "not-my-secret"
		header      = "eyJhbGciOiJIUzI1NiJ9" // base64url of {"alg":"HS256"}
		payload     = "eyJzdWIiOiIxMjMifQ"   // base64url of {"sub":"123"}
	)

	validToken := signToken(header, payload, secret)
	badSigToken := signToken(header, payload, wrongSecret)
	malformed := "not-a-jwt"

	publicPaths := map[string]bool{
		"/health": true,
		"/login":  true,
	}

	tests := []struct {
		name         string
		path         string
		authHeader   string // "" means don't set the header
		wantStatus   int
		wantNextCall bool
	}{
		{
			name:         "public path bypasses auth",
			path:         "/health",
			authHeader:   "", // no header needed
			wantStatus:   http.StatusOK,
			wantNextCall: true,
		},
		{
			name:         "public path bypasses auth even with bad token",
			path:         "/login",
			authHeader:   "Bearer garbage",
			wantStatus:   http.StatusOK,
			wantNextCall: true,
		},
		{
			name:         "missing Authorization header",
			path:         "/private",
			authHeader:   "",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "wrong scheme (Basic, not Bearer)",
			path:         "/private",
			authHeader:   "Basic dXNlcjpwYXNz",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "Bearer with empty token",
			path:         "/private",
			authHeader:   "Bearer ",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "malformed token",
			path:         "/private",
			authHeader:   "Bearer " + malformed,
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "valid structure, bad signature",
			path:         "/private",
			authHeader:   "Bearer " + badSigToken,
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "valid token passes through",
			path:         "/private",
			authHeader:   "Bearer " + validToken,
			wantStatus:   http.StatusOK,
			wantNextCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			mw := HS256Verifier(secret, publicPaths, next)

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNextCall {
				t.Errorf("next called = %v; want %v", nextCalled, tt.wantNextCall)
			}
		})
	}
}
