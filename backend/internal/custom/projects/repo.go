package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xproject"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xprojectstep"
)

// Repo stores projects in the custom tables. The project/step tables go through the custom
// ent client; the (project_id, entity_id) link table has a composite key ent can't model
// without an upstream edge, so it uses plain SQL on the same *sql.DB.
type Repo struct {
	ent     *xent.Client
	db      *sql.DB
	dialect string // "postgres" or "sqlite3"
}

// NewRepo builds a Repo. dialect is the entgo dialect name ("postgres" or "sqlite3").
func NewRepo(client *xent.Client, db *sql.DB, dialect string) *Repo {
	return &Repo{ent: client, db: db, dialect: dialect}
}

// ph returns the n-th (1-based) bind placeholder for the dialect.
func (r *Repo) ph(n int) string {
	if r.dialect == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func dateOnly(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return &d
}

// ---- projects ----

// CreateProject creates a project in group gid.
func (r *Repo) CreateProject(ctx context.Context, gid uuid.UUID, in ProjectInput) (Project, error) {
	if strings.TrimSpace(in.Name) == "" || in.Priority < 0 || in.Priority > 3 {
		return Project{}, fmt.Errorf("%w: name required, priority 0-3", ErrInvalid)
	}
	status := in.Status
	if status == "" {
		status = StatusIdea
	}
	if !validProjectStatus(status) {
		return Project{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	c := r.ent.XProject.Create().
		SetGroupID(gid).
		SetName(strings.TrimSpace(in.Name)).
		SetDescription(in.Description).
		SetStatus(xproject.Status(status)).
		SetPriority(in.Priority).
		SetNillableTargetDate(dateOnly(in.TargetDate)).
		SetNillableBudget(in.Budget)
	if status == StatusInProgress {
		c.SetStartedAt(time.Now().UTC())
	}
	if status == StatusDone {
		c.SetCompletedAt(time.Now().UTC())
	}
	row, err := c.Save(ctx)
	if err != nil {
		return Project{}, err
	}
	return r.GetProject(ctx, gid, row.ID)
}

// GetProject returns one project with steps (ordered) and linked entity IDs.
func (r *Repo) GetProject(ctx context.Context, gid, id uuid.UUID) (Project, error) {
	row, err := r.ent.XProject.Query().
		Where(xproject.ID(id), xproject.GroupID(gid)).
		WithSteps(func(q *xent.XProjectStepQuery) {
			q.Order(xprojectstep.ByPosition(), xprojectstep.ByCreatedAt())
		}).
		Only(ctx)
	if xent.IsNotFound(err) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	ids, err := r.entityIDs(ctx, []uuid.UUID{id})
	if err != nil {
		return Project{}, err
	}
	return toProject(row, ids[id]), nil
}

// ListProjects returns the group's projects (steps and links loaded), newest first.
func (r *Repo) ListProjects(ctx context.Context, gid uuid.UUID, f ListFilter) ([]Project, error) {
	q := r.ent.XProject.Query().
		Where(xproject.GroupID(gid)).
		WithSteps(func(q *xent.XProjectStepQuery) {
			q.Order(xprojectstep.ByPosition(), xprojectstep.ByCreatedAt())
		}).
		Order(xent.Desc(xproject.FieldCreatedAt))
	if f.Status != "" {
		if !validProjectStatus(f.Status) {
			return nil, fmt.Errorf("%w: status %q", ErrInvalid, f.Status)
		}
		q.Where(xproject.StatusEQ(xproject.Status(f.Status)))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	return r.withLinks(ctx, rows)
}

func (r *Repo) withLinks(ctx context.Context, rows []*xent.XProject) ([]Project, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	links, err := r.entityIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Project, len(rows))
	for i, row := range rows {
		out[i] = toProject(row, links[row.ID])
	}
	return out, nil
}

// UpdateProject replaces the editable fields of a project. started_at / completed_at are
// maintained from status transitions: first entering in_progress sets started_at; entering
// done sets completed_at; leaving done clears it.
func (r *Repo) UpdateProject(ctx context.Context, gid, id uuid.UUID, in ProjectInput) (Project, error) {
	if strings.TrimSpace(in.Name) == "" || in.Priority < 0 || in.Priority > 3 {
		return Project{}, fmt.Errorf("%w: name required, priority 0-3", ErrInvalid)
	}
	if in.Status != "" && !validProjectStatus(in.Status) {
		return Project{}, fmt.Errorf("%w: status %q", ErrInvalid, in.Status)
	}
	cur, err := r.ent.XProject.Query().Where(xproject.ID(id), xproject.GroupID(gid)).Only(ctx)
	if xent.IsNotFound(err) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	u := cur.Update().
		SetName(strings.TrimSpace(in.Name)).
		SetDescription(in.Description).
		SetPriority(in.Priority).
		SetNillableTargetDate(dateOnly(in.TargetDate)).
		SetNillableBudget(in.Budget)
	if in.TargetDate == nil {
		u.ClearTargetDate()
	}
	if in.Budget == nil {
		u.ClearBudget()
	}
	if in.Status != "" && in.Status != string(cur.Status) {
		now := time.Now().UTC()
		u.SetStatus(xproject.Status(in.Status))
		if in.Status == StatusInProgress && cur.StartedAt == nil {
			u.SetStartedAt(now)
		}
		if in.Status == StatusDone {
			u.SetCompletedAt(now)
		} else if string(cur.Status) == StatusDone {
			u.ClearCompletedAt()
		}
	}
	if _, err := u.Save(ctx); err != nil {
		return Project{}, err
	}
	return r.GetProject(ctx, gid, id)
}

// DeleteProject deletes a project; its steps and links go with it.
func (r *Repo) DeleteProject(ctx context.Context, gid, id uuid.UUID) error {
	tx, err := r.ent.Tx(ctx)
	if err != nil {
		return err
	}
	n, err := tx.XProject.Query().Where(xproject.ID(id), xproject.GroupID(gid)).Count(ctx)
	if err != nil {
		return rollback(tx, err)
	}
	if n == 0 {
		return rollback(tx, ErrNotFound)
	}
	// Explicit deletes as well as the FK cascade, so cleanup doesn't depend on
	// SQLite's foreign_keys pragma being on.
	if _, err := tx.XProjectStep.Delete().Where(xprojectstep.ProjectID(id)).Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	if _, err := tx.XProject.Delete().Where(xproject.ID(id), xproject.GroupID(gid)).Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "DELETE FROM x_project_entities WHERE project_id = "+r.ph(1), id)
	return err
}

func rollback(tx *xent.Tx, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		return fmt.Errorf("%w (rollback: %v)", err, rerr)
	}
	return err
}

// ---- steps ----

// ownsProject reports ErrNotFound unless project pid is in group gid.
func (r *Repo) ownsProject(ctx context.Context, gid, pid uuid.UUID) error {
	ok, err := r.ent.XProject.Query().Where(xproject.ID(pid), xproject.GroupID(gid)).Exist(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func validateStep(in StepInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return fmt.Errorf("%w: title required", ErrInvalid)
	}
	if in.Status != "" && !validStepStatus(in.Status) {
		return fmt.Errorf("%w: step status %q", ErrInvalid, in.Status)
	}
	return nil
}

// AddStep appends a step to the end of a project.
func (r *Repo) AddStep(ctx context.Context, gid, pid uuid.UUID, in StepInput) (Step, error) {
	if err := validateStep(in); err != nil {
		return Step{}, err
	}
	if err := r.ownsProject(ctx, gid, pid); err != nil {
		return Step{}, err
	}
	status := in.Status
	if status == "" {
		status = StepTodo
	}
	pos := 0
	if last, err := r.ent.XProjectStep.Query().Where(xprojectstep.ProjectID(pid)).
		Order(xent.Desc(xprojectstep.FieldPosition)).First(ctx); err == nil {
		pos = last.Position + 1
	} else if !xent.IsNotFound(err) {
		return Step{}, err
	}
	c := r.ent.XProjectStep.Create().
		SetProjectID(pid).
		SetPosition(pos).
		SetTitle(strings.TrimSpace(in.Title)).
		SetNotes(in.Notes).
		SetStatus(xprojectstep.Status(status)).
		SetNillableDueDate(dateOnly(in.DueDate)).
		SetNillableEstCost(in.EstCost).
		SetNillableActualCost(in.ActualCost).
		SetNillableURL(in.URL).
		SetNillableEntityID(in.EntityID).
		SetLogAsMaintenance(in.LogAsMaintenance)
	if status == StepDone {
		c.SetCompletedAt(time.Now().UTC())
	}
	row, err := c.Save(ctx)
	if err != nil {
		return Step{}, err
	}
	return toStep(row), nil
}

func (r *Repo) getStep(ctx context.Context, gid, pid, sid uuid.UUID) (*xent.XProjectStep, error) {
	row, err := r.ent.XProjectStep.Query().
		Where(xprojectstep.ID(sid), xprojectstep.ProjectID(pid),
			xprojectstep.HasProjectWith(xproject.GroupID(gid))).
		Only(ctx)
	if xent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return row, err
}

// GetStep returns one step.
func (r *Repo) GetStep(ctx context.Context, gid, pid, sid uuid.UUID) (Step, error) {
	row, err := r.getStep(ctx, gid, pid, sid)
	if err != nil {
		return Step{}, err
	}
	return toStep(row), nil
}

// UpdateStep replaces the editable fields of a step.
func (r *Repo) UpdateStep(ctx context.Context, gid, pid, sid uuid.UUID, in StepInput) (Step, error) {
	if err := validateStep(in); err != nil {
		return Step{}, err
	}
	cur, err := r.getStep(ctx, gid, pid, sid)
	if err != nil {
		return Step{}, err
	}
	u := cur.Update().
		SetTitle(strings.TrimSpace(in.Title)).
		SetNotes(in.Notes).
		SetNillableDueDate(dateOnly(in.DueDate)).
		SetNillableEstCost(in.EstCost).
		SetNillableActualCost(in.ActualCost).
		SetNillableURL(in.URL).
		SetNillableEntityID(in.EntityID).
		SetLogAsMaintenance(in.LogAsMaintenance)
	if in.DueDate == nil {
		u.ClearDueDate()
	}
	if in.EstCost == nil {
		u.ClearEstCost()
	}
	if in.ActualCost == nil {
		u.ClearActualCost()
	}
	if in.URL == nil {
		u.ClearURL()
	}
	if in.EntityID == nil {
		u.ClearEntityID()
	}
	if in.Status != "" && in.Status != string(cur.Status) {
		u.SetStatus(xprojectstep.Status(in.Status))
		if in.Status == StepDone {
			u.SetCompletedAt(time.Now().UTC())
		} else if string(cur.Status) == StepDone {
			u.ClearCompletedAt()
		}
	}
	row, err := u.Save(ctx)
	if err != nil {
		return Step{}, err
	}
	return toStep(row), nil
}

// CompleteStep marks a step done, applying the completion details. Used by the service when
// a step is completed (optionally recording the upstream maintenance entry it created).
func (r *Repo) CompleteStep(ctx context.Context, gid, pid, sid uuid.UUID, c StepCompletion) (Step, error) {
	cur, err := r.getStep(ctx, gid, pid, sid)
	if err != nil {
		return Step{}, err
	}
	at := c.At
	if at.IsZero() {
		at = time.Now()
	}
	u := cur.Update().
		SetStatus(xprojectstep.StatusDone).
		SetCompletedAt(at.UTC()).
		SetLogAsMaintenance(c.LogAsMaintenance)
	if c.ActualCost != nil {
		u.SetActualCost(*c.ActualCost)
	}
	if c.MaintenanceEntryID != nil {
		u.SetMaintenanceEntryID(*c.MaintenanceEntryID)
	}
	row, err := u.Save(ctx)
	if err != nil {
		return Step{}, err
	}
	return toStep(row), nil
}

// DeleteStep removes a step and closes the gap in positions.
func (r *Repo) DeleteStep(ctx context.Context, gid, pid, sid uuid.UUID) error {
	if _, err := r.getStep(ctx, gid, pid, sid); err != nil {
		return err
	}
	tx, err := r.ent.Tx(ctx)
	if err != nil {
		return err
	}
	if err := tx.XProjectStep.DeleteOneID(sid).Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	rows, err := tx.XProjectStep.Query().Where(xprojectstep.ProjectID(pid)).
		Order(xprojectstep.ByPosition(), xprojectstep.ByCreatedAt()).All(ctx)
	if err != nil {
		return rollback(tx, err)
	}
	for i, row := range rows {
		if row.Position != i {
			if err := tx.XProjectStep.UpdateOne(row).SetPosition(i).Exec(ctx); err != nil {
				return rollback(tx, err)
			}
		}
	}
	return tx.Commit()
}

// ReorderSteps sets step positions from an ordered list that must contain exactly the
// project's step IDs. It runs in one transaction.
func (r *Repo) ReorderSteps(ctx context.Context, gid, pid uuid.UUID, order []uuid.UUID) error {
	if err := r.ownsProject(ctx, gid, pid); err != nil {
		return err
	}
	tx, err := r.ent.Tx(ctx)
	if err != nil {
		return err
	}
	rows, err := tx.XProjectStep.Query().Where(xprojectstep.ProjectID(pid)).All(ctx)
	if err != nil {
		return rollback(tx, err)
	}
	if len(rows) != len(order) {
		return rollback(tx, fmt.Errorf("%w: order must list all %d steps", ErrInvalid, len(rows)))
	}
	have := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		have[row.ID] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(order))
	for _, id := range order {
		if _, ok := have[id]; !ok {
			return rollback(tx, fmt.Errorf("%w: step %s not in project", ErrInvalid, id))
		}
		if _, dup := seen[id]; dup {
			return rollback(tx, fmt.Errorf("%w: duplicate step %s", ErrInvalid, id))
		}
		seen[id] = struct{}{}
	}
	for i, id := range order {
		if err := tx.XProjectStep.UpdateOneID(id).SetPosition(i).Exec(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	return tx.Commit()
}

// ---- entity links ----

// ReplaceEntities replaces the set of items linked to a project. Callers must already have
// validated that the entities belong to the group (via the upstream adapter).
func (r *Repo) ReplaceEntities(ctx context.Context, gid, pid uuid.UUID, entityIDs []uuid.UUID) error {
	if err := r.ownsProject(ctx, gid, pid); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM x_project_entities WHERE project_id = "+r.ph(1), pid); err != nil {
		_ = tx.Rollback()
		return err
	}
	seen := make(map[uuid.UUID]struct{}, len(entityIDs))
	for _, eid := range entityIDs {
		if _, dup := seen[eid]; dup {
			continue
		}
		seen[eid] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO x_project_entities (project_id, entity_id) VALUES ("+r.ph(1)+", "+r.ph(2)+")",
			pid, eid); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListByEntity returns the group's projects linked to an item.
func (r *Repo) ListByEntity(ctx context.Context, gid, entityID uuid.UUID) ([]Project, error) {
	rs, err := r.db.QueryContext(ctx,
		"SELECT project_id FROM x_project_entities WHERE entity_id = "+r.ph(1), entityID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var ids []uuid.UUID
	for rs.Next() {
		var id uuid.UUID
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Project{}, nil
	}
	rows, err := r.ent.XProject.Query().
		Where(xproject.IDIn(ids...), xproject.GroupID(gid)).
		WithSteps(func(q *xent.XProjectStepQuery) {
			q.Order(xprojectstep.ByPosition(), xprojectstep.ByCreatedAt())
		}).
		Order(xent.Desc(xproject.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return r.withLinks(ctx, rows)
}

func (r *Repo) entityIDs(ctx context.Context, projectIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := make(map[uuid.UUID][]uuid.UUID, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(projectIDs))
	args := make([]any, len(projectIDs))
	for i, id := range projectIDs {
		ph[i] = r.ph(i + 1)
		args[i] = id
	}
	rs, err := r.db.QueryContext(ctx,
		"SELECT project_id, entity_id FROM x_project_entities WHERE project_id IN ("+
			strings.Join(ph, ",")+") ORDER BY entity_id", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var pid, eid uuid.UUID
		if err := rs.Scan(&pid, &eid); err != nil {
			return nil, err
		}
		out[pid] = append(out[pid], eid)
	}
	return out, rs.Err()
}

// ---- mapping ----

func toProject(row *xent.XProject, entityIDs []uuid.UUID) Project {
	p := Project{
		ID: row.ID, GroupID: row.GroupID, Name: row.Name, Description: row.Description,
		Status: string(row.Status), Priority: row.Priority, TargetDate: row.TargetDate,
		Budget: row.Budget, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Steps: make([]Step, 0, len(row.Edges.Steps)), EntityIDs: entityIDs,
	}
	if p.EntityIDs == nil {
		p.EntityIDs = []uuid.UUID{}
	}
	for _, s := range row.Edges.Steps {
		p.Steps = append(p.Steps, toStep(s))
	}
	return p
}

func toStep(row *xent.XProjectStep) Step {
	return Step{
		ID: row.ID, ProjectID: row.ProjectID, Position: row.Position, Title: row.Title,
		Notes: row.Notes, Status: string(row.Status), DueDate: row.DueDate,
		EstCost: row.EstCost, ActualCost: row.ActualCost, URL: row.URL, EntityID: row.EntityID,
		LogAsMaintenance: row.LogAsMaintenance, MaintenanceEntryID: row.MaintenanceEntryID,
		CompletedAt: row.CompletedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
