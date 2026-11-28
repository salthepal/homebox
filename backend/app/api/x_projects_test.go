package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

type projectsEnv struct {
	t      *testing.T
	a      *app
	router *chi.Mux
	deps   xapi.Deps
	tokA   string
	gidA   uuid.UUID
	tokB   string
	gidB   uuid.UUID
}

// newProjectsEnv builds the real middleware stack over a migrated database with two groups,
// each with one logged-in user.
func newProjectsEnv(t *testing.T) *projectsEnv {
	t.Helper()
	ctx := context.Background()
	a, tokA, gidA, deps := newCustomTestApp(t)

	dialect := "sqlite3"
	if xtest.IsPostgres() {
		dialect = "postgres"
	}
	deps.Projects = newProjectsService(a, deps.Ent, a.db.Sql(), dialect)

	groupB, err := a.repos.Groups.GroupCreate(ctx, "x-test-b", uuid.Nil)
	require.NoError(t, err)
	const emailB, pw = "x-test-b@example.com", "correct horse battery staple b"
	hash, err := hasher.HashPassword(pw)
	require.NoError(t, err)
	_, err = a.repos.Users.Create(ctx, repo.UserCreate{
		Name: "X Test B", Email: emailB, Password: &hash, DefaultGroupID: groupB.ID,
	})
	require.NoError(t, err)
	tokensB, err := a.services.User.Login(ctx, emailB, pw, false)
	require.NoError(t, err)

	r := chi.NewRouter()
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	mountCustomRoutes(a, r, chain, deps)
	return &projectsEnv{t: t, a: a, router: r, deps: deps, tokA: tokA, gidA: gidA, tokB: tokensB.Raw, gidB: groupB.ID}
}

func (e *projectsEnv) do(method, path, token string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		require.NoError(e.t, err)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1/x"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func (e *projectsEnv) createProject(token string, body map[string]any) xapi.ProjectDTO {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/projects", token, body)
	require.Equal(e.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeInto[xapi.ProjectDTO](e.t, rec)
}

