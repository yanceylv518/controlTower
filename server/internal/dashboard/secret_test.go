package dashboard

import (
	"strings"
	"testing"
)

func TestSecretRoundTripDoesNotStorePlaintext(t *testing.T) {
	const plaintext = "readonly:very-secret@tcp(db:3306)/newapi?parseTime=true"
	encrypted, err := encryptSecret("stable-production-key", plaintext)
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	if encrypted == plaintext || strings.Contains(encrypted, "very-secret") {
		t.Fatalf("encrypted value contains plaintext: %q", encrypted)
	}
	decoded, err := decryptSecret("stable-production-key", encrypted)
	if err != nil {
		t.Fatalf("decryptSecret: %v", err)
	}
	if decoded != plaintext {
		t.Fatalf("decoded = %q, want %q", decoded, plaintext)
	}
}

func TestSecretRejectsWrongOrMissingKey(t *testing.T) {
	encrypted, err := encryptSecret("correct", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decryptSecret("wrong", encrypted); err == nil {
		t.Fatal("expected wrong key to fail")
	}
	if _, err = encryptSecret("", "secret"); err == nil {
		t.Fatal("expected missing key to fail")
	}
}
