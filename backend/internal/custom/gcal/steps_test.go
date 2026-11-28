package gcal_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
)

func fp(f float64) *float64 { return &f }

type stepEnv struct {
	recEnv
	repo *projects.Repo
}

// newStepEnv is a recEnv whose reconciler also syncs project steps from the real custom
// tables (the same provider the app registers).
func newStepEnv(t *testing.T) stepEnv {
	t.Helper()
	r := newRecEnv(t, nil)
	r.rec = gcal.NewReconciler(r.svc, gcal.ReconcilerOptions{Debounce: 30 * time.Millisecond},
		gcal.MaintenanceProvider{Source: r.lister, BaseURL: "https://homebox.example.org"},
		gcal.StepProvider{Source: gcal.EntStepLister{Ent: r.ent}, BaseURL: "https://homebox.example.org"})
	return stepEnv{recEnv: r, repo: projects.NewRepo(r.ent, r.db.SQL, r.db.Dialect)}
}

func (s stepEnv) project(t *testing.T, name string) projects.Project {
	t.Helper()
	p, err := s.repo.CreateProject(context.Background(), s.gid, projects.ProjectInput{Name: name})
	require.NoError(t, err)
	return p
}

func (s stepEnv) step(t *testing.T, p projects.Project, in projects.StepInput) projects.Step {
	t.Helper()
	st, err := s.repo.AddStep(context.Background(), s.gid, p.ID, in)
	require.NoError(t, err)
	return st
}

func TestXReconcileProjectStepDueAppearsThenDone(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "Upgrade NAS to 10GbE")
	url := "https://example.com/nic"
	st := s.step(t, p, projects.StepInput{
		Title: "Buy NIC", Notes: "Intel X550", DueDate: day(2027, 4, 2), EstCost: fp(80), URL: &url,
	})

	assert.Equal(t, gcal.Result{Inserted: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1)
	ev := evs[0]
	assert.Equal(t, "📋 Upgrade NAS to 10GbE: Buy NIC", ev.Summary)
	assert.Equal(t, "2027-04-02", ev.Start.Date)
	assert.Equal(t, "2027-04-03", ev.End.Date)
	assert.Contains(t, ev.Description, "Intel X550")
	assert.Contains(t, ev.Description, "Estimated cost: $80.00")
	assert.Contains(t, ev.Description, url)
	assert.Contains(t, ev.Description, "https://homebox.example.org/x/projects/"+p.ID.String())
	require.NotNil(t, ev.Reminders)
	require.Len(t, ev.Reminders.Overrides, 1, "an open step reminds")
	assert.True(t, ev.IsOurs())
	assert.Equal(t, st.ID.String(), ev.ExtendedProperties.Private[gcal.IDProperty])
	assert.Equal(t, gcal.SourceProjectStep, ev.ExtendedProperties.Private[gcal.TypeProperty])

	// Unchanged on a second run.
	assert.Equal(t, gcal.Result{Unchanged: 1}, s.sync(t))

	// Completing turns it into a ✅ with no reminder and the actual cost.
	_, err := s.repo.CompleteStep(context.Background(), s.gid, p.ID, st.ID, projects.StepCompletion{ActualCost: fp(75.5)})
	require.NoError(t, err)
	assert.Equal(t, gcal.Result{Patched: 1}, s.sync(t))
	evs = s.events()
	require.Len(t, evs, 1)
	assert.Equal(t, "✅ Upgrade NAS to 10GbE: Buy NIC", evs[0].Summary)
	assert.Equal(t, "2027-04-02", evs[0].Start.Date, "stays on the due date")
	assert.Contains(t, evs[0].Description, "Cost: $75.50")
	assert.Empty(t, evs[0].Reminders.Overrides)
}

func TestXReconcileProjectStepsWithoutDueDateAreIgnored(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "P")
	s.step(t, p, projects.StepInput{Title: "no date"})
	s.step(t, p, projects.StepInput{Title: "has date", DueDate: day(2027, 5, 1)})

	assert.Equal(t, gcal.Result{Inserted: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1)
	assert.Contains(t, evs[0].Summary, "has date")

	// Clearing the due date removes the event.
	steps, err := s.repo.GetProject(context.Background(), s.gid, p.ID)
	require.NoError(t, err)
	for _, st := range steps.Steps {
		if st.Title == "has date" {
			_, err := s.repo.UpdateStep(context.Background(), s.gid, p.ID, st.ID, projects.StepInput{Title: st.Title})
			require.NoError(t, err)
		}
	}
	assert.Equal(t, gcal.Result{Deleted: 1}, s.sync(t))
	assert.Empty(t, s.events())
}