func (e *projectsEnv) addStep(token string, pid uuid.UUID, body map[string]any) xapi.StepDTO {
	e.t.Helper()
	rec := e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps", pid), token, body)
	require.Equal(e.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeInto[xapi.StepDTO](e.t, rec)
}

func (e *projectsEnv) getProject(token string, pid uuid.UUID) xapi.ProjectDTO {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/projects/%s", pid), token, nil)
	require.Equal(e.t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeInto[xapi.ProjectDTO](e.t, rec)
}

// createItem creates a real upstream item (under a container) in group gid.
func (e *projectsEnv) createItem(gid uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	locET, err := e.a.repos.EntityTypes.GetDefault(ctx, gid, true)
	require.NoError(e.t, err)
	itemET, err := e.a.repos.EntityTypes.GetDefault(ctx, gid, false)
	require.NoError(e.t, err)
	loc, err := e.a.repos.Entities.Create(ctx, gid, repo.EntityCreate{Name: "Loc " + name, EntityTypeID: locET.ID})
	require.NoError(e.t, err)
	item, err := e.a.repos.Entities.Create(ctx, gid, repo.EntityCreate{Name: name, ParentID: loc.ID, EntityTypeID: itemET.ID})
	require.NoError(e.t, err)
	return item.ID
}

func (e *projectsEnv) maintenance(gid, itemID uuid.UUID) []repo.MaintenanceEntryWithDetails {
	e.t.Helper()
	out, err := e.a.repos.MaintEntry.GetMaintenanceByItemID(context.Background(), gid, itemID,
		repo.MaintenanceFilters{Status: repo.MaintenanceFilterStatusBoth})
	require.NoError(e.t, err)
	return out
}

func TestCustomProjectsHappyPath(t *testing.T) {
	e := newProjectsEnv(t)
	nas := e.createItem(e.gidA, "NAS")
	switchItem := e.createItem(e.gidA, "Switch")

	// Create.
	p := e.createProject(e.tokA, map[string]any{
		"name": "Upgrade NAS to 10GbE", "description": "faster", "priority": 2,
		"target_date": "2027-03-14", "budget": 300.5,
	})
	assert.Equal(t, "idea", p.Status)
	require.NotNil(t, p.TargetDate)
	assert.Equal(t, "2027-03-14", *p.TargetDate)
	assert.Empty(t, p.Steps)
	assert.Empty(t, p.EntityIDs)
	assert.Equal(t, 0, p.Progress.Total)
	assert.False(t, p.AllStepsComplete)

	// Steps.
	s1 := e.addStep(e.tokA, p.ID, map[string]any{
		"title": "Buy NIC", "est_cost": 80, "url": "https://example.com/nic", "due_date": "2027-01-10",
		"entity_id": nas, "log_as_maintenance": true, "notes": "Intel X550",
	})
	s2 := e.addStep(e.tokA, p.ID, map[string]any{"title": "Install NIC", "est_cost": 0})
	s3 := e.addStep(e.tokA, p.ID, map[string]any{"title": "Buy cables", "est_cost": 20})
	assert.Equal(t, []int{0, 1, 2}, []int{s1.Position, s2.Position, s3.Position})
	require.NotNil(t, s1.DueDate)
	assert.Equal(t, "2027-01-10", *s1.DueDate)

	// Reorder.
	rec := e.do(http.MethodPatch, fmt.Sprintf("/projects/%s/steps/order", p.ID), e.tokA,
		map[string]any{"step_ids": []uuid.UUID{s3.ID, s1.ID, s2.ID}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeInto[xapi.ProjectDTO](t, rec)
	require.Len(t, got.Steps, 3)
	assert.Equal(t, []uuid.UUID{s3.ID, s1.ID, s2.ID}, []uuid.UUID{got.Steps[0].ID, got.Steps[1].ID, got.Steps[2].ID})
	assert.InDelta(t, 100, got.EstCostTotal, 0.001)

	// Link items; names are resolved from upstream.
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", p.ID), e.tokA,
		map[string]any{"entity_ids": []uuid.UUID{nas, switchItem}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = decodeInto[xapi.ProjectDTO](t, rec)
	assert.ElementsMatch(t, []uuid.UUID{nas, switchItem}, got.EntityIDs)
	names := map[string]bool{}
	for _, en := range got.Entities {
		names[en.Name] = true
	}
	assert.Equal(t, map[string]bool{"NAS": true, "Switch": true}, names)

	// Moving the first step to doing starts the project.
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/steps/%s", p.ID, s1.ID), e.tokA, map[string]any{
		"title": "Buy NIC", "status": "doing", "est_cost": 80, "entity_id": nas, "log_as_maintenance": true, "notes": "Intel X550",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = e.getProject(e.tokA, p.ID)
	assert.Equal(t, "in_progress", got.Status)
	assert.NotNil(t, got.StartedAt)

	// Complete with logging: a real upstream maintenance entry appears on the item.
	require.Empty(t, e.maintenance(e.gidA, nas))
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, s1.ID), e.tokA,
		map[string]any{"actual_cost": 72.25})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	done := decodeInto[xapi.StepDTO](t, rec)
	assert.Equal(t, "done", done.Status)
	assert.NotNil(t, done.CompletedAt)
	require.NotNil(t, done.MaintenanceEntryID)
	require.NotNil(t, done.ActualCost)
	assert.InDelta(t, 72.25, *done.ActualCost, 0.001)

	entries := e.maintenance(e.gidA, nas)
	require.Len(t, entries, 1)
	assert.Equal(t, *done.MaintenanceEntryID, entries[0].ID)
	assert.Equal(t, "Buy NIC", entries[0].Name)
	assert.Contains(t, entries[0].Description, "Intel X550")
	assert.Contains(t, entries[0].Description, "From project: Upgrade NAS to 10GbE")
	assert.InDelta(t, 72.25, entries[0].Cost, 0.001)
	assert.False(t, entries[0].CompletedDate.Time().IsZero(), "entry is completed")

	// Completing again conflicts and doesn't log twice.
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, s1.ID), e.tokA, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Len(t, e.maintenance(e.gidA, nas), 1)

	// Complete without logging (empty body is fine; explicit false overrides the step flag).
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, s2.ID), e.tokA,
		map[string]any{"log_as_maintenance": false})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, s3.ID), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, decodeInto[xapi.StepDTO](t, rec).MaintenanceEntryID)

	// Progress and totals; the project is NOT auto-closed.
	got = e.getProject(e.tokA, p.ID)
	assert.Equal(t, xapi.ProgressDTO{Done: 3, Total: 3}, got.Progress)
	assert.True(t, got.AllStepsComplete)
	assert.Equal(t, "in_progress", got.Status)
	assert.InDelta(t, 72.25, got.ActualCostTotal, 0.001)

	// Update the project (the UI's "mark done" prompt).
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s", p.ID), e.tokA, map[string]any{
		"name": "Upgrade NAS to 10GbE", "status": "done", "priority": 2,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = decodeInto[xapi.ProjectDTO](t, rec)
	assert.Equal(t, "done", got.Status)
	assert.NotNil(t, got.CompletedAt)
	assert.Nil(t, got.TargetDate, "PUT is a full replace")

	// List + filters + by-entity.
	other := e.createProject(e.tokA, map[string]any{"name": "Second", "status": "planning"})
	rec = e.do(http.MethodGet, "/projects", e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, decodeInto[[]xapi.ProjectDTO](t, rec), 2)
	rec = e.do(http.MethodGet, "/projects?status=planning", e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	list := decodeInto[[]xapi.ProjectDTO](t, rec)
	require.Len(t, list, 1)
	assert.Equal(t, other.ID, list[0].ID)
	rec = e.do(http.MethodGet, "/projects?status=bogus", e.tokA, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = e.do(http.MethodGet, fmt.Sprintf("/entities/%s/projects", switchItem), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	byItem := decodeInto[[]xapi.ProjectDTO](t, rec)
	require.Len(t, byItem, 1)
	assert.Equal(t, p.ID, byItem[0].ID)
	rec = e.do(http.MethodGet, fmt.Sprintf("/entities/%s/projects", uuid.New()), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())

	// Delete a step, then the project.
	rec = e.do(http.MethodDelete, fmt.Sprintf("/projects/%s/steps/%s", p.ID, s2.ID), e.tokA, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Len(t, e.getProject(e.tokA, p.ID).Steps, 2)
	rec = e.do(http.MethodDelete, fmt.Sprintf("/projects/%s", p.ID), e.tokA, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = e.do(http.MethodGet, fmt.Sprintf("/projects/%s", p.ID), e.tokA, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	// The maintenance entry the step logged stays: it's upstream's data now.
	assert.Len(t, e.maintenance(e.gidA, nas), 1)
}

func TestCustomProjectsCompleteStepRules(t *testing.T) {
	e := newProjectsEnv(t)
	item := e.createItem(e.gidA, "Printer")
	p := e.createProject(e.tokA, map[string]any{"name": "P"})

	noItem := e.addStep(e.tokA, p.ID, map[string]any{"title": "no item"})
	// Logging requested but the step has no item: 400, step stays open.
	rec := e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, noItem.ID), e.tokA,
		map[string]any{"log_as_maintenance": true})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, "todo", e.getProject(e.tokA, p.ID).Steps[0].Status)

	// Negative cost is rejected.
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, noItem.ID), e.tokA,
		map[string]any{"actual_cost": -1})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// A step with an item and no explicit flag doesn't log unless the step says so.
	plain := e.addStep(e.tokA, p.ID, map[string]any{"title": "plain", "entity_id": item})
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, plain.ID), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, e.maintenance(e.gidA, item))

	// An explicit true logs even though the step flag is off.
	forced := e.addStep(e.tokA, p.ID, map[string]any{"title": "forced", "entity_id": item})
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, forced.ID), e.tokA,
		map[string]any{"log_as_maintenance": true})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, e.maintenance(e.gidA, item), 1)
}

