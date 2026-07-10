package agentgateway

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func validBearerToken(r *http.Request, expectedToken string) bool {
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

