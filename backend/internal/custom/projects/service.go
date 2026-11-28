package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

// ErrConflict is returned when an action doesn't apply to the current state (for example
// completing a step that is already done).
var ErrConflict = errors.New("projects: conflict")

// Entities is the part of the upstream items adapter the service needs.
type Entities interface {
	Names(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error)
	ExistInGroup(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error)
}

// Maintenance is the part of the upstream maintenance adapter the service needs.
type Maintenance interface {
	CreateCompleted(ctx context.Context, gid, entityID uuid.UUID, in upstream.MaintenanceInput) (uuid.UUID, error)
	Delete(ctx context.Context, gid, id uuid.UUID) error
	ExistInGroup(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error)
}

// Service holds the Projects business rules (.fork/PLAN.md §6) on top of Repo.
type Service struct {
	repo  *Repo
	ents  Entities
	maint Maintenance
	now   func() time.Time
}

// NewService builds a Service.
func NewService(repo *Repo, ents Entities, maint Maintenance) *Service {
	return &Service{repo: repo, ents: ents, maint: maint, now: time.Now}
}

// EntityName is a linked item's display name.
type EntityName struct {
	ID   uuid.UUID
	Name string
}

// View is a project plus display data resolved from upstream.
type View struct {
	Project
	// Entities are the linked items that still exist, with names, ordered like EntityIDs.
	Entities []EntityName
}

func (s *Service) view(ctx context.Context, gid uuid.UUID, ps ...Project) ([]View, error) {
	seen := map[uuid.UUID]struct{}{}
	var ids []uuid.UUID
	for _, p := range ps {
		for _, id := range p.EntityIDs {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	names, err := s.ents.Names(ctx, gid, ids)
	if err != nil {
		return nil, err
	}
	out := make([]View, len(ps))
	for i, p := range ps {
		v := View{Project: p, Entities: []EntityName{}}
		for _, id := range p.EntityIDs {
			if n, ok := names[id]; ok {
				v.Entities = append(v.Entities, EntityName{ID: id, Name: n})
			}
		}
		out[i] = v
	}
	return out, nil
}

// List returns the group's projects.
func (s *Service) List(ctx context.Context, gid uuid.UUID, f ListFilter) ([]View, error) {
	ps, err := s.repo.ListProjects(ctx, gid, f)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, gid, ps...)
}

// Get returns one project.
func (s *Service) Get(ctx context.Context, gid, id uuid.UUID) (View, error) {
	p, err := s.repo.GetProject(ctx, gid, id)
	if err != nil {
		return View{}, err
	}
	return s.one(ctx, gid, p)
}

func (s *Service) one(ctx context.Context, gid uuid.UUID, p Project) (View, error) {
	vs, err := s.view(ctx, gid, p)
	if err != nil {
		return View{}, err
	}
	return vs[0], nil
}

// Create creates a project.
func (s *Service) Create(ctx context.Context, gid uuid.UUID, in ProjectInput) (View, error) {
	p, err := s.repo.CreateProject(ctx, gid, in)
	if err != nil {
		return View{}, err
	}
	return s.one(ctx, gid, p)
}

// Update replaces a project's editable fields.
func (s *Service) Update(ctx context.Context, gid, id uuid.UUID, in ProjectInput) (View, error) {
	p, err := s.repo.UpdateProject(ctx, gid, id, in)
	if err != nil {
		return View{}, err
	}
	return s.one(ctx, gid, p)
}

// Delete deletes a project.
func (s *Service) Delete(ctx context.Context, gid, id uuid.UUID) error {
	return s.repo.DeleteProject(ctx, gid, id)
}

// ListByEntity returns the group's projects linked to an item.
func (s *Service) ListByEntity(ctx context.Context, gid, entityID uuid.UUID) ([]View, error) {
	ps, err := s.repo.ListByEntity(ctx, gid, entityID)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, gid, ps...)
}