func TestCustomProjectsCrossGroupIsolation(t *testing.T) {
	e := newProjectsEnv(t)
	itemA := e.createItem(e.gidA, "A's NAS")
	itemB := e.createItem(e.gidB, "B's NAS")

	pa := e.createProject(e.tokA, map[string]any{"name": "A project"})
	sa := e.addStep(e.tokA, pa.ID, map[string]any{"title": "A step", "entity_id": itemA, "log_as_maintenance": true})
	sa2 := e.addStep(e.tokA, pa.ID, map[string]any{"title": "A step 2"})
	rec := e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", pa.ID), e.tokA, map[string]any{"entity_ids": []uuid.UUID{itemA}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Group B gets 404 on every route that names A's project or step, and nothing changes.
	pid, sid := pa.ID, sa.ID
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/projects/%s", pid), nil},
		{http.MethodPut, fmt.Sprintf("/projects/%s", pid), map[string]any{"name": "hijacked"}},
		{http.MethodDelete, fmt.Sprintf("/projects/%s", pid), nil},
		{http.MethodPost, fmt.Sprintf("/projects/%s/steps", pid), map[string]any{"title": "injected"}},
		{http.MethodPut, fmt.Sprintf("/projects/%s/steps/%s", pid, sid), map[string]any{"title": "hijacked"}},
		{http.MethodDelete, fmt.Sprintf("/projects/%s/steps/%s", pid, sid), nil},
		{http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", pid, sid), map[string]any{}},
		{http.MethodPatch, fmt.Sprintf("/projects/%s/steps/order", pid), map[string]any{"step_ids": []uuid.UUID{sa2.ID, sa.ID}}},
		{http.MethodPut, fmt.Sprintf("/projects/%s/entities", pid), map[string]any{"entity_ids": []uuid.UUID{}}},
	} {
		rec := e.do(tc.method, tc.path, e.tokB, tc.body)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s -> %s", tc.method, tc.path, rec.Body.String())
	}

	// B's lists don't include A's data.
	rec = e.do(http.MethodGet, "/projects", e.tokB, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
	rec = e.do(http.MethodGet, fmt.Sprintf("/entities/%s/projects", itemA), e.tokB, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())

	// B can't link A's item to B's own project or to a step, and can't log maintenance on it.
	pb := e.createProject(e.tokB, map[string]any{"name": "B project"})
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", pb.ID), e.tokB, map[string]any{"entity_ids": []uuid.UUID{itemA}})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps", pb.ID), e.tokB, map[string]any{"title": "x", "entity_id": itemA})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	// B's project can't reach A's step either.
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/steps/%s", pb.ID, sid), e.tokB, map[string]any{"title": "hijacked"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	// B can link its own item.
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", pb.ID), e.tokB, map[string]any{"entity_ids": []uuid.UUID{itemB}})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Nothing of A's changed, and no maintenance entry was created on A's item.
	got := e.getProject(e.tokA, pa.ID)
	assert.Equal(t, "A project", got.Name)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, "A step", got.Steps[0].Title)
	assert.Equal(t, "todo", got.Steps[0].Status)
	assert.Equal(t, []uuid.UUID{itemA}, got.EntityIDs)
	assert.Empty(t, e.maintenance(e.gidA, itemA))
}

