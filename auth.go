package gogateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

func Verify(token, secret string) bool {
	parts := strings.Split(token, ".")

	if len(parts) != 3 {
		return false
	}

	signingInput := parts[0] + "." + parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	computedBytes := mac.Sum(nil)

	computedSignature := base64.RawURLEncoding.EncodeToString(computedBytes)

	return hmac.Equal([]byte(computedSignature), []byte(parts[2]))
}

func HS256Verifier(secret string, publicPaths map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// skip auth for public routes
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" {
			http.Error(w, "empty token", http.StatusUnauthorized)
			return
		}

		if !Verify(token, secret) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
