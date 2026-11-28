package upstream

import (
	"context"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/usergroup"
)

// IsOwner reports whether the user is an owner of the group. It mirrors upstream's
// GroupRepository.IsOwnerOf, the authoritative per-group ownership check. The ICS feed uses it
// to restrict creating, rotating and revoking the feed link to owners.
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
