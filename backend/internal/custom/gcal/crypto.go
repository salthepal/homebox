// Package gcal implements Google Calendar sync for maintenance entries (.fork/PLAN.md §7):
// OAuth connect, a hand-written Calendar REST client (R6), the connection/link store, and
// (in later tasks) the reconciler.
package gcal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// subkey derives a purpose-specific 32-byte key from the configured secret, so the same
// HBOX_X_SECRET_KEY can both encrypt tokens and sign OAuth state without key reuse.
func subkey(secret []byte, purpose string) []byte {
	h := sha256.New()
	h.Write([]byte("homebox-x-gcal/" + purpose + "\x00"))
	h.Write(secret)
	return h.Sum(nil)
}

// Encrypt seals plaintext with AES-256-GCM. The result is nonce || ciphertext.
func Encrypt(secret, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(secret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("gcal: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a value produced by Encrypt.
func Decrypt(secret, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(secret)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("gcal: ciphertext too short")
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("gcal: decrypt failed (wrong HBOX_X_SECRET_KEY?)")
	}
	return pt, nil
}

func newGCM(secret []byte) (cipher.AEAD, error) {
	if len(secret) == 0 {
		return nil, errors.New("gcal: secret key not configured")
	}
	block, err := aes.NewCipher(subkey(secret, "enc"))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
