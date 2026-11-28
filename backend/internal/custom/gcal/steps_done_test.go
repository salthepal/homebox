package gcal_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
)

func TestXReconcileProjectStepDoneFollowsIncludeCompleted(t *testing.T) {
	s := newStepEnv(t)
	ctx := context.Background()
	p := s.project(t, "P")
	open := s.step(t, p, projects.StepInput{Title: "open", DueDate: day(2027, 11, 1)})
	done := s.step(t, p, projects.StepInput{Title: "done", DueDate: day(2027, 11, 2)})
	_, err := s.repo.CompleteStep(ctx, s.gid, p.ID, done.ID, projects.StepCompletion{})
	require.NoError(t, err)
	assert.Equal(t, gcal.Result{Inserted: 2}, s.sync(t))

	off := false
	require.NoError(t, s.svc.UpdateSettings(ctx, s.gid, gcal.SettingsInput{IncludeCompleted: &off}))
	assert.Equal(t, gcal.Result{Deleted: 1, Unchanged: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1, "open steps stay, done steps go")
	assert.Equal(t, open.ID.String(), evs[0].ExtendedProperties.Private[gcal.IDProperty])

	on := true
	require.NoError(t, s.svc.UpdateSettings(ctx, s.gid, gcal.SettingsInput{IncludeCompleted: &on}))
	assert.Equal(t, gcal.Result{Inserted: 1, Unchanged: 1}, s.sync(t))
}

// A step that logged a maintenance entry is already on the calendar through that entry's
// ✅ event; its own ✅ would be a duplicate. Steps completed without logging keep theirs.
func TestXReconcileProjectStepSkipsDoneStepWithLoggedMaintenance(t *testing.T) {
	s := newStepEnv(t)
	ctx := context.Background()
	p := s.project(t, "P")
	logged := s.step(t, p, projects.StepInput{Title: "logged", DueDate: day(2027, 12, 1)})
	plain := s.step(t, p, projects.StepInput{Title: "plain", DueDate: day(2027, 12, 2)})
	assert.Equal(t, gcal.Result{Inserted: 2}, s.sync(t))

	mid := uuid.New()
	_, err := s.repo.CompleteStep(ctx, s.gid, p.ID, logged.ID,
		projects.StepCompletion{LogAsMaintenance: true, MaintenanceEntryID: &mid})
	require.NoError(t, err)
	_, err = s.repo.CompleteStep(ctx, s.gid, p.ID, plain.ID, projects.StepCompletion{})
	require.NoError(t, err)

	// The logged step's open 📋 event goes; the plain one turns ✅.
	assert.Equal(t, gcal.Result{Deleted: 1, Patched: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1)
	assert.Equal(t, "✅ P: plain", evs[0].Summary)
	assert.Equal(t, plain.ID.String(), evs[0].ExtendedProperties.Private[gcal.IDProperty])
}

func TestXReconcileProjectStepsOfCancelledProjectKeepDoneOnly(t *testing.T) {
	s := newStepEnv(t)
	ctx := context.Background()
	p := s.project(t, "Cancelled one")
	done := s.step(t, p, projects.StepInput{Title: "was done", DueDate: day(2028, 1, 1)})
	s.step(t, p, projects.StepInput{Title: "never happened", DueDate: day(2028, 1, 2)})
	assert.Equal(t, gcal.Result{Inserted: 2}, s.sync(t))
	_, err := s.repo.CompleteStep(ctx, s.gid, p.ID, done.ID, projects.StepCompletion{})
	require.NoError(t, err)

	_, err = s.repo.UpdateProject(ctx, s.gid, p.ID,
		projects.ProjectInput{Name: "Cancelled one", Status: projects.StatusCancelled})
	require.NoError(t, err)
	assert.Equal(t, gcal.Result{Patched: 1, Deleted: 1}, s.sync(t))
	evs := s.events()
	require.Len(t, evs, 1, "history stays, the not-done step is dropped")
	assert.Equal(t, "✅ Cancelled one: was done", evs[0].Summary)

	// The kept ✅ still follows include_completed.
	off := false
	require.NoError(t, s.svc.UpdateSettings(ctx, s.gid, gcal.SettingsInput{IncludeCompleted: &off}))
	assert.Equal(t, gcal.Result{Deleted: 1}, s.sync(t))
	assert.Empty(t, s.events())
}
