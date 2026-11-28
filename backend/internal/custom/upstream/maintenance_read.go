// Package upstream holds adapters onto upstream (non-custom) code. Each file wraps one
// upstream package so an upstream signature change breaks one file, at compile time
// (.fork/PLAN.md R10). This file is the read side of upstream maintenance entries, used by
// the Google Calendar reconciler.
package upstream

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/hook"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/maintenanceentry"
)

// MaintenanceItem is a maintenance entry with the name of the item it belongs to.
type MaintenanceItem struct {
	ID            uuid.UUID
	EntityID      uuid.UUID
	EntityName    string
	Name          string
	Description   string
	Cost          float64
	ScheduledDate *time.Time // nil when not scheduled
	CompletedDate *time.Time // nil when not completed
}

// MaintenanceReader reads maintenance entries straight from upstream's ent client.
type MaintenanceReader struct {
	db *ent.Client
}

// NewMaintenanceReader wraps upstream's ent client.
func NewMaintenanceReader(db *ent.Client) *MaintenanceReader { return &MaintenanceReader{db: db} }

// realDate returns nil for unset and zero dates (upstream stores "no date" as either NULL or
// the zero time).
func realDate(t time.Time) *time.Time {
	if t.IsZero() || t.Year() <= 1 {
		return nil
	}
	return &t
}

// ListForGroup returns every maintenance entry in the group that has a scheduled or a
// completed date. Entries with neither are not calendar material.
func (r *MaintenanceReader) ListForGroup(ctx context.Context, gid uuid.UUID) ([]MaintenanceItem, error) {
	rows, err := r.db.MaintenanceEntry.Query().
		Where(
			maintenanceentry.HasEntityWith(entity.HasGroupWith(group.ID(gid))),
			maintenanceentry.Or(maintenanceentry.DateNotNil(), maintenanceentry.ScheduledDateNotNil()),
		).
		WithEntity().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MaintenanceItem, 0, len(rows))
	for _, row := range rows {
		it := MaintenanceItem{
			ID: row.ID, EntityID: row.EntityID, Name: row.Name, Description: row.Description, Cost: row.Cost,
			ScheduledDate: realDate(row.ScheduledDate),
			CompletedDate: realDate(row.Date),
		}
		if row.Edges.Entity != nil {
			it.EntityName = row.Edges.Entity.Name
		}
		if it.ScheduledDate == nil && it.CompletedDate == nil {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// OnMaintenanceChange registers a runtime ent hook (no codegen) that calls notify with the
// group ID of every maintenance entry that is created, updated or deleted through db. It
// runs after the mutation succeeds. Deletions that cascade from an item's deletion happen in
// the database and never reach hooks; the periodic reconciler covers those.
//
// The group is resolved with the mutation's own client, so inside a transaction the lookup
// uses the same connection (a second connection would deadlock single-connection SQLite).
func OnMaintenanceChange(db *ent.Client, notify func(gid uuid.UUID)) {
	db.MaintenanceEntry.Use(func(next ent.Mutator) ent.Mutator {
		return hook.MaintenanceEntryFunc(func(ctx context.Context, m *ent.MaintenanceEntryMutation) (ent.Value, error) {
			// Updates and deletes: resolve the group before the rows change or vanish.
			var before []uuid.UUID
			if !m.Op().Is(ent.OpCreate) {
				if ids, err := m.IDs(ctx); err == nil && len(ids) > 0 {
					before, _ = groupsOfEntries(ctx, m.Client(), ids)
				}
			}
			v, err := next.Mutate(ctx, m)
			if err != nil {
				return v, err
			}
			seen := map[uuid.UUID]struct{}{}
			emit := func(gids []uuid.UUID) {
				for _, g := range gids {
					if _, dup := seen[g]; !dup {
						seen[g] = struct{}{}
						notify(g)
					}
				}
			}
			emit(before)
			if m.Op().Is(ent.OpCreate) {
				if id, ok := m.ID(); ok {
					gids, _ := groupsOfEntries(ctx, m.Client(), []uuid.UUID{id})
					emit(gids)
				}
			}
			return v, nil
		})
	})
}

func groupsOfEntries(ctx context.Context, c *ent.Client, ids []uuid.UUID) ([]uuid.UUID, error) {
	return c.MaintenanceEntry.Query().
		Where(maintenanceentry.IDIn(ids...)).
		QueryEntity().QueryGroup().
		IDs(ctx)
}
