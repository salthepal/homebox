package projects_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

type recNotifier struct {
	mu   sync.Mutex
	gids []uuid.UUID
}

func (n *recNotifier) Enqueue(gid uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gids = append(n.gids, gid)
}

func (n *recNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.gids)
}

func (n *recNotifier) reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gids = nil
}

func notifySvc(t *testing.T) (*projects.Service, *recNotifier, *fakeAdapters) {
	t.Helper()
	s, _, f := newSvc(t)
	n := &recNotifier{}
	s.SetNotifier(n)
	return s, n, f
}

func TestXProjectsNotifyOnEveryWrite(t *testing.T) {
	ctx := context.Background()
	s, n, _ := notifySvc(t)
	gid := uuid.New()

	p, err := s.Create(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	assert.Equal(t, 1, n.count(), "create")

	st, err := s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "a"})
	require.NoError(t, err)
	st2, err := s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "b"})
	require.NoError(t, err)
	assert.Equal(t, 3, n.count(), "add step x2")

	_, err = s.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "a2"})
	require.NoError(t, err)
	_, err = s.ReorderSteps(ctx, gid, p.ID, []uuid.UUID{st2.ID, st.ID})
	require.NoError(t, err)
	_, err = s.Update(ctx, gid, p.ID, projects.ProjectInput{Name: "P2"})
	require.NoError(t, err)
	_, err = s.SetEntities(ctx, gid, p.ID, []uuid.UUID{uuid.New()})
	require.NoError(t, err)
	require.NoError(t, s.DeleteStep(ctx, gid, p.ID, st.ID))
	assert.Equal(t, 8, n.count(), "update step, reorder, update, set entities, delete step")

	require.NoError(t, s.Delete(ctx, gid, p.ID))
	assert.Equal(t, 9, n.count(), "delete project")
	for _, g := range n.gids {
		assert.Equal(t, gid, g, "always the writing group")
	}
}

func TestXProjectsNoNotifyOnFailedWrites(t *testing.T) {
	ctx := context.Background()
	s, n, _ := notifySvc(t)
	gid, other := uuid.New(), uuid.New()
	p, err := s.Create(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	st, err := s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "a"})
	require.NoError(t, err)
	n.reset()

	_, err = s.Create(ctx, gid, projects.ProjectInput{Name: " "})
	require.ErrorIs(t, err, projects.ErrInvalid)
	_, err = s.Update(ctx, other, p.ID, projects.ProjectInput{Name: "x"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, other, p.ID), projects.ErrNotFound)
	_, err = s.AddStep(ctx, other, p.ID, projects.StepInput{Title: "x"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	_, err = s.UpdateStep(ctx, other, p.ID, st.ID, projects.StepInput{Title: "x"})
	require.ErrorIs(t, err, projects.ErrNotFound)
	require.ErrorIs(t, s.DeleteStep(ctx, other, p.ID, st.ID), projects.ErrNotFound)
	_, err = s.ReorderSteps(ctx, gid, p.ID, []uuid.UUID{uuid.New()})
	require.ErrorIs(t, err, projects.ErrInvalid)
	_, err = s.CompleteStep(ctx, other, p.ID, st.ID, projects.CompleteInput{})
	require.ErrorIs(t, err, projects.ErrNotFound)

	assert.Zero(t, n.count())
}

// Completing without logging maintenance is just the claim; the calendar hears about it
// right then.
func TestXProjectsCompleteWithoutLoggingNotifies(t *testing.T) {
	ctx := context.Background()
	s, n, f := notifySvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid) // step has log_as_maintenance on; override per call
	n.reset()

	off := false
	done, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{LogAsMaintenance: &off})
	require.NoError(t, err)
	assert.Equal(t, projects.StepDone, done.Status)
	assert.Nil(t, done.MaintenanceEntryID)
	assert.Equal(t, 1, n.count())
	assert.EqualValues(t, 0, f.created)

	// Already done: a conflict, no second notification.
	_, err = s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{LogAsMaintenance: &off})
	require.ErrorIs(t, err, projects.ErrConflict)
	assert.Equal(t, 1, n.count())
}

// With logging, the notification waits until the maintenance entry is recorded: exactly one,
// and none when creating the entry fails and the claim is reverted.
func TestXProjectsCompleteWithLoggingNotifiesOnceAndNotOnRevert(t *testing.T) {
	ctx := context.Background()
	s, n, f := notifySvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)
	n.reset()

	f.failNew = upstream.ErrEntityNotFound
	_, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.ErrorIs(t, err, projects.ErrInvalid)
	assert.Zero(t, n.count(), "a reverted claim must not notify")

	f.failNew = nil
	done, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.NoError(t, err)
	require.NotNil(t, done.MaintenanceEntryID)
	assert.Equal(t, 1, n.count(), "one notification, after the entry is recorded")
}

func TestXProjectsNilNotifierIsFine(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSvc(t) // no SetNotifier
	gid := uuid.New()
	p, err := s.Create(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	st, err := s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "a"})
	require.NoError(t, err)
	off := false
	_, err = s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{LogAsMaintenance: &off})
	require.NoError(t, err)
}
