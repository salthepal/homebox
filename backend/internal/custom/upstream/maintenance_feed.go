package upstream

import (
	"context"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/maintenanceentry"
)

// noDate is the cutoff below which a stored date counts as "unset" (upstream stores no date as
// NULL or the zero time).
var noDate = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// ListForFeed returns a bounded slice of a group's maintenance entries for the public ICS
// feed, newest first by effective date (completed date, else scheduled date), at most limit:
//
//   - completed entries with a completed date on or after completedSince
//   - not-completed entries scheduled on or before scheduledUntil (overdue ones included)
//
// The bounds are applied in the database, so a huge history never gets loaded.
func (r *MaintenanceReader) ListForFeed(ctx context.Context, gid uuid.UUID, completedSince, scheduledUntil time.Time, limit int) ([]MaintenanceItem, error) {
	inGroup := maintenanceentry.HasEntityWith(entity.HasGroupWith(group.ID(gid)))

	completed, err := r.db.MaintenanceEntry.Query().
		Where(inGroup, maintenanceentry.DateGTE(completedSince)).
		Order(maintenanceentry.ByDate(sql.OrderDesc())).
		Limit(limit).
		WithEntity().
		All(ctx)
	if err != nil {
		return nil, err
	}
	scheduled, err := r.db.MaintenanceEntry.Query().
		Where(
			inGroup,
			maintenanceentry.Or(maintenanceentry.DateIsNil(), maintenanceentry.DateLT(noDate)),
			maintenanceentry.ScheduledDateGTE(noDate),
			maintenanceentry.ScheduledDateLTE(scheduledUntil),
		).
		Order(maintenanceentry.ByScheduledDate(sql.OrderDesc())).
		Limit(limit).
		WithEntity().
		All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]MaintenanceItem, 0, len(completed)+len(scheduled))
	for _, row := range append(completed, scheduled...) {
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

	eff := func(m MaintenanceItem) time.Time {
		if m.CompletedDate != nil {
			return *m.CompletedDate
		}
		return *m.ScheduledDate
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := eff(out[i]), eff(out[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
