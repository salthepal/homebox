package ics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/google/uuid"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xicsfeed"
)

// maxTokenLen bounds what Lookup will hash; real tokens are 43 characters.
const maxTokenLen = 128

// Status describes a group's feed without revealing the token.
type Status struct {
	Exists    bool       `json:"exists"`
	CreatedAt *time.Time `json:"created_at"`
	RotatedAt *time.Time `json:"rotated_at"`
}

// Store keeps one hashed feed token per group.
type Store struct {
	ent *xent.Client
}

// NewStore wraps the custom ent client.
func NewStore(c *xent.Client) *Store { return &Store{ent: c} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Issue creates the group's token, or rotates it (invalidating the old one). The plaintext
// token is returned once and never stored.
func (s *Store) Issue(ctx context.Context, gid, uid uuid.UUID) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	h := hashToken(token)

	n, err := s.ent.XIcsFeed.Update().
		Where(xicsfeed.ID(gid)).
		SetTokenHash(h).
		SetRotatedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return "", err
	}
	if n == 0 {
		if err := s.ent.XIcsFeed.Create().SetID(gid).SetTokenHash(h).SetCreatedBy(uid).Exec(ctx); err != nil {
			return "", err
		}
	}
	return token, nil
}

// Status reports whether the group has a feed.
func (s *Store) Status(ctx context.Context, gid uuid.UUID) (Status, error) {
	row, err := s.ent.XIcsFeed.Get(ctx, gid)
	if xent.IsNotFound(err) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	created := row.CreatedAt
	return Status{Exists: true, CreatedAt: &created, RotatedAt: row.RotatedAt}, nil
}

// Revoke deletes the group's token. It is not an error if there was none.
func (s *Store) Revoke(ctx context.Context, gid uuid.UUID) error {
	_, err := s.ent.XIcsFeed.Delete().Where(xicsfeed.ID(gid)).Exec(ctx)
	return err
}

// Lookup resolves a presented token to its group. Unknown, malformed and revoked tokens are
// all the same (uuid.Nil, false, nil): callers must answer them identically. The stored hash
// is compared in constant time.
func (s *Store) Lookup(ctx context.Context, token string) (uuid.UUID, bool, error) {
	if len(token) > maxTokenLen {
		token = token[:maxTokenLen]
	}
	h := hashToken(token) // always hash, so every request does the same work
	row, err := s.ent.XIcsFeed.Query().Where(xicsfeed.TokenHash(h)).Only(ctx)
	if xent.IsNotFound(err) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if subtle.ConstantTimeCompare([]byte(row.TokenHash), []byte(h)) != 1 {
		return uuid.Nil, false, nil
	}
	return row.ID, true, nil
}
