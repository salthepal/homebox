package main

import (
	"bytes"
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/graceful"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

type stepSyncEnv struct {
	gid  uuid.UUID
	fake *gcaltest.Fake
	svc  *projects.Service
	deps xapi.Deps
	api  *gcalAPI
}

// newStepSyncEnv uses the app's real wiring (registerCustomGcal): the step provider, the
// projects-service notifier and the reconciler queue, against a fake Google.
func newStepSyncEnv(t *testing.T) stepSyncEnv {
	t.Helper()
	a, token, gid, deps := newCustomTestApp(t)
	dialect := "sqlite3"
	if xtest.IsPostgres() {
		dialect = "postgres"
	}
	deps.Projects = newProjectsService(a, deps.Ent, a.db.Sql(), dialect)

	fake := gcaltest.New(t)
	xcfg := xconfig.Config{
		PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: "secret",
		SecretKey: bytes.Repeat([]byte{3}, 32), GcalSyncInterval: 15 * time.Minute,
	}
	deps.Config = xcfg
	deps.Gcal = fake.Options()
	deps.Gcal.Backoff = time.Millisecond
	prev := gcalReconcilerOptions
	gcalReconcilerOptions = gcal.ReconcilerOptions{Debounce: 40 * time.Millisecond}
	t.Cleanup(func() { gcalReconcilerOptions = prev })
	registerCustomGcal(a, xcfg, deps.Ent, graceful.NewRunner(), &deps)
	require.NotNil(t, deps.GcalReconciler)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = deps.GcalReconciler.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	svc := gcal.New(deps.Ent, xcfg, deps.Gcal)
	u, err := svc.ConnectURL(uuid.New(), gid)
	require.NoError(t, err)
	_, err = svc.HandleCallback(ctx, gcaltest.Code, mustQuery(t, u, "state"))
	require.NoError(t, err)
	cal := syncCal
	require.NoError(t, svc.UpdateSettings(ctx, gid, gcal.SettingsInput{CalendarID: &cal}))

	r := chi.NewRouter()
	mountCustomRoutes(a, r, errchain.New(mid.Errors(zerolog.Nop())), deps)
	return stepSyncEnv{
		gid: gid, fake: fake, svc: deps.Projects, deps: deps,
		api: &gcalAPI{t: t, r: r, token: token, fake: fake},
	}
}

func (s stepSyncEnv) events() []gcal.Event {
	evs := s.fake.EventsOn(syncCal)
	sort.Slice(evs, func(i, j int) bool { return evs[i].ID < evs[j].ID })
	return evs
}

func (s stepSyncEnv) waitEvents(t *testing.T, n int, msg string) []gcal.Event {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.events()) == n }, 10*time.Second, 20*time.Millisecond, msg)
	return s.events()
}

func (s stepSyncEnv) waitSummary(t *testing.T, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		e := s.events()
		return len(e) == 1 && e[0].Summary == want
	}, 10*time.Second, 20*time.Millisecond, want)
}

// Project and step writes made through the projects service reach the calendar via the
// notifier and the debounced queue, without anyone calling Reconcile.
func TestCustomGcalProjectStepWritesDriveSync(t *testing.T) {
	s := newStepSyncEnv(t)
	ctx := context.Background()
	due := time.Date(2027, 4, 2, 0, 0, 0, 0, time.UTC)

	p, err := s.svc.Create(ctx, s.gid, projects.ProjectInput{Name: "NAS upgrade"})
	require.NoError(t, err)
	step, err := s.svc.AddStep(ctx, s.gid, p.ID, projects.StepInput{Title: "Buy NIC", DueDate: &due})
	require.NoError(t, err)
	evs := s.waitEvents(t, 1, "a due step should reach the calendar")
	assert.Equal(t, "📋 NAS upgrade: Buy NIC", evs[0].Summary)
	assert.Equal(t, "2027-04-02", evs[0].Start.Date)
	assert.Contains(t, evs[0].Description, "/x/projects/"+p.ID.String())

	// Renaming the project patches the event title.
	_, err = s.svc.Update(ctx, s.gid, p.ID, projects.ProjectInput{Name: "NAS 10GbE"})
	require.NoError(t, err)
	s.waitSummary(t, "📋 NAS 10GbE: Buy NIC")

	// Completing turns it into a ✅.
	_, err = s.svc.CompleteStep(ctx, s.gid, p.ID, step.ID, projects.CompleteInput{})
	require.NoError(t, err)
	s.waitSummary(t, "✅ NAS 10GbE: Buy NIC")

	// Deleting the step removes the event.
	require.NoError(t, s.svc.DeleteStep(ctx, s.gid, p.ID, step.ID))
	s.waitEvents(t, 0, "a deleted step should leave the calendar")

	// A project delete takes its remaining steps off too.
	_, err = s.svc.AddStep(ctx, s.gid, p.ID, projects.StepInput{Title: "Install", DueDate: &due})
	require.NoError(t, err)
	s.waitEvents(t, 1, "second step should appear")
	require.NoError(t, s.svc.Delete(ctx, s.gid, p.ID))
	s.waitEvents(t, 0, "a deleted project should leave the calendar")
}

// Turning include_project_steps off in the settings removes the step events without a
// manual sync, and turning it on brings them back.
func TestCustomGcalSettingsToggleSyncsProjectSteps(t *testing.T) {
	s := newStepSyncEnv(t)
	ctx := context.Background()
	due := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
	p, err := s.svc.Create(ctx, s.gid, projects.ProjectInput{Name: "P"})
	require.NoError(t, err)
	_, err = s.svc.AddStep(ctx, s.gid, p.ID, projects.StepInput{Title: "s", DueDate: &due})
	require.NoError(t, err)
	s.waitEvents(t, 1, "step event")

	rec := s.api.do(http.MethodPut, "/api/v1/x/gcal/settings", map[string]any{"include_project_steps": false}, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	s.waitEvents(t, 0, "option off removes step events")

	rec = s.api.do(http.MethodPut, "/api/v1/x/gcal/settings", map[string]any{"include_project_steps": true}, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	s.waitEvents(t, 1, "option on restores step events")
}
