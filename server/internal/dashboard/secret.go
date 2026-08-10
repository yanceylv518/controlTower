package dashboard

import (
	"controltower/server/internal/secrets"
)

var errSecretKeyMissing = secrets.ErrKeyMissing

func encryptSecret(key, plaintext string) (string, error) {
	return secrets.Encrypt(key, plaintext)
}

func decryptSecret(key, encoded string) (string, error) {
	return secrets.Decrypt(key, encoded)
}
