package upstream

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/maintenanceentry"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// ErrEntityNotFound is returned when the item a maintenance entry would belong to doesn't
// exist in the caller's group.
var ErrEntityNotFound = errors.New("upstream: item not found")

// Maintenance creates and checks upstream maintenance entries.
type Maintenance struct {
	db   *ent.Client
	repo *repo.MaintenanceEntryRepository
}

// NewMaintenance wraps upstream's maintenance repo (writes) and ent client (bulk reads).
func NewMaintenance(db *ent.Client, r *repo.MaintenanceEntryRepository) *Maintenance {
	return &Maintenance{db: db, repo: r}
}

// MaintenanceInput is a completed maintenance entry to create.
type MaintenanceInput struct {
	Name        string
	Description string
	Cost        float64
	Date        time.Time // completion date
}

// CreateCompleted creates an already-completed maintenance entry on an item in group gid and
// returns its ID.
func (m *Maintenance) CreateCompleted(ctx context.Context, gid, entityID uuid.UUID, in MaintenanceInput) (uuid.UUID, error) {
	out, err := m.repo.Create(ctx, gid, entityID, repo.MaintenanceEntryCreate{
		CompletedDate: types.DateFromTime(in.Date),
		Name:          in.Name,
		Description:   in.Description,
		Cost:          in.Cost,
	})
	if ent.IsNotFound(err) {
		return uuid.Nil, ErrEntityNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return out.ID, nil
}

// Delete removes an entry in group gid (used to undo a create when a later step fails).
func (m *Maintenance) Delete(ctx context.Context, gid, id uuid.UUID) error {
	err := m.repo.Delete(ctx, gid, id)
	if ent.IsNotFound(err) {
		return nil
	}
	return err
}

// ExistInGroup returns the subset of ids that are maintenance entries in group gid.
func (m *Maintenance) ExistInGroup(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	set := make(map[uuid.UUID]struct{}, len(ids))
	if len(ids) == 0 {
		return set, nil
	}
	found, err := m.db.MaintenanceEntry.Query().
		Where(maintenanceentry.IDIn(ids...), maintenanceentry.HasEntityWith(entity.HasGroupWith(group.ID(gid)))).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range found {
		set[id] = struct{}{}
	}
	return set, nil
}
