package projects_test

import (
	"context"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

func newRepo(t *testing.T) (*projects.Repo, xtest.DB) {
	t.Helper()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(context.Background(), d.SQL, d.Driver)
	require.NoError(t, err)
	client := xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))
	return projects.NewRepo(client, d.SQL, d.Dialect), d
}

func fp(f float64) *float64 { return &f }

func count(t *testing.T, d xtest.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, d.SQL.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestXProjectsCRUD(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	target := time.Date(2027, 3, 14, 15, 9, 0, 0, time.UTC)

	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{
		Name: "  Upgrade NAS to 10GbE ", Description: "d", Priority: 2, TargetDate: &target, Budget: fp(250.5),
	})
	require.NoError(t, err)
	assert.Equal(t, "Upgrade NAS to 10GbE", p.Name)
	assert.Equal(t, projects.StatusIdea, p.Status)
	require.NotNil(t, p.TargetDate)
	assert.Equal(t, "2027-03-14", p.TargetDate.Format("2006-01-02"))
	require.NotNil(t, p.Budget)
	assert.InDelta(t, 250.5, *p.Budget, 0.001)
	assert.Empty(t, p.Steps)
	assert.Empty(t, p.EntityIDs)

	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)

	// Status transitions maintain timestamps.
	up, err := r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "NAS", Status: projects.StatusInProgress})
	require.NoError(t, err)
	assert.Equal(t, projects.StatusInProgress, up.Status)
	assert.NotNil(t, up.StartedAt)
	assert.Nil(t, up.TargetDate, "full replace clears omitted optional fields")
	assert.Nil(t, up.Budget)

	up, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "NAS", Status: projects.StatusDone})
	require.NoError(t, err)
	assert.NotNil(t, up.CompletedAt)
	up, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "NAS", Status: projects.StatusOnHold})
	require.NoError(t, err)
	assert.Nil(t, up.CompletedAt)
	assert.NotNil(t, up.StartedAt)

	// Empty status keeps the current one.
	up, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "NAS2"})
	require.NoError(t, err)
	assert.Equal(t, projects.StatusOnHold, up.Status)

	// Filtering.
	_, err = r.CreateProject(ctx, gid, projects.ProjectInput{Name: "Other", Status: projects.StatusPlanning})
	require.NoError(t, err)
	all, err := r.ListProjects(ctx, gid, projects.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
	planning, err := r.ListProjects(ctx, gid, projects.ListFilter{Status: projects.StatusPlanning})
	require.NoError(t, err)
	require.Len(t, planning, 1)
	assert.Equal(t, "Other", planning[0].Name)

	// Validation.
	_, err = r.CreateProject(ctx, gid, projects.ProjectInput{Name: " "})
	require.ErrorIs(t, err, projects.ErrInvalid)
	_, err = r.CreateProject(ctx, gid, projects.ProjectInput{Name: "x", Status: "bogus"})
	require.ErrorIs(t, err, projects.ErrInvalid)
	_, err = r.CreateProject(ctx, gid, projects.ProjectInput{Name: "x", Priority: 4})
	require.ErrorIs(t, err, projects.ErrInvalid)

	require.NoError(t, r.DeleteProject(ctx, gid, p.ID))
	_, err = r.GetProject(ctx, gid, p.ID)
	require.ErrorIs(t, err, projects.ErrNotFound)
	require.ErrorIs(t, r.DeleteProject(ctx, gid, p.ID), projects.ErrNotFound)
}

