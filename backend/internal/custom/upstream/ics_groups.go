package upstream

import (
	"context"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/usergroup"
)

// Groups answers group questions the ICS feed needs: who may manage it, and whether the
// group a token belongs to still exists.
type Groups struct {
	db *ent.Client
}

// NewGroups wraps upstream's ent client.
func NewGroups(db *ent.Client) *Groups { return &Groups{db: db} }

// IsOwner reports whether the user is an owner of the group. It mirrors upstream's
// GroupRepository.IsOwnerOf, the authoritative per-group ownership check.
func (g *Groups) IsOwner(ctx context.Context, gid, uid uuid.UUID) (bool, error) {
	return g.db.UserGroup.Query().
		Where(
			usergroup.UserID(uid),
			usergroup.GroupID(gid),
			usergroup.RoleEQ(usergroup.RoleOwner),
		).
		Exist(ctx)
}

// GroupExists reports whether the group still exists.
func (g *Groups) GroupExists(ctx context.Context, gid uuid.UUID) (bool, error) {
	return g.db.Group.Query().Where(group.ID(gid)).Exist(ctx)
}
