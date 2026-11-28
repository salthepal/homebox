package projects_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

// fakeAdapters stands in for the upstream items and maintenance adapters.
type fakeAdapters struct {
	mu      sync.Mutex
	created int32
	entries map[uuid.UUID]struct{}
	failNew error
}

func (f *fakeAdapters) Names(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]string, error) {
	return map[uuid.UUID]string{}, nil
}

func (f *fakeAdapters) ExistInGroup(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	out := map[uuid.UUID]struct{}{}
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

func (f *fakeAdapters) CreateCompleted(_ context.Context, _, _ uuid.UUID, _ upstream.MaintenanceInput) (uuid.UUID, error) {
	if f.failNew != nil {
		return uuid.Nil, f.failNew
	}
	atomic.AddInt32(&f.created, 1)
	id := uuid.New()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entries == nil {
		f.entries = map[uuid.UUID]struct{}{}
	}
	f.entries[id] = struct{}{}
	return id, nil
}

func (f *fakeAdapters) Delete(_ context.Context, _, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, id)
	return nil
}

type maintAdapter struct{ *fakeAdapters }

func (m maintAdapter) ExistInGroup(ctx context.Context, gid uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	return m.fakeAdapters.ExistInGroup(ctx, gid, ids)
}

func newSvc(t *testing.T) (*projects.Service, *projects.Repo, *fakeAdapters) {
	t.Helper()
	r, _ := newRepo(t)
	f := &fakeAdapters{}
	return projects.NewService(r, f, maintAdapter{f}), r, f
}

func loggedStep(t *testing.T, s *projects.Service, gid uuid.UUID) (projects.Project, projects.Step) {
	t.Helper()
	ctx := context.Background()
	p, err := s.Create(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	eid := uuid.New()
	st, err := s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "S", EntityID: &eid, LogAsMaintenance: true})
	require.NoError(t, err)
	return p.Project, st
}

func TestXProjectsCompleteStepOnce(t *testing.T) {
	ctx := context.Background()
	s, r, f := newSvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)

	// Sequential: the second complete is a conflict and creates no second entry.
	done, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.NoError(t, err)
	require.NotNil(t, done.MaintenanceEntryID)
	_, err = s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.ErrorIs(t, err, projects.ErrConflict)
	assert.EqualValues(t, 1, atomic.LoadInt32(&f.created))

	// The claim itself is exclusive even when the caller skipped the pre-check.
	_, err = r.ClaimStep(ctx, gid, p.ID, st.ID, projects.StepCompletion{})
	require.ErrorIs(t, err, projects.ErrConflict)
}

func TestXProjectsCompleteStepConcurrent(t *testing.T) {
	ctx := context.Background()
	s, _, f := newSvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)

	const n = 8
	var wg sync.WaitGroup
	var ok, conflict int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, projects.ErrConflict):
				atomic.AddInt32(&conflict, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, ok)
	assert.EqualValues(t, n-1, conflict)
	assert.EqualValues(t, 1, atomic.LoadInt32(&f.created), "exactly one maintenance entry")
	assert.Len(t, f.entries, 1)
}

func TestXProjectsCompleteStepRevertsClaim(t *testing.T) {
	ctx := context.Background()
	s, r, f := newSvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)

	f.failNew = upstream.ErrEntityNotFound
	_, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{ActualCost: fp(5)})
	require.ErrorIs(t, err, projects.ErrInvalid)
	got, err := r.GetStep(ctx, gid, p.ID, st.ID)
	require.NoError(t, err)
	assert.Equal(t, projects.StepTodo, got.Status)
	assert.Nil(t, got.CompletedAt)
	assert.Nil(t, got.ActualCost)
	assert.Nil(t, got.MaintenanceEntryID)

	// A retry once the problem is gone works and logs once.
	f.failNew = nil
	_, err = s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(&f.created))
}

