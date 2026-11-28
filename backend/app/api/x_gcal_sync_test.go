package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

const syncCal = "household@group.calendar.google.com"

type syncEnv struct {
	a     *app
	token string
	gid   uuid.UUID
	fake  *gcaltest.Fake
	rec   *gcal.Reconciler
	r     chi.Router
}

// newSyncEnv wires the real thing: upstream tables, the upstream maintenance reader, the ent
// hook, the reconciler queue goroutine and the HTTP routes, against a fake Google.
func newSyncEnv(t *testing.T) syncEnv {
	t.Helper()
	return newSyncEnvWith(t, 40*time.Millisecond, nil)
}

// newSyncEnvWith is newSyncEnv with a custom debounce (an hour keeps the background queue
// out of a test that drives Sync now by hand) and a hook to adjust the Google options.
func newSyncEnvWith(t *testing.T, debounce time.Duration, tweak func(*gcal.Options)) syncEnv {
	t.Helper()
	a, token, gid, deps := newCustomTestApp(t)
	fake := gcaltest.New(t)
	xcfg := xconfig.Config{
		PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: gcalTestSecret,
		SecretKey: bytes.Repeat([]byte{3}, 32), GcalSyncInterval: 15 * time.Minute,
	}
	opts := fake.Options()
	opts.Backoff = time.Millisecond
	if tweak != nil {
		tweak(&opts)
	}

	rec := newGcalReconciler(a, xcfg, deps.Ent, opts, gcal.ReconcilerOptions{Debounce: debounce})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = rec.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Connect through the real flow and pick the shared calendar.
	svc := gcal.New(deps.Ent, xcfg, opts)
	u, err := svc.ConnectURL(uuid.New(), gid)
	require.NoError(t, err)
	_, err = svc.HandleCallback(ctx, gcaltest.Code, mustQuery(t, u, "state"))
	require.NoError(t, err)
	cal := syncCal
	require.NoError(t, svc.UpdateSettings(ctx, gid, gcal.SettingsInput{CalendarID: &cal}))

	deps.Config, deps.Gcal, deps.GcalReconciler = xcfg, opts, rec
	r := chi.NewRouter()
	mountCustomRoutes(a, r, errchain.New(mid.Errors(zerolog.Nop())), deps)
	return syncEnv{a: a, token: token, gid: gid, fake: fake, rec: rec, r: r}
}

func mustQuery(t *testing.T, rawURL, key string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	v := req.URL.Query().Get(key)
	require.NotEmpty(t, v)
	return v
}

func (s syncEnv) events() []gcal.Event {
	evs := s.fake.EventsOn(syncCal)
	sort.Slice(evs, func(i, j int) bool { return evs[i].ID < evs[j].ID })
	return evs
}

func (s syncEnv) waitEvents(t *testing.T, n int, msg string) []gcal.Event {
	t.Helper()
	require.Eventually(t, func() bool { return len(s.events()) == n }, 10*time.Second, 20*time.Millisecond, msg)
	return s.events()
}

func (s syncEnv) newItem(t *testing.T, name string) uuid.UUID {
	t.Helper()
	it, err := s.a.repos.Entities.Create(context.Background(), s.gid, repo.EntityCreate{Name: name, Quantity: 1})
	require.NoError(t, err)
	return it.ID
}

