// Package secrets encrypts short credentials (DSNs, API tokens) with
// AES-256-GCM derived from CT_SECRET_KEY before they are stored in the
// Control Tower database.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var ErrKeyMissing = errors.New("secret key is not configured")

func Encrypt(key, plaintext string) (string, error) {
	if key == "" {
		return "", ErrKeyMissing
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func Decrypt(key, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if key == "" {
		return "", ErrKeyMissing
	}
	if len(encoded) < 4 || encoded[:3] != "v1:" {
		return "", errors.New("unsupported encrypted secret")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded[3:])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted secret")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}
