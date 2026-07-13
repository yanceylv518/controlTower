package agentgateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

func (h Handler) authenticate(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return "", false
	}
	if h.lookup != nil {
		id, ok, err := h.lookup.InstanceIDByTokenHash(hashToken(h.pepper, token), time.Now().UTC())
		if err != nil {
			// A lookup failure is an operational problem (e.g. the collation
			// mismatch found by the M1 stage verification), not a bad token.
			// It must be visible in logs instead of silently becoming a 401.
			log.Printf("control tower agent token lookup failed: %v", err)
		}
		if err == nil && ok {
			return id, true
		}
	}
	if validBearerToken(r, h.expectedToken) {
		return "", true
	}
	return "", false
}
func hashToken(pepper, token string) string {
	s := sha256.Sum256([]byte(pepper + token))
	return hex.EncodeToString(s[:])
}

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
func hasBearerToken(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != ""
}
