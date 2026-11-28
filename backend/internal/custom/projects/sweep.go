package projects

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Refs are the upstream IDs referenced by one group's projects.
type Refs struct {
	EntityIDs      []uuid.UUID // from project links and step.entity_id
	MaintenanceIDs []uuid.UUID // from step.maintenance_entry_id
}

const sweepChunk = 400

// ReferencedIDs returns, per group, the distinct upstream entity and maintenance-entry IDs
// that projects reference. Used by the orphan sweeper (cross-group by design).
func (r *Repo) ReferencedIDs(ctx context.Context) (map[uuid.UUID]*Refs, error) {
	out := map[uuid.UUID]*Refs{}
	get := func(gid uuid.UUID) *Refs {
		if out[gid] == nil {
			out[gid] = &Refs{}
		}
		return out[gid]
	}
	collect := func(query string, add func(gid uuid.UUID, id uuid.UUID)) error {
		rs, err := r.db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer func() { _ = rs.Close() }()
		for rs.Next() {
			var gid, id uuid.UUID
			if err := rs.Scan(&gid, &id); err != nil {
				return err
			}
			add(gid, id)
		}
		return rs.Err()
	}

	if err := collect(`SELECT DISTINCT p.group_id, e.entity_id FROM x_project_entities e
		JOIN x_projects p ON p.id = e.project_id`,
		func(gid, id uuid.UUID) { get(gid).EntityIDs = append(get(gid).EntityIDs, id) }); err != nil {
		return nil, err
	}
	if err := collect(`SELECT DISTINCT p.group_id, s.entity_id FROM x_project_steps s
		JOIN x_projects p ON p.id = s.project_id WHERE s.entity_id IS NOT NULL`,
		func(gid, id uuid.UUID) { get(gid).EntityIDs = append(get(gid).EntityIDs, id) }); err != nil {
		return nil, err
	}
	if err := collect(`SELECT DISTINCT p.group_id, s.maintenance_entry_id FROM x_project_steps s
		JOIN x_projects p ON p.id = s.project_id WHERE s.maintenance_entry_id IS NOT NULL`,
		func(gid, id uuid.UUID) { get(gid).MaintenanceIDs = append(get(gid).MaintenanceIDs, id) }); err != nil {
		return nil, err
	}
	return out, nil
}

// inList builds "(?,?,?)" / "($2,$3)" placeholders for ids, starting at placeholder start.
func (r *Repo) inList(start int, ids []uuid.UUID) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = r.ph(start + i)
		args[i] = id
	}
	return "(" + strings.Join(ph, ",") + ")", args
}

func chunks(ids []uuid.UUID) [][]uuid.UUID {
	var out [][]uuid.UUID
	for len(ids) > 0 {
		n := min(sweepChunk, len(ids))
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	return out
}

// RemoveEntityRefs deletes links to, and nulls step references to, the given upstream
// entities, within group gid. It returns the number of rows changed.
func (r *Repo) RemoveEntityRefs(ctx context.Context, gid uuid.UUID, entityIDs []uuid.UUID) (int64, error) {
	var total int64
	for _, c := range chunks(entityIDs) {
		in, args := r.inList(2, c)
		res, err := r.db.ExecContext(ctx,
			"DELETE FROM x_project_entities WHERE project_id IN (SELECT id FROM x_projects WHERE group_id = "+
				r.ph(1)+") AND entity_id IN "+in,
			append([]any{gid}, args...)...)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		res, err = r.db.ExecContext(ctx,
			"UPDATE x_project_steps SET entity_id = NULL WHERE project_id IN (SELECT id FROM x_projects WHERE group_id = "+
				r.ph(1)+") AND entity_id IN "+in,
			append([]any{gid}, args...)...)
		if err != nil {
			return total, err
		}
		n, _ = res.RowsAffected()
		total += n
	}
	return total, nil
}

// RemoveMaintenanceRefs nulls step.maintenance_entry_id for the given upstream entries,
// within group gid. It returns the number of rows changed.
func (r *Repo) RemoveMaintenanceRefs(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (int64, error) {
	var total int64
	for _, c := range chunks(ids) {
		in, args := r.inList(2, c)
		res, err := r.db.ExecContext(ctx,
			"UPDATE x_project_steps SET maintenance_entry_id = NULL WHERE project_id IN (SELECT id FROM x_projects WHERE group_id = "+
				r.ph(1)+") AND maintenance_entry_id IN "+in,
			append([]any{gid}, args...)...)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
