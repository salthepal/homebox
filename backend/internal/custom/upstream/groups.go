package upstream

import (
	"context"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/user"
)

// Groups answers membership and existence questions about upstream groups.
type Groups struct {
	db *ent.Client
}

// NewGroups wraps upstream's ent client.
func NewGroups(db *ent.Client) *Groups { return &Groups{db: db} }

// IsMember reports whether the user belongs to the group (mirrors upstream
// GroupRepository.IsMember).
func (g *Groups) IsMember(ctx context.Context, gid, uid uuid.UUID) (bool, error) {
	return g.db.Group.Query().Where(group.ID(gid), group.HasUsersWith(user.ID(uid))).Exist(ctx)
}

// ExistingIDs returns the subset of ids that are existing groups.
func (g *Groups) ExistingIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	out := make(map[uuid.UUID]struct{}, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	found, err := g.db.Group.Query().Where(group.IDIn(ids...)).IDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range found {
		out[id] = struct{}{}
	}
	return out, nil
}