func TestXReconcileProjectStepAndProjectDeleteRemoveEvents(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "Keep")
	q := s.project(t, "Drop")
	keep := s.step(t, p, projects.StepInput{Title: "k", DueDate: day(2027, 6, 1)})
	gone := s.step(t, p, projects.StepInput{Title: "g", DueDate: day(2027, 6, 2)})
	s.step(t, q, projects.StepInput{Title: "q1", DueDate: day(2027, 6, 3)})
	s.step(t, q, projects.StepInput{Title: "q2", DueDate: day(2027, 6, 4)})
	assert.Equal(t, gcal.Result{Inserted: 4}, s.sync(t))

	require.NoError(t, s.repo.DeleteStep(context.Background(), s.gid, p.ID, gone.ID))
	assert.Equal(t, gcal.Result{Deleted: 1, Unchanged: 3}, s.sync(t))

	require.NoError(t, s.repo.DeleteProject(context.Background(), s.gid, q.ID))
	assert.Equal(t, gcal.Result{Deleted: 2, Unchanged: 1}, s.sync(t))

	evs := s.events()
	require.Len(t, evs, 1)
	assert.Equal(t, keep.ID.String(), evs[0].ExtendedProperties.Private[gcal.IDProperty])
}

func TestXReconcileProjectStepsSkippedAndCancelledProjects(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "P")
	sk := s.step(t, p, projects.StepInput{Title: "skipme", DueDate: day(2027, 7, 1)})
	s.step(t, p, projects.StepInput{Title: "live", DueDate: day(2027, 7, 2)})
	c := s.project(t, "C")
	s.step(t, c, projects.StepInput{Title: "in cancelled", DueDate: day(2027, 7, 3)})
	_, err := s.repo.UpdateProject(context.Background(), s.gid, c.ID, projects.ProjectInput{Name: "C", Status: projects.StatusCancelled})
	require.NoError(t, err)
	_, err = s.repo.UpdateStep(context.Background(), s.gid, p.ID, sk.ID,
		projects.StepInput{Title: "skipme", Status: projects.StepSkipped, DueDate: day(2027, 7, 1)})
	require.NoError(t, err)

	assert.Equal(t, gcal.Result{Inserted: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1)
	assert.Contains(t, evs[0].Summary, "live")
}

func TestXReconcileProjectStepsOptionOffRemovesThem(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "P")
	s.step(t, p, projects.StepInput{Title: "s1", DueDate: day(2027, 8, 1)})
	s.step(t, p, projects.StepInput{Title: "s2", DueDate: day(2027, 8, 2)})
	m := item("Flush tank", day(2027, 8, 3))
	s.lister.set(m)
	assert.Equal(t, gcal.Result{Inserted: 3}, s.sync(t))

	off := false
	require.NoError(t, s.svc.UpdateSettings(context.Background(), s.gid, gcal.SettingsInput{IncludeProjectSteps: &off}))
	assert.Equal(t, gcal.Result{Deleted: 2, Unchanged: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1, "only the maintenance event is left")
	assert.Contains(t, evs[0].Summary, "Flush tank")

	// And back on.
	on := true
	require.NoError(t, s.svc.UpdateSettings(context.Background(), s.gid, gcal.SettingsInput{IncludeProjectSteps: &on}))
	assert.Equal(t, gcal.Result{Inserted: 2, Unchanged: 1}, s.sync(t))
}

func TestXReconcileProjectStepsOtherGroupIgnored(t *testing.T) {
	s := newStepEnv(t)
	other, err := s.repo.CreateProject(context.Background(), uuid.New(), projects.ProjectInput{Name: "theirs"})
	require.NoError(t, err)
	_, err = s.repo.AddStep(context.Background(), other.GroupID, other.ID, projects.StepInput{Title: "x", DueDate: day(2027, 9, 1)})
	require.NoError(t, err)

	assert.Equal(t, gcal.Result{}, s.sync(t))
	assert.Empty(t, s.events())
}

func TestXReconcileProjectStepLinksRebuiltWithoutDuplicates(t *testing.T) {
	s := newStepEnv(t)
	p := s.project(t, "P")
	s.step(t, p, projects.StepInput{Title: "s1", DueDate: day(2027, 10, 1)})
	assert.Equal(t, gcal.Result{Inserted: 1}, s.sync(t))

	_, err := s.db.SQL.Exec("DELETE FROM x_gcal_links")
	require.NoError(t, err)
	res := s.sync(t)
	assert.Equal(t, 1, res.Relinked)
	assert.Zero(t, res.Inserted)
	assert.Len(t, s.events(), 1)
}
