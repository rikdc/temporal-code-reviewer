package middleware

import (
	"crypto/subtle"
	"net/http"
)

// BearerAuth returns an HTTP middleware that checks for a valid bearer token.
// If token is empty, the middleware is a no-op (allows unauthenticated access).
func BearerAuth(token string) func(http.Handler) http.Handler {
	if token == "" {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(auth) < len(prefix) || auth[:len(prefix)] != prefix {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			tok := auth[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(tok), []byte(token)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