// SetEntities replaces the items linked to a project after checking that every one of them
// exists in the caller's group.
func (s *Service) SetEntities(ctx context.Context, gid, id uuid.UUID, entityIDs []uuid.UUID) (View, error) {
	if _, err := s.repo.GetProject(ctx, gid, id); err != nil {
		return View{}, err
	}
	if err := s.requireEntities(ctx, gid, entityIDs); err != nil {
		return View{}, err
	}
	if err := s.repo.ReplaceEntities(ctx, gid, id, entityIDs); err != nil {
		return View{}, err
	}
	return s.Get(ctx, gid, id)
}

func (s *Service) requireEntities(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	ok, err := s.ents.ExistInGroup(ctx, gid, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, found := ok[id]; !found {
			return fmt.Errorf("%w: item %s not found", ErrInvalid, id)
		}
	}
	return nil
}

// errStepDone explains why ordinary step writes can't set status "done".
func errStepDone() error {
	return fmt.Errorf("%w: use POST .../steps/{id}/complete to mark a step done", ErrInvalid)
}

// AddStep appends a step. A step created in "doing" starts the project (atomically, in the
// repo). Steps can't be created already done: completion goes through CompleteStep so the
// maintenance entry isn't skipped.
func (s *Service) AddStep(ctx context.Context, gid, pid uuid.UUID, in StepInput) (Step, error) {
	if in.Status == StepDone {
		return Step{}, errStepDone()
	}
	if in.EntityID != nil {
		if err := s.requireEntities(ctx, gid, []uuid.UUID{*in.EntityID}); err != nil {
			return Step{}, err
		}
	}
	return s.repo.AddStep(ctx, gid, pid, in)
}

// UpdateStep replaces a step's editable fields. A step moving to "doing" moves an
// idea/planning project to in_progress (in the repo, in the same transaction). Moving a step
// to "done" is rejected (use CompleteStep); a step that is already done may be edited and may
// leave done.
func (s *Service) UpdateStep(ctx context.Context, gid, pid, sid uuid.UUID, in StepInput) (Step, error) {
	if in.Status == StepDone {
		cur, err := s.repo.GetStep(ctx, gid, pid, sid)
		if err != nil {
			return Step{}, err
		}
		if cur.Status != StepDone {
			return Step{}, errStepDone()
		}
	}
	if in.EntityID != nil {
		if err := s.requireEntities(ctx, gid, []uuid.UUID{*in.EntityID}); err != nil {
			return Step{}, err
		}
	}
	return s.repo.UpdateStep(ctx, gid, pid, sid, in)
}

// DeleteStep removes a step.
func (s *Service) DeleteStep(ctx context.Context, gid, pid, sid uuid.UUID) error {
	return s.repo.DeleteStep(ctx, gid, pid, sid)
}

// ReorderSteps sets the step order.
func (s *Service) ReorderSteps(ctx context.Context, gid, pid uuid.UUID, order []uuid.UUID) (View, error) {
	if err := s.repo.ReorderSteps(ctx, gid, pid, order); err != nil {
		return View{}, err
	}
	return s.Get(ctx, gid, pid)
}

// CompleteInput is the body of a complete-step request. Nil LogAsMaintenance means "use the
// step's own log_as_maintenance setting".
type CompleteInput struct {
	ActualCost       *float64
	LogAsMaintenance *bool
}

