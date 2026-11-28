package ics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xicsfeed"
)

// issueAttempts bounds the update/insert retry in Issue.
const issueAttempts = 5

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

	// Each statement is atomic, so concurrent first-time issuers can only collide on the
	// insert (unique group_id). The loser retries as an update, so nobody sees an error and
	// the last write wins: exactly one token, the last one stored, is valid afterwards.
	// (An ON CONFLICT upsert would need ent's sql/upsert feature, which would regenerate
	// every other custom entity.)
	for attempt := 0; attempt < issueAttempts; attempt++ {
		n, err := s.ent.XIcsFeed.Update().
			Where(xicsfeed.ID(gid)).
			SetTokenHash(h).
			SetRotatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return "", err
		}
		if n > 0 {
			return token, nil
		}
		err = s.ent.XIcsFeed.Create().SetID(gid).SetTokenHash(h).SetCreatedBy(uid).Exec(ctx)
		if err == nil {
			return token, nil
		}
		if !xent.IsConstraintError(err) {
			return "", err
		}
		// Someone else created the row between our update and insert: update it next time.
	}
	return "", errors.New("ics: could not store feed token after concurrent updates")
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

// GroupIDs lists the groups that have a feed row (for the orphan sweep).
func (s *Store) GroupIDs(ctx context.Context) ([]uuid.UUID, error) {
	return s.ent.XIcsFeed.Query().IDs(ctx)
}

// SweepOrphans removes the feed rows of groups that no longer exist and returns how many
// were removed. existing returns the subset of the given IDs that are live groups (upstream
// tables can't cascade into ours, R4). Run daily by the custom orphan sweep.
func (s *Store) SweepOrphans(ctx context.Context, existing func(context.Context, []uuid.UUID) (map[uuid.UUID]struct{}, error)) (int, error) {
	ids, err := s.GroupIDs(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	alive, err := existing(ctx, ids)
	if err != nil {
		return 0, err
	}
	var dead []uuid.UUID
	for _, id := range ids {
		if _, ok := alive[id]; !ok {
			dead = append(dead, id)
		}
	}
	return s.DeleteForGroups(ctx, dead)
}

// DeleteForGroups deletes the feed rows of the given groups and returns how many were
// removed.
func (s *Store) DeleteForGroups(ctx context.Context, ids []uuid.UUID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return s.ent.XIcsFeed.Delete().Where(xicsfeed.IDIn(ids...)).Exec(ctx)
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
