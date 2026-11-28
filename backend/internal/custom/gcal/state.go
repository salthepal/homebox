package gcal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// StateTTL is how long an OAuth state value stays valid.
const StateTTL = 10 * time.Minute

// ErrInvalidState is returned for a malformed, forged or expired state value.
var ErrInvalidState = errors.New("gcal: invalid or expired state")

type statePayload struct {
	UID   uuid.UUID `json:"u"`
	GID   uuid.UUID `json:"g"`
	Exp   int64     `json:"e"`
	Nonce string    `json:"n"`
}

// SignState returns a signed, short-lived state value bound to a user and group. The OAuth
// callback is a browser redirect with no Bearer token, so this is what authenticates it.
func SignState(secret []byte, uid, gid uuid.UUID, now time.Time) (string, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	body, err := json.Marshal(statePayload{
		UID: uid, GID: gid, Exp: now.Add(StateTTL).Unix(),
		Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(body)
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac(secret, enc)), nil
}

// VerifyState checks the signature and expiry and returns the bound user and group.
func VerifyState(secret []byte, state string, now time.Time) (uid, gid uuid.UUID, err error) {
	if len(secret) == 0 {
		return uuid.Nil, uuid.Nil, ErrInvalidState
	}
	var enc, sig string
	for i := 0; i < len(state); i++ {
		if state[i] == '.' {
			enc, sig = state[:i], state[i+1:]
			break
		}
	}
	got, derr := base64.RawURLEncoding.DecodeString(sig)
	if enc == "" || derr != nil || !hmac.Equal(got, mac(secret, enc)) {
		return uuid.Nil, uuid.Nil, ErrInvalidState
	}
	body, derr := base64.RawURLEncoding.DecodeString(enc)
	if derr != nil {
		return uuid.Nil, uuid.Nil, ErrInvalidState
	}
	var p statePayload
	if json.Unmarshal(body, &p) != nil || now.Unix() > p.Exp || p.UID == uuid.Nil || p.GID == uuid.Nil {
		return uuid.Nil, uuid.Nil, ErrInvalidState
	}
	return p.UID, p.GID, nil
}

func mac(secret []byte, msg string) []byte {
	m := hmac.New(sha256.New, subkey(secret, "state"))
	m.Write([]byte(msg))
	return m.Sum(nil)
}
