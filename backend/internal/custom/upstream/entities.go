// Package upstream holds the adapters between custom code and upstream Homebox packages,
// one file per upstream package (.fork/PLAN.md R10). When upstream changes a signature, the
// break is confined to these files and shows up at compile time.
package upstream

import (
	"context"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
)

// Entities answers questions about upstream items (entities) for custom code.
type Entities struct {
	db *ent.Client
}

// NewEntities wraps upstream's ent client.
func NewEntities(db *ent.Client) *Entities { return &Entities{db: db} }

// Names returns id -> name for the given IDs that exist in group gid. IDs that don't exist,
// or belong to another group, are absent from the result.
func (e *Entities) Names(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := e.db.Entity.Query().
		Where(entity.IDIn(ids...), entity.HasGroupWith(group.ID(gid))).
		Select(entity.FieldID, entity.FieldName).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// ExistInGroup returns the subset of ids that exist in group gid, as a set.
func (e *Entities) ExistInGroup(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	names, err := e.Names(ctx, gid, ids)
	if err != nil {
		return nil, err
	}
	set := make(map[uuid.UUID]struct{}, len(names))
	for id := range names {
		set[id] = struct{}{}
	}
	return set, nil
}