func TestXProjectsSteps(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)

	eid := uuid.New()
	url := "https://example.com/nic"
	due := time.Date(2027, 1, 2, 22, 0, 0, 0, time.UTC)
	s1, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{
		Title: "Buy NIC", Notes: "n", EstCost: fp(80), URL: &url, EntityID: &eid, DueDate: &due, LogAsMaintenance: true,
	})
	require.NoError(t, err)
	s2, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "Install"})
	require.NoError(t, err)
	s3, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "Test"})
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 2}, []int{s1.Position, s2.Position, s3.Position})
	assert.Equal(t, projects.StepTodo, s1.Status)
	assert.Equal(t, "2027-01-02", s1.DueDate.Format("2006-01-02"))
	require.NotNil(t, s1.URL)
	assert.Equal(t, url, *s1.URL)
	assert.Equal(t, &eid, s1.EntityID)
	assert.True(t, s1.LogAsMaintenance)

	// Update + status transitions.
	s2u, err := r.UpdateStep(ctx, gid, p.ID, s2.ID, projects.StepInput{Title: "Install card", Status: projects.StepDone, ActualCost: fp(12)})
	require.NoError(t, err)
	assert.Equal(t, "Install card", s2u.Title)
	assert.NotNil(t, s2u.CompletedAt)
	require.NotNil(t, s2u.ActualCost)
	s2u, err = r.UpdateStep(ctx, gid, p.ID, s2.ID, projects.StepInput{Title: "Install card", Status: projects.StepDoing})
	require.NoError(t, err)
	assert.Nil(t, s2u.CompletedAt)
	assert.Nil(t, s2u.ActualCost, "full replace clears omitted optional fields")

	// CompleteStep.
	mid := uuid.New()
	done, err := r.CompleteStep(ctx, gid, p.ID, s1.ID, projects.StepCompletion{
		ActualCost: fp(75.25), LogAsMaintenance: true, MaintenanceEntryID: &mid,
	})
	require.NoError(t, err)
	assert.Equal(t, projects.StepDone, done.Status)
	assert.NotNil(t, done.CompletedAt)
	assert.Equal(t, &mid, done.MaintenanceEntryID)
	assert.InDelta(t, 75.25, *done.ActualCost, 0.001)

	// Validation.
	_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: ""})
	require.ErrorIs(t, err, projects.ErrInvalid)
	_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "x", Status: "nope"})
	require.ErrorIs(t, err, projects.ErrInvalid)

	// Delete closes the gap.
	require.NoError(t, r.DeleteStep(ctx, gid, p.ID, s1.ID))
	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, s2.ID, got.Steps[0].ID)
	assert.Equal(t, 0, got.Steps[0].Position)
	assert.Equal(t, 1, got.Steps[1].Position)
	require.ErrorIs(t, r.DeleteStep(ctx, gid, p.ID, s1.ID), projects.ErrNotFound)
}

func TestXProjectsReorder(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	var ids []uuid.UUID
	for _, title := range []string{"a", "b", "c"} {
		s, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: title})
		require.NoError(t, err)
		ids = append(ids, s.ID)
	}

	require.NoError(t, r.ReorderSteps(ctx, gid, p.ID, []uuid.UUID{ids[2], ids[0], ids[1]}))
	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	var titles []string
	for _, s := range got.Steps {
		titles = append(titles, s.Title)
	}
	assert.Equal(t, []string{"c", "a", "b"}, titles)

	// Bad orders change nothing.
	other, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "Q"})
	require.NoError(t, err)
	foreign, err := r.AddStep(ctx, gid, other.ID, projects.StepInput{Title: "z"})
	require.NoError(t, err)
	for _, bad := range [][]uuid.UUID{
		{ids[0], ids[1]},                     // missing one
		{ids[0], ids[0], ids[1]},             // duplicate
		{ids[0], ids[1], foreign.ID},         // step from another project
		{ids[0], ids[1], ids[2], uuid.New()}, // extra
	} {
		require.ErrorIs(t, r.ReorderSteps(ctx, gid, p.ID, bad), projects.ErrInvalid)
	}
	got, err = r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "c", got.Steps[0].Title)
}

func TestXProjectsEntityLinks(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p1, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P1"})
	require.NoError(t, err)
	p2, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P2"})
	require.NoError(t, err)
	e1, e2, e3 := uuid.New(), uuid.New(), uuid.New()

	require.NoError(t, r.ReplaceEntities(ctx, gid, p1.ID, []uuid.UUID{e1, e2, e1}))
	require.NoError(t, r.ReplaceEntities(ctx, gid, p2.ID, []uuid.UUID{e2}))
	got, err := r.GetProject(ctx, gid, p1.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{e1, e2}, got.EntityIDs)

	// Replace, not append.
	require.NoError(t, r.ReplaceEntities(ctx, gid, p1.ID, []uuid.UUID{e3}))
	got, err = r.GetProject(ctx, gid, p1.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{e3}, got.EntityIDs)

	byE2, err := r.ListByEntity(ctx, gid, e2)
	require.NoError(t, err)
	require.Len(t, byE2, 1)
	assert.Equal(t, p2.ID, byE2[0].ID)
	none, err := r.ListByEntity(ctx, gid, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)

	// Clearing.
	require.NoError(t, r.ReplaceEntities(ctx, gid, p1.ID, nil))
	got, err = r.GetProject(ctx, gid, p1.ID)
	require.NoError(t, err)
	assert.Empty(t, got.EntityIDs)

	// List includes links.
	all, err := r.ListProjects(ctx, gid, projects.ListFilter{})
	require.NoError(t, err)
	for _, p := range all {
		if p.ID == p2.ID {
			assert.Equal(t, []uuid.UUID{e2}, p.EntityIDs)
		}
	}
}

func TestXProjectsCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	r, d := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	keep, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "keep"})
	require.NoError(t, err)
	for _, title := range []string{"a", "b"} {
		_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: title})
		require.NoError(t, err)
	}
	_, err = r.AddStep(ctx, gid, keep.ID, projects.StepInput{Title: "k"})
	require.NoError(t, err)
	require.NoError(t, r.ReplaceEntities(ctx, gid, p.ID, []uuid.UUID{uuid.New(), uuid.New()}))
	require.NoError(t, r.ReplaceEntities(ctx, gid, keep.ID, []uuid.UUID{uuid.New()}))

	require.NoError(t, r.DeleteProject(ctx, gid, p.ID))
	assert.Equal(t, 1, count(t, d, "x_project_steps"))
	assert.Equal(t, 1, count(t, d, "x_project_entities"))
	assert.Equal(t, 1, count(t, d, "x_projects"))
}

// The database-level cascade (not just the repo's explicit deletes) removes children.
func TestXProjectsDBCascade(t *testing.T) {
	ctx := context.Background()
	r, d := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "a"})
	require.NoError(t, err)
	require.NoError(t, r.ReplaceEntities(ctx, gid, p.ID, []uuid.UUID{uuid.New()}))

	if d.Driver == "sqlite3" {
		_, err = d.SQL.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)
	}
	_, err = d.SQL.Exec("DELETE FROM x_projects")
	require.NoError(t, err)
	assert.Equal(t, 0, count(t, d, "x_project_steps"))
	assert.Equal(t, 0, count(t, d, "x_project_entities"))
}

func TestXProjectsGroupIsolation(t *testing.T) {
	ctx := context.Background()
	r, d := newRepo(t)
	a, b := uuid.New(), uuid.New()
	e := uuid.New()

	pa, err := r.CreateProject(ctx, a, projects.ProjectInput{Name: "A's project"})
	require.NoError(t, err)
	sa, err := r.AddStep(ctx, a, pa.ID, projects.StepInput{Title: "A step"})
	require.NoError(t, err)
	sa2, err := r.AddStep(ctx, a, pa.ID, projects.StepInput{Title: "A step 2"})
	require.NoError(t, err)
	require.NoError(t, r.ReplaceEntities(ctx, a, pa.ID, []uuid.UUID{e}))

	// Group B sees nothing and can change nothing.
	_, err = r.GetProject(ctx, b, pa.ID)
	require.ErrorIs(t, err, projects.ErrNotFound)
	list, err := r.ListProjects(ctx, b, projects.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, list)
	byE, err := r.ListByEntity(ctx, b, e)
	require.NoError(t, err)
	assert.Empty(t, byE)

	_, err = r.UpdateProject(ctx, b, pa.ID, projects.ProjectInput{Name: "hijacked"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	require.ErrorIs(t, r.DeleteProject(ctx, b, pa.ID), projects.ErrNotFound)

	_, err = r.AddStep(ctx, b, pa.ID, projects.StepInput{Title: "injected"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	_, err = r.GetStep(ctx, b, pa.ID, sa.ID)
	require.ErrorIs(t, err, projects.ErrNotFound)
	_, err = r.UpdateStep(ctx, b, pa.ID, sa.ID, projects.StepInput{Title: "hijacked"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	_, err = r.CompleteStep(ctx, b, pa.ID, sa.ID, projects.StepCompletion{})
	require.ErrorIs(t, err, projects.ErrNotFound)
	require.ErrorIs(t, r.DeleteStep(ctx, b, pa.ID, sa.ID), projects.ErrNotFound)
	require.ErrorIs(t, r.ReorderSteps(ctx, b, pa.ID, []uuid.UUID{sa2.ID, sa.ID}), projects.ErrNotFound)
	require.ErrorIs(t, r.ReplaceEntities(ctx, b, pa.ID, nil), projects.ErrNotFound)

	// Group B's own project can't reach A's steps through its own project ID either.
	pb, err := r.CreateProject(ctx, b, projects.ProjectInput{Name: "B's project"})
	require.NoError(t, err)
	_, err = r.UpdateStep(ctx, b, pb.ID, sa.ID, projects.StepInput{Title: "hijacked"})
	require.ErrorIs(t, err, projects.ErrNotFound)

	// Nothing of A's changed.
	got, err := r.GetProject(ctx, a, pa.ID)
	require.NoError(t, err)
	assert.Equal(t, "A's project", got.Name)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, "A step", got.Steps[0].Title)
	assert.Equal(t, []uuid.UUID{e}, got.EntityIDs)
	assert.Equal(t, 2, count(t, d, "x_project_steps"))
}