// CompleteStep marks a step done. It first claims the step atomically (status done,
// completed_at, cost) so concurrent completions can't both log maintenance: the loser gets
// ErrConflict. When logging is on and the step has an item, it then creates a completed
// upstream maintenance entry (name = step title, description = notes + "From project:
// <name>", cost = actual cost, date = now) and stores its ID on the step. If creating the
// entry or recording its ID fails, the entry is deleted and the claim is reverted.
func (s *Service) CompleteStep(ctx context.Context, gid, pid, sid uuid.UUID, in CompleteInput) (Step, error) {
	p, err := s.repo.GetProject(ctx, gid, pid)
	if err != nil {
		return Step{}, err
	}
	step, err := s.repo.GetStep(ctx, gid, pid, sid)
	if err != nil {
		return Step{}, err
	}
	if step.Status == StepDone {
		return Step{}, fmt.Errorf("%w: step already completed", ErrConflict)
	}
	if err := ValidateMoney("actual_cost", in.ActualCost); err != nil {
		return Step{}, err
	}

	logIt := step.LogAsMaintenance
	if in.LogAsMaintenance != nil {
		logIt = *in.LogAsMaintenance
	}
	cost := step.ActualCost
	if in.ActualCost != nil {
		cost = in.ActualCost
	}
	if logIt && step.EntityID == nil {
		return Step{}, fmt.Errorf("%w: step has no item to log maintenance on", ErrInvalid)
	}
	now := s.now()
	prev, err := s.repo.ClaimStep(ctx, gid, pid, sid, StepCompletion{ActualCost: cost, LogAsMaintenance: logIt, At: now})
	if err != nil {
		return Step{}, err
	}
	if !logIt {
		return s.repo.GetStep(ctx, gid, pid, sid)
	}

	desc := strings.TrimSpace(step.Notes)
	if desc != "" {
		desc += "\n\n"
	}
	desc += "From project: " + p.Name
	var c float64
	if cost != nil {
		c = *cost
	}
	mid, err := s.maint.CreateCompleted(ctx, gid, *step.EntityID, upstream.MaintenanceInput{
		Name: step.Title, Description: desc, Cost: c, Date: now,
	})
	if err != nil {
		s.release(gid, pid, sid, prev)
		if errors.Is(err, upstream.ErrEntityNotFound) {
			return Step{}, fmt.Errorf("%w: item %s not found", ErrInvalid, *step.EntityID)
		}
		return Step{}, err
	}
	done, err := s.repo.RecordStepMaintenance(ctx, gid, pid, sid, mid)
	if err != nil {
		// Undo the upstream entry so a retry doesn't log it twice.
		if derr := s.maint.Delete(ctx, gid, mid); derr != nil {
			log.Warn().Err(derr).Msg("projects: failed to roll back maintenance entry")
		}
		s.release(gid, pid, sid, prev)
		return Step{}, err
	}
	return done, nil
}

// release reverts a claim. It uses its own context: the request context may be what failed.
func (s *Service) release(gid, pid, sid uuid.UUID, prev Step) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()
	if err := s.repo.ReleaseStep(ctx, gid, pid, sid, prev); err != nil {
		log.Warn().Err(err).Msg("projects: failed to revert step claim")
	}
}

// SweepOrphans removes references to upstream rows that no longer exist: project links and
// step items for deleted items, and step maintenance IDs for deleted entries. Upstream
// deletes bypass us and we hold no foreign keys (R4), so this runs daily.
func (s *Service) SweepOrphans(ctx context.Context) error {
	refs, err := s.repo.ReferencedIDs(ctx)
	if err != nil {
		return err
	}
	for gid, r := range refs {
		if len(r.EntityIDs) > 0 {
			ok, err := s.ents.ExistInGroup(ctx, gid, r.EntityIDs)
			if err != nil {
				return err
			}
			var gone []uuid.UUID
			for _, id := range r.EntityIDs {
				if _, found := ok[id]; !found {
					gone = append(gone, id)
				}
			}
			if len(gone) > 0 {
				n, err := s.repo.RemoveEntityRefs(ctx, gid, gone)
				if err != nil {
					return err
				}
				log.Info().Str("group", gid.String()).Int64("rows", n).Msg("projects: removed references to deleted items")
			}
		}
		if len(r.MaintenanceIDs) > 0 {
			ok, err := s.maint.ExistInGroup(ctx, gid, r.MaintenanceIDs)
			if err != nil {
				return err
			}
			var gone []uuid.UUID
			for _, id := range r.MaintenanceIDs {
				if _, found := ok[id]; !found {
					gone = append(gone, id)
				}
			}
			if len(gone) > 0 {
				n, err := s.repo.RemoveMaintenanceRefs(ctx, gid, gone)
				if err != nil {
					return err
				}
				log.Info().Str("group", gid.String()).Int64("rows", n).Msg("projects: cleared deleted maintenance references")
			}
		}
	}
	return nil
}