func TestXProjectsStepDoneOnlyViaComplete(t *testing.T) {
	ctx := context.Background()
	s, _, f := newSvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)

	_, err := s.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S", Status: projects.StepDone, EntityID: st.EntityID, LogAsMaintenance: true})
	require.ErrorIs(t, err, projects.ErrInvalid)
	assert.Contains(t, err.Error(), "/complete")
	_, err = s.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "born done", Status: projects.StepDone})
	require.ErrorIs(t, err, projects.ErrInvalid)

	_, err = s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{})
	require.NoError(t, err)
	// Editing a done step (still done) and leaving done both work.
	_, err = s.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S2", Status: projects.StepDone})
	require.NoError(t, err)
	back, err := s.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S2", Status: projects.StepTodo})
	require.NoError(t, err)
	assert.Equal(t, projects.StepTodo, back.Status)
	assert.Nil(t, back.CompletedAt)
	assert.EqualValues(t, 1, atomic.LoadInt32(&f.created))
}

func TestXProjectsStartProjectIsNarrow(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "Old", Priority: 1, Budget: fp(10)})
	require.NoError(t, err)

	// Another request edits the project after the caller read it.
	_, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "Renamed", Description: "new", Priority: 3, Budget: fp(99)})
	require.NoError(t, err)

	started, err := r.StartProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.True(t, started)
	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.Equal(t, projects.StatusInProgress, got.Status)
	assert.NotNil(t, got.StartedAt)
	assert.Equal(t, "Renamed", got.Name)
	assert.Equal(t, "new", got.Description)
	assert.Equal(t, 3, got.Priority)
	assert.InDelta(t, 99, *got.Budget, 0.001)

	// Only idea/planning move; others (and other groups) are untouched.
	again, err := r.StartProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.False(t, again)
	hold, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "H", Status: projects.StatusOnHold})
	require.NoError(t, err)
	moved, err := r.StartProject(ctx, gid, hold.ID)
	require.NoError(t, err)
	assert.False(t, moved)
	other, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "O"})
	require.NoError(t, err)
	moved, err = r.StartProject(ctx, uuid.New(), other.ID)
	require.NoError(t, err)
	assert.False(t, moved)
}

func TestXProjectsOnlyTransitionIntoDoingStarts(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	status := func() string {
		got, err := r.GetProject(ctx, gid, p.ID)
		require.NoError(t, err)
		return got.Status
	}

	st, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "S", Status: projects.StepDoing})
	require.NoError(t, err)
	assert.Equal(t, projects.StatusInProgress, status(), "step created doing starts the project")

	// Project goes back to planning with the step still doing: an unrelated edit of the step
	// must not force it to in_progress again.
	_, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "P", Status: projects.StatusPlanning})
	require.NoError(t, err)
	_, err = r.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S renamed", Status: projects.StepDoing})
	require.NoError(t, err)
	assert.Equal(t, projects.StatusPlanning, status())

	// Leaving and re-entering doing does start it, and started_at is kept from the first start.
	first, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	_, err = r.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S", Status: projects.StepTodo})
	require.NoError(t, err)
	_, err = r.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "S", Status: projects.StepDoing})
	require.NoError(t, err)
	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.Equal(t, projects.StatusInProgress, got.Status)
	require.NotNil(t, got.StartedAt)
	assert.True(t, first.StartedAt.Equal(*got.StartedAt))
}