func TestCustomProjectsValidationAndAuth(t *testing.T) {
	e := newProjectsEnv(t)
	p := e.createProject(e.tokA, map[string]any{"name": "P"})
	s := e.addStep(e.tokA, p.ID, map[string]any{"title": "S"})

	// Unauthenticated.
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/projects", "", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPost, "/projects", "", map[string]any{"name": "x"}).Code)

	bad := []struct {
		name, method, path string
		body               any
	}{
		{"empty name", http.MethodPost, "/projects", map[string]any{"name": " "}},
		{"bad status", http.MethodPost, "/projects", map[string]any{"name": "x", "status": "bogus"}},
		{"bad priority", http.MethodPost, "/projects", map[string]any{"name": "x", "priority": 9}},
		{"bad date", http.MethodPost, "/projects", map[string]any{"name": "x", "target_date": "tomorrow"}},
		{"unknown field", http.MethodPost, "/projects", map[string]any{"name": "x", "nope": 1}},
		{"malformed json", http.MethodPost, "/projects", "{not json"},
		{"empty body", http.MethodPost, "/projects", nil},
		{"bad project id", http.MethodGet, "/projects/not-a-uuid", nil},
		{"empty step title", http.MethodPost, fmt.Sprintf("/projects/%s/steps", p.ID), map[string]any{"title": ""}},
		{"bad step status", http.MethodPost, fmt.Sprintf("/projects/%s/steps", p.ID), map[string]any{"title": "x", "status": "nope"}},
		{"negative cost", http.MethodPost, fmt.Sprintf("/projects/%s/steps", p.ID), map[string]any{"title": "x", "est_cost": -5}},
		{"bad due date", http.MethodPut, fmt.Sprintf("/projects/%s/steps/%s", p.ID, s.ID), map[string]any{"title": "x", "due_date": "13/13/2027"}},
		{"unknown item", http.MethodPost, fmt.Sprintf("/projects/%s/steps", p.ID), map[string]any{"title": "x", "entity_id": uuid.New()}},
		{"link unknown item", http.MethodPut, fmt.Sprintf("/projects/%s/entities", p.ID), map[string]any{"entity_ids": []uuid.UUID{uuid.New()}}},
		{"reorder missing step", http.MethodPatch, fmt.Sprintf("/projects/%s/steps/order", p.ID), map[string]any{"step_ids": []uuid.UUID{}}},
		{"reorder foreign step", http.MethodPatch, fmt.Sprintf("/projects/%s/steps/order", p.ID), map[string]any{"step_ids": []uuid.UUID{uuid.New()}}},
	}
	for _, tc := range bad {
		rec := e.do(tc.method, tc.path, e.tokA, tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", tc.name, rec.Body.String())
	}

	// Nothing was created by the rejected requests.
	got := e.getProject(e.tokA, p.ID)
	assert.Len(t, got.Steps, 1)
	assert.Empty(t, got.EntityIDs)
	rec := e.do(http.MethodGet, "/projects", e.tokA, nil)
	assert.Len(t, decodeInto[[]xapi.ProjectDTO](t, rec), 1)

	// Unknown IDs are 404.
	assert.Equal(t, http.StatusNotFound, e.do(http.MethodGet, fmt.Sprintf("/projects/%s", uuid.New()), e.tokA, nil).Code)
	assert.Equal(t, http.StatusNotFound, e.do(http.MethodDelete, fmt.Sprintf("/projects/%s/steps/%s", p.ID, uuid.New()), e.tokA, nil).Code)
}

func TestCustomProjectsSweepOrphans(t *testing.T) {
	e := newProjectsEnv(t)
	ctx := context.Background()
	keepItem := e.createItem(e.gidA, "Keep")
	goneItem := e.createItem(e.gidA, "Gone")
	bItem := e.createItem(e.gidB, "B item")

	p := e.createProject(e.tokA, map[string]any{"name": "P"})
	rec := e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", p.ID), e.tokA,
		map[string]any{"entity_ids": []uuid.UUID{keepItem, goneItem}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	logged := e.addStep(e.tokA, p.ID, map[string]any{"title": "logged", "entity_id": keepItem, "log_as_maintenance": true})
	onGone := e.addStep(e.tokA, p.ID, map[string]any{"title": "on gone", "entity_id": goneItem})
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps/%s/complete", p.ID, logged.ID), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	entryID := *decodeInto[xapi.StepDTO](t, rec).MaintenanceEntryID

	pb := e.createProject(e.tokB, map[string]any{"name": "B"})
	rec = e.do(http.MethodPut, fmt.Sprintf("/projects/%s/entities", pb.ID), e.tokB, map[string]any{"entity_ids": []uuid.UUID{bItem}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Nothing is orphaned yet: the sweep changes nothing.
	require.NoError(t, e.deps.Projects.SweepOrphans(ctx))
	got := e.getProject(e.tokA, p.ID)
	assert.Len(t, got.EntityIDs, 2)
	assert.NotNil(t, got.Steps[0].MaintenanceEntryID)

	// Upstream deletes the maintenance entry: only the step's reference is cleared.
	require.NoError(t, e.a.repos.MaintEntry.Delete(ctx, e.gidA, entryID))
	require.NoError(t, e.deps.Projects.SweepOrphans(ctx))
	got = e.getProject(e.tokA, p.ID)
	assert.Nil(t, got.Steps[0].MaintenanceEntryID)
	assert.Equal(t, "done", got.Steps[0].Status)
	assert.Len(t, got.EntityIDs, 2)

	// Upstream deletes an item: its link goes and the step's item is nulled.
	require.NoError(t, e.a.repos.Entities.DeleteByGroup(ctx, e.gidA, goneItem))
	require.NoError(t, e.deps.Projects.SweepOrphans(ctx))
	got = e.getProject(e.tokA, p.ID)
	assert.Equal(t, []uuid.UUID{keepItem}, got.EntityIDs)
	for _, s := range got.Steps {
		if s.ID == onGone.ID {
			assert.Nil(t, s.EntityID)
		} else {
			require.NotNil(t, s.EntityID)
			assert.Equal(t, keepItem, *s.EntityID)
		}
	}

	// The other group's links are untouched.
	assert.Equal(t, []uuid.UUID{bItem}, e.getProject(e.tokB, pb.ID).EntityIDs)
}
