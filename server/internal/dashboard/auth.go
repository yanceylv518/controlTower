package dashboard

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func RequireBearerToken(expectedToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validDashboardBearerToken(r, expectedToken) {
			writeDashboardError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validDashboardBearerToken(r *http.Request, expectedToken string) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	actual := strings.TrimPrefix(header, "Bearer ")
	if expectedToken == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expectedToken)) == 1
}