// Maintenance changes made through upstream's own repo reach the calendar via the ent hook
// and the debounced queue, without anyone calling Reconcile.
func TestCustomGcalHookDrivesSync(t *testing.T) {
	s := newSyncEnv(t)
	ctx := context.Background()
	itemID := s.newItem(t, "Water heater")

	// create -> inserted
	sched := types.DateFromTime(time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC))
	entry, err := s.a.repos.MaintEntry.Create(ctx, s.gid, itemID, repo.MaintenanceEntryCreate{
		Name: "Flush tank", Description: "annual", ScheduledDate: sched, Cost: 40,
	})
	require.NoError(t, err)
	evs := s.waitEvents(t, 1, "created entry should reach the calendar")
	assert.Equal(t, "🔧 Water heater: Flush tank", evs[0].Summary)
	assert.Equal(t, "2027-03-14", evs[0].Start.Date)
	assert.Contains(t, evs[0].Description, "/item/"+itemID.String()+"/maintenance")

	// edit -> patched in place
	_, err = s.a.repos.MaintEntry.Update(ctx, s.gid, entry.ID, repo.MaintenanceEntryUpdate{
		Name: "Flush and descale", Description: "annual", ScheduledDate: sched, Cost: 40,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		e := s.events()
		return len(e) == 1 && e[0].Summary == "🔧 Water heater: Flush and descale"
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, evs[0].ID, s.events()[0].ID)

	// complete -> ✅ on the completion date, no reminder
	done := types.DateFromTime(time.Date(2027, 3, 16, 0, 0, 0, 0, time.UTC))
	_, err = s.a.repos.MaintEntry.Update(ctx, s.gid, entry.ID, repo.MaintenanceEntryUpdate{
		Name: "Flush and descale", Description: "annual", ScheduledDate: sched, CompletedDate: done, Cost: 55,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		e := s.events()
		return len(e) == 1 && e[0].Summary == "✅ Water heater: Flush and descale"
	}, 10*time.Second, 20*time.Millisecond)
	got := s.events()[0]
	assert.Equal(t, "2027-03-16", got.Start.Date)
	assert.Empty(t, got.Reminders.Overrides)

	// delete -> event deleted
	require.NoError(t, s.a.repos.MaintEntry.Delete(ctx, s.gid, entry.ID))
	s.waitEvents(t, 0, "deleted entry should leave the calendar")
}

