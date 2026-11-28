package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

const maxBody = 1 << 20

// ---- wire types (snake_case JSON; dates "YYYY-MM-DD", timestamps RFC3339) ----

// ProjectDTO is a project as returned by the API.
type ProjectDTO struct {
	ID               uuid.UUID      `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Status           string         `json:"status"`
	Priority         int            `json:"priority"`
	TargetDate       *string        `json:"target_date"`
	Budget           *float64       `json:"budget"`
	StartedAt        *time.Time     `json:"started_at"`
	CompletedAt      *time.Time     `json:"completed_at"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Steps            []StepDTO      `json:"steps"`
	EntityIDs        []uuid.UUID    `json:"entity_ids"`
	Entities         []EntityRefDTO `json:"entities"`
	Progress         ProgressDTO    `json:"progress"`
	EstCostTotal     float64        `json:"est_cost_total"`
	ActualCostTotal  float64        `json:"actual_cost_total"`
	AllStepsComplete bool           `json:"all_steps_complete"`
}

// ProgressDTO counts steps that are done or skipped against all steps.
type ProgressDTO struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// EntityRefDTO is a linked item (only items that still exist are listed).
type EntityRefDTO struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// StepDTO is a project step as returned by the API.
type StepDTO struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	Position           int        `json:"position"`
	Title              string     `json:"title"`
	Notes              string     `json:"notes"`
	Status             string     `json:"status"`
	DueDate            *string    `json:"due_date"`
	EstCost            *float64   `json:"est_cost"`
	ActualCost         *float64   `json:"actual_cost"`
	URL                *string    `json:"url"`
	EntityID           *uuid.UUID `json:"entity_id"`
	LogAsMaintenance   bool       `json:"log_as_maintenance"`
	MaintenanceEntryID *uuid.UUID `json:"maintenance_entry_id"`
	CompletedAt        *time.Time `json:"completed_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ProjectBody is the create/update request body.
type ProjectBody struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    int      `json:"priority"`
	TargetDate  *string  `json:"target_date"`
	Budget      *float64 `json:"budget"`
}

// StepBody is the step create/update request body.
type StepBody struct {
	Title            string     `json:"title"`
	Notes            string     `json:"notes"`
	Status           string     `json:"status"`
	DueDate          *string    `json:"due_date"`
	EstCost          *float64   `json:"est_cost"`
	ActualCost       *float64   `json:"actual_cost"`
	URL              *string    `json:"url"`
	EntityID         *uuid.UUID `json:"entity_id"`
	LogAsMaintenance bool       `json:"log_as_maintenance"`
}

// CompleteBody is the complete-step request body. Both fields are optional.
type CompleteBody struct {
	ActualCost       *float64 `json:"actual_cost"`
	LogAsMaintenance *bool    `json:"log_as_maintenance"`
}

// OrderBody is the reorder request body: every step ID of the project, in the new order.
type OrderBody struct {
	StepIDs []uuid.UUID `json:"step_ids"`
}

// EntitiesBody is the replace-links request body.
type EntitiesBody struct {
	EntityIDs []uuid.UUID `json:"entity_ids"`
}

const dateLayout = "2006-01-02"

func fmtDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(dateLayout)
	return &s
}

func parseDate(field string, s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		t, err = time.Parse(time.RFC3339, v)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be YYYY-MM-DD", projects.ErrInvalid, field)
	}
	return &t, nil
}

func stepDTO(s projects.Step) StepDTO {
	return StepDTO{
		ID: s.ID, ProjectID: s.ProjectID, Position: s.Position, Title: s.Title, Notes: s.Notes,
		Status: s.Status, DueDate: fmtDate(s.DueDate), EstCost: s.EstCost, ActualCost: s.ActualCost,
		URL: s.URL, EntityID: s.EntityID, LogAsMaintenance: s.LogAsMaintenance,
		MaintenanceEntryID: s.MaintenanceEntryID, CompletedAt: s.CompletedAt,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func projectDTO(v projects.View) ProjectDTO {
	d := ProjectDTO{
		ID: v.ID, Name: v.Name, Description: v.Description, Status: v.Status, Priority: v.Priority,
		TargetDate: fmtDate(v.TargetDate), Budget: v.Budget, StartedAt: v.StartedAt,
		CompletedAt: v.CompletedAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		Steps: make([]StepDTO, 0, len(v.Steps)), EntityIDs: v.EntityIDs,
		Entities: make([]EntityRefDTO, 0, len(v.Entities)),
	}
	if d.EntityIDs == nil {
		d.EntityIDs = []uuid.UUID{}
	}
	for _, s := range v.Steps {
		d.Steps = append(d.Steps, stepDTO(s))
		d.Progress.Total++
		if s.Status == projects.StepDone || s.Status == projects.StepSkipped {
			d.Progress.Done++
		}
		if s.EstCost != nil {
			d.EstCostTotal += *s.EstCost
		}
		if s.ActualCost != nil {
			d.ActualCostTotal += *s.ActualCost
		}
	}
	d.AllStepsComplete = d.Progress.Total > 0 && d.Progress.Done == d.Progress.Total
	for _, e := range v.Entities {
		d.Entities = append(d.Entities, EntityRefDTO{ID: e.ID, Name: e.Name})
	}
	return d
}

func projectInput(b ProjectBody) (projects.ProjectInput, error) {
	td, err := parseDate("target_date", b.TargetDate)
	if err != nil {
		return projects.ProjectInput{}, err
	}
	return projects.ProjectInput{
		Name: b.Name, Description: b.Description, Status: b.Status, Priority: b.Priority,
		TargetDate: td, Budget: b.Budget,
	}, nil
}

func stepInput(b StepBody) (projects.StepInput, error) {
	dd, err := parseDate("due_date", b.DueDate)
	if err != nil {
		return projects.StepInput{}, err
	}
	for _, c := range []*float64{b.EstCost, b.ActualCost} {
		if c != nil && *c < 0 {
			return projects.StepInput{}, fmt.Errorf("%w: costs must not be negative", projects.ErrInvalid)
		}
	}
	url := b.URL
	if url != nil && strings.TrimSpace(*url) == "" {
		url = nil
	}
	return projects.StepInput{
		Title: b.Title, Notes: b.Notes, Status: b.Status, DueDate: dd, EstCost: b.EstCost,
		ActualCost: b.ActualCost, URL: url, EntityID: b.EntityID, LogAsMaintenance: b.LogAsMaintenance,
	}, nil
}

// ---- plumbing ----

// mapErr turns repo/service errors into HTTP errors (404 for rows in other groups too).
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, projects.ErrNotFound):
		return validate.NewRequestError(errors.New("not found"), http.StatusNotFound)
	case errors.Is(err, projects.ErrInvalid):
		return validate.NewRequestError(err, http.StatusBadRequest)
	case errors.Is(err, projects.ErrConflict):
		return validate.NewRequestError(err, http.StatusConflict)
	}
	return err
}

func decode[T any](r *http.Request, allowEmpty bool) (T, error) {
	var v T
	body := http.MaxBytesReader(nil, r.Body, maxBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) && allowEmpty {
			return v, nil
		}
		return v, validate.NewRequestError(fmt.Errorf("invalid request body: %w", err), http.StatusBadRequest)
	}
	// Exactly one JSON value: anything after it (another value, garbage) is a 400.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return v, validate.NewRequestError(errors.New("invalid request body: unexpected data after JSON value"), http.StatusBadRequest)
	}
	return v, nil
}

func routeID(r *http.Request, key string) (uuid.UUID, error) {
	return adapters.RouteUUID(r, key)
}

type projectHandlers struct{ svc *projects.Service }

// mountProjects registers the Projects routes (.fork/PLAN.md §6) on r.
func mountProjects(r chi.Router, chain *errchain.ErrChain, userMW []errchain.Middleware, d Deps) {
	h := projectHandlers{svc: d.Projects}
	route := func(method, path string, fn errchain.HandlerFunc) {
		r.MethodFunc(method, path, chain.ToHandlerFunc(fn, userMW...))
	}
	route(http.MethodGet, "/projects", h.list)
	route(http.MethodPost, "/projects", h.create)
	route(http.MethodGet, "/projects/{id}", h.get)
	route(http.MethodPut, "/projects/{id}", h.update)
	route(http.MethodDelete, "/projects/{id}", h.delete)
	route(http.MethodPost, "/projects/{id}/steps", h.addStep)
	route(http.MethodPatch, "/projects/{id}/steps/order", h.reorder)
	route(http.MethodPut, "/projects/{id}/steps/{stepId}", h.updateStep)
	route(http.MethodDelete, "/projects/{id}/steps/{stepId}", h.deleteStep)
	route(http.MethodPost, "/projects/{id}/steps/{stepId}/complete", h.completeStep)
	route(http.MethodPut, "/projects/{id}/entities", h.setEntities)
	route(http.MethodGet, "/entities/{entityId}/projects", h.byEntity)
}

func (h projectHandlers) list(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	vs, err := h.svc.List(r.Context(), gid, projects.ListFilter{Status: r.URL.Query().Get("status")})
	if err != nil {
		return mapErr(err)
	}
	return writeProjects(w, vs)
}

func writeProjects(w http.ResponseWriter, vs []projects.View) error {
	out := make([]ProjectDTO, len(vs))
	for i, v := range vs {
		out[i] = projectDTO(v)
	}
	return server.JSON(w, http.StatusOK, out)
}

func (h projectHandlers) byEntity(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	eid, err := routeID(r, "entityId")
	if err != nil {
		return err
	}
	vs, err := h.svc.ListByEntity(r.Context(), gid, eid)
	if err != nil {
		return mapErr(err)
	}
	return writeProjects(w, vs)
}

func (h projectHandlers) create(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	body, err := decode[ProjectBody](r, false)
	if err != nil {
		return err
	}
	in, err := projectInput(body)
	if err != nil {
		return mapErr(err)
	}
	v, err := h.svc.Create(r.Context(), gid, in)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusCreated, projectDTO(v))
}

func (h projectHandlers) get(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	id, err := routeID(r, "id")
	if err != nil {
		return err
	}
	v, err := h.svc.Get(r.Context(), gid, id)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, projectDTO(v))
}

func (h projectHandlers) update(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	id, err := routeID(r, "id")
	if err != nil {
		return err
	}
	body, err := decode[ProjectBody](r, false)
	if err != nil {
		return err
	}
	in, err := projectInput(body)
	if err != nil {
		return mapErr(err)
	}
	v, err := h.svc.Update(r.Context(), gid, id, in)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, projectDTO(v))
}

func (h projectHandlers) delete(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	id, err := routeID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(r.Context(), gid, id); err != nil {
		return mapErr(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h projectHandlers) addStep(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	body, err := decode[StepBody](r, false)
	if err != nil {
		return err
	}
	in, err := stepInput(body)
	if err != nil {
		return mapErr(err)
	}
	s, err := h.svc.AddStep(r.Context(), gid, pid, in)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusCreated, stepDTO(s))
}

func (h projectHandlers) updateStep(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	sid, err := routeID(r, "stepId")
	if err != nil {
		return err
	}
	body, err := decode[StepBody](r, false)
	if err != nil {
		return err
	}
	in, err := stepInput(body)
	if err != nil {
		return mapErr(err)
	}
	s, err := h.svc.UpdateStep(r.Context(), gid, pid, sid, in)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, stepDTO(s))
}

func (h projectHandlers) deleteStep(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	sid, err := routeID(r, "stepId")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteStep(r.Context(), gid, pid, sid); err != nil {
		return mapErr(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h projectHandlers) reorder(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	body, err := decode[OrderBody](r, false)
	if err != nil {
		return err
	}
	v, err := h.svc.ReorderSteps(r.Context(), gid, pid, body.StepIDs)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, projectDTO(v))
}

func (h projectHandlers) completeStep(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	sid, err := routeID(r, "stepId")
	if err != nil {
		return err
	}
	body, err := decode[CompleteBody](r, true)
	if err != nil {
		return err
	}
	s, err := h.svc.CompleteStep(r.Context(), gid, pid, sid, projects.CompleteInput{
		ActualCost: body.ActualCost, LogAsMaintenance: body.LogAsMaintenance,
	})
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, stepDTO(s))
}

func (h projectHandlers) setEntities(w http.ResponseWriter, r *http.Request) error {
	gid := services.NewContext(r.Context()).GID
	pid, err := routeID(r, "id")
	if err != nil {
		return err
	}
	body, err := decode[EntitiesBody](r, false)
	if err != nil {
		return err
	}
	v, err := h.svc.SetEntities(r.Context(), gid, pid, body.EntityIDs)
	if err != nil {
		return mapErr(err)
	}
	return server.JSON(w, http.StatusOK, projectDTO(v))
}