func TestXProjectsAddStepAndStartAreAtomic(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	client := xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))
	r := projects.NewRepo(client, d.SQL, d.Dialect)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)

	// Make the project transition fail after the step insert.
	client.XProject.Use(func(next xent.Mutator) xent.Mutator {
		return xent.MutateFunc(func(ctx context.Context, m xent.Mutation) (xent.Value, error) {
			if m.Op().Is(xent.OpUpdate) {
				return nil, errors.New("injected failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "S", Status: projects.StepDoing})
	require.Error(t, err)

	got, err := r.GetProject(ctx, gid, p.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Steps, "the step insert rolled back with the failed transition")
	assert.Equal(t, projects.StatusIdea, got.Status)
}

func TestXProjectsValidateText(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()

	ok255 := strings.Repeat("a", 255)
	long256 := strings.Repeat("a", 256)
	ok127 := strings.Repeat("é", 127)   // 254 bytes
	long128 := strings.Repeat("é", 128) // 256 bytes: over ent's byte limit
	cjk := strings.Repeat("家", 85)      // 255 bytes exactly
	cjk86 := strings.Repeat("家", 86)    // 258 bytes

	for name, tc := range map[string]struct {
		v  string
		ok bool
	}{"255 ascii": {ok255, true}, "256 ascii": {long256, false}, "127 two-byte": {ok127, true},
		"128 two-byte": {long128, false}, "85 three-byte": {cjk, true}, "86 three-byte": {cjk86, false},
		"padded to 256 then trimmed": {"  " + ok255 + "  ", true}} {
		t.Run(name, func(t *testing.T) {
			p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: tc.v})
			if tc.ok {
				require.NoError(t, err)
				_, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: tc.v})
				require.NoError(t, err)
				_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: tc.v})
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, projects.ErrInvalid)
			ok, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "ok"})
			require.NoError(t, err)
			_, err = r.UpdateProject(ctx, gid, ok.ID, projects.ProjectInput{Name: tc.v})
			require.ErrorIs(t, err, projects.ErrInvalid)
			_, err = r.AddStep(ctx, gid, ok.ID, projects.StepInput{Title: tc.v})
			require.ErrorIs(t, err, projects.ErrInvalid)
			s, err := r.AddStep(ctx, gid, ok.ID, projects.StepInput{Title: "t"})
			require.NoError(t, err)
			_, err = r.UpdateStep(ctx, gid, ok.ID, s.ID, projects.StepInput{Title: tc.v})
			require.ErrorIs(t, err, projects.ErrInvalid)
		})
	}
}

func TestXProjectsValidateMoney(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	gid := uuid.New()
	p, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)

	cases := []struct {
		name string
		v    float64
		ok   bool
	}{
		{"80", 80, true}, {"80.5", 80.5, true}, {"80.55", 80.55, true}, {"zero", 0, true},
		{"float noise 0.1+0.2", 0.1 + 0.2, true}, {"max", 999999999999.99, true},
		{"80.555", 80.555, false}, {"80.001", 80.001, false}, {"negative", -1, false},
		{"1e12", 1e12, false}, {"1e15", 1e15, false},
		{"NaN", math.NaN(), false}, {"+Inf", math.Inf(1), false}, {"-Inf", math.Inf(-1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.v
			check := func(err error) {
				if tc.ok {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, projects.ErrInvalid)
				}
			}
			_, err := r.CreateProject(ctx, gid, projects.ProjectInput{Name: "B", Budget: &v})
			check(err)
			_, err = r.UpdateProject(ctx, gid, p.ID, projects.ProjectInput{Name: "P", Budget: &v})
			check(err)
			_, err = r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "E", EstCost: &v})
			check(err)
			st, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "A"})
			require.NoError(t, err)
			_, err = r.UpdateStep(ctx, gid, p.ID, st.ID, projects.StepInput{Title: "A", ActualCost: &v})
			check(err)
			check(projects.ValidateMoney("actual_cost", &v))
		})
	}

	// Stored values are normalised to exactly two decimals.
	n := 0.1 + 0.2
	st, err := r.AddStep(ctx, gid, p.ID, projects.StepInput{Title: "N", EstCost: &n})
	require.NoError(t, err)
	assert.InDelta(t, 0.3, *st.EstCost, 1e-12)
}

func TestXProjectsServiceRejectsBadCompleteCost(t *testing.T) {
	ctx := context.Background()
	s, r, _ := newSvc(t)
	gid := uuid.New()
	p, st := loggedStep(t, s, gid)
	for _, v := range []float64{80.555, -1, 1e12, math.NaN(), math.Inf(1)} {
		_, err := s.CompleteStep(ctx, gid, p.ID, st.ID, projects.CompleteInput{ActualCost: &v})
		require.ErrorIs(t, err, projects.ErrInvalid, "%v", v)
	}
	got, err := r.GetStep(ctx, gid, p.ID, st.ID)
	require.NoError(t, err)
	assert.Equal(t, projects.StepTodo, got.Status)
}