// Deleting an item cascades in the database and bypasses ent hooks; the periodic full sync
// is what removes its events.
func TestCustomGcalItemDeleteCaughtByPeriodicSync(t *testing.T) {
	s := newSyncEnv(t)
	ctx := context.Background()
	itemID := s.newItem(t, "Dishwasher")
	_, err := s.a.repos.MaintEntry.Create(ctx, s.gid, itemID, repo.MaintenanceEntryCreate{
		Name: "Clean filter", ScheduledDate: types.DateFromTime(time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	s.waitEvents(t, 1, "entry reaches the calendar")

	require.NoError(t, s.a.repos.Entities.Delete(ctx, itemID))
	s.rec.ReconcileAll(ctx)
	s.waitEvents(t, 0, "the periodic sync removes events of deleted items")
}

// Another group's maintenance changes never touch this group's calendar, and a group with no
// connection is simply ignored by the hook.
func TestCustomGcalHookIgnoresUnconnectedGroups(t *testing.T) {
	s := newSyncEnv(t)
	ctx := context.Background()

	other, err := s.a.repos.Groups.GroupCreate(ctx, "other", uuid.Nil)
	require.NoError(t, err)
	it, err := s.a.repos.Entities.Create(ctx, other.ID, repo.EntityCreate{Name: "Foreign item", Quantity: 1})
	require.NoError(t, err)
	_, err = s.a.repos.MaintEntry.Create(ctx, other.ID, it.ID, repo.MaintenanceEntryCreate{
		Name: "x", ScheduledDate: types.DateFromTime(time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)
	assert.Empty(t, s.events())
}

func TestCustomGcalSyncNowEndpoint(t *testing.T) {
	s := newSyncEnv(t)
	ctx := context.Background()
	itemID := s.newItem(t, "Furnace")
	_, err := s.a.repos.MaintEntry.Create(ctx, s.gid, itemID, repo.MaintenanceEntryCreate{
		Name: "Replace filter", ScheduledDate: types.DateFromTime(time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)

	g := &gcalAPI{t: t, r: s.r, token: s.token, fake: s.fake}
	assert.Equal(t, http.StatusUnauthorized, g.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, false).Code)

	rec := g.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeBody[struct {
		Result gcal.Result `json:"result"`
		Status gcal.Status `json:"status"`
	}](t, rec)
	// The debounced hook may have synced first; either way the event exists exactly once.
	assert.Equal(t, 1, body.Result.Inserted+body.Result.Unchanged)
	assert.NotNil(t, body.Status.LastSyncAt)
	assert.Empty(t, body.Status.LastError)
	assert.Len(t, s.events(), 1)
}

func TestCustomGcalSyncNowErrors(t *testing.T) {
	// Not connected.
	g := newGcalAPI(t, true)
	g.r = chiWithReconciler(t, g)
	assert.Equal(t, http.StatusNotFound, g.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, true).Code)

	// No reconciler wired (e.g. an older embedding): a clear 409, not a panic.
	g2 := newGcalAPI(t, true)
	assert.Equal(t, http.StatusConflict, g2.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, true).Code)
}

// chiWithReconciler rebuilds g's router with a reconciler over an empty source.
func chiWithReconciler(t *testing.T, g *gcalAPI) chi.Router {
	t.Helper()
	a, token, _, deps := newCustomTestApp(t)
	g.token = token
	xcfg := xconfig.Config{
		PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: gcalTestSecret,
		SecretKey: bytes.Repeat([]byte{3}, 32), GcalSyncInterval: time.Minute,
	}
	deps.Config, deps.Gcal = xcfg, g.fake.Options()
	deps.GcalReconciler = newGcalReconciler(a, xcfg, deps.Ent, deps.Gcal, gcal.ReconcilerOptions{})
	r := chi.NewRouter()
	mountCustomRoutes(a, r, errchain.New(mid.Errors(zerolog.Nop())), deps)
	return r
}

// failFirstInsert answers the first events.insert with a 400, like Google rejecting one
// bad entry, and passes everything else through.
type failFirstInsert struct {
	base  http.RoundTripper
	fired atomic.Bool
}

func (f *failFirstInsert) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/events") && f.fired.CompareAndSwap(false, true) {
		return &http.Response{
			StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Request: req,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"code":400,"message":"bad event","errors":[{"reason":"invalid"}]}}`)),
		}, nil
	}
	return f.base.RoundTrip(req)
}

// When some entries sync and one fails, Sync now still reports what was done: the error status
// carries the result, the refreshed status and the message.
func TestCustomGcalSyncNowReportsPartialResult(t *testing.T) {
	tr := &failFirstInsert{base: http.DefaultTransport}
	s := newSyncEnvWith(t, time.Hour, func(o *gcal.Options) { o.HTTPClient = &http.Client{Transport: tr} })
	ctx := context.Background()
	for _, name := range []string{"Furnace", "Boiler"} {
		itemID := s.newItem(t, name)
		_, err := s.a.repos.MaintEntry.Create(ctx, s.gid, itemID, repo.MaintenanceEntryCreate{
			Name: "Replace filter", ScheduledDate: types.DateFromTime(time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)),
		})
		require.NoError(t, err)
	}

	g := &gcalAPI{t: t, r: s.r, token: s.token, fake: s.fake}
	rec := g.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, true)
	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	body := decodeBody[struct {
		Result gcal.Result `json:"result"`
		Status gcal.Status `json:"status"`
		Error  string      `json:"error"`
	}](t, rec)
	assert.Equal(t, 1, body.Result.Inserted, "the entry that did sync is reported")
	assert.Contains(t, body.Error, "bad event")
	assert.Contains(t, body.Status.LastError, "bad event")
	assert.NotEmpty(t, s.events(), "and it is really on the calendar")
	assert.Len(t, s.events(), 1)

	// A second press retries what failed and succeeds.
	rec = g.do(http.MethodPost, "/api/v1/x/gcal/sync", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	ok := decodeBody[struct {
		Result gcal.Result `json:"result"`
		Error  string      `json:"error"`
	}](t, rec)
	assert.Equal(t, 1, ok.Result.Inserted)
	assert.Empty(t, ok.Error)
	assert.Len(t, s.events(), 2)
}
