package gogateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// the logic for Verify the jwt token
func Verify(token, secret string) bool {
	// sparate the three part of the jwt token
	parts := strings.Split(token, ".")
	//check that jwt have 3 parts
	if len(parts) != 3 {
		return false
	}

	// the first tow part of jwt for creat singnatur part for check
	signingInput := parts[0] + "." + parts[1]

	// creat the singnatur part
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	computedBytes := mac.Sum(nil)

	computedSignature := base64.RawURLEncoding.EncodeToString(computedBytes)

	// returns the check result in boolean
	return hmac.Equal([]byte(computedSignature), []byte(parts[2]))
}

// the middleware
func HS256Verifier(secret string, publicPaths map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// skip auth for public routes
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// check the existance of jwt token in client Request
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}

		// check the jwt is standard(have Bearer in the beginnig of it)
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" {
			http.Error(w, "empty token", http.StatusUnauthorized)
			return
		}

		// validating the jwt token
		if !Verify(token, secret) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		// pass to next middleware
		next.ServeHTTP(w, r)
	})
}
