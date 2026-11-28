package gcal_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

type gcaltestFake = gcaltest.Fake

const householdCal = "household@group.calendar.google.com"

// fakeLister is an in-memory stand-in for upstream maintenance entries.
type fakeLister struct {
	mu    sync.Mutex
	items []upstream.MaintenanceItem
	calls atomic.Int32
}

func (f *fakeLister) ListForGroup(_ context.Context, _ uuid.UUID) ([]upstream.MaintenanceItem, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstream.MaintenanceItem(nil), f.items...), nil
}

func (f *fakeLister) set(items ...upstream.MaintenanceItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = items
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func item(name string, sched *time.Time) upstream.MaintenanceItem {
	return upstream.MaintenanceItem{
		ID: uuid.New(), EntityID: uuid.New(), EntityName: "Water heater",
		Name: name, Description: "flush the tank", Cost: 40, ScheduledDate: sched,
	}
}

type recEnv struct {
	env
	lister *fakeLister
	rec    *gcal.Reconciler
	svc    *gcal.Service
}

// newRecEnv connects the group, picks the household calendar and builds a reconciler.
// opt customizes the service options (HTTP client, sleep).
func newRecEnv(t *testing.T, tweak func(*gcal.Options)) recEnv {
	t.Helper()
	e := newEnv(t)
	e.connect(t)
	opt := e.fake.Options()
	opt.Backoff = time.Millisecond
	opt.Sleep = func(context.Context, time.Duration) error { return nil }
	if tweak != nil {
		tweak(&opt)
	}
	svc := gcal.New(e.ent, testConfig(), opt)
	cal := householdCal
	require.NoError(t, svc.UpdateSettings(context.Background(), e.gid, gcal.SettingsInput{CalendarID: &cal}))

	l := &fakeLister{}
	rec := gcal.NewReconciler(svc, gcal.ReconcilerOptions{Debounce: 30 * time.Millisecond},
		gcal.MaintenanceProvider{Source: l, BaseURL: "https://homebox.example.org"})
	return recEnv{env: e, lister: l, rec: rec, svc: svc}
}

func (r recEnv) sync(t *testing.T) gcal.Result {
	t.Helper()
	res, err := r.rec.Reconcile(context.Background(), r.gid)
	require.NoError(t, err)
	return res
}

func (r recEnv) events() []gcal.Event {
	evs := r.fake.EventsOn(householdCal)
	sort.Slice(evs, func(i, j int) bool { return evs[i].ID < evs[j].ID })
	return evs
}

func TestXReconcileCreateInsertsEvent(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)

	res := r.sync(t)
	assert.Equal(t, gcal.Result{Inserted: 1}, res)

	evs := r.events()
	require.Len(t, evs, 1)
	ev := evs[0]
	assert.Equal(t, "🔧 Water heater: Flush tank", ev.Summary)
	assert.Equal(t, "2027-03-14", ev.Start.Date)
	assert.Equal(t, "2027-03-15", ev.End.Date, "all-day end date is exclusive")
	assert.Contains(t, ev.Description, "flush the tank")
	assert.Contains(t, ev.Description, "Estimated cost: $40.00")
	assert.Contains(t, ev.Description, "https://homebox.example.org/item/"+m.EntityID.String()+"/maintenance")
	require.NotNil(t, ev.Reminders)
	assert.False(t, ev.Reminders.UseDefault)
	assert.Equal(t, []gcal.ReminderOverride{{Method: "popup", Minutes: 1440}}, ev.Reminders.Overrides)
	assert.True(t, ev.IsOurs())
	assert.Equal(t, m.ID.String(), ev.ExtendedProperties.Private[gcal.IDProperty])

	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, ev.ID, links[0].EventID)

	// A second run with nothing changed does no writes.
	r.fake.Set(func(f *gcaltestFake) { f.Requests = nil })
	assert.Equal(t, gcal.Result{Unchanged: 1}, r.sync(t))
	r.fake.Set(func(f *gcaltestFake) {
		for _, req := range f.Requests {
			assert.Equal(t, "GET", req[:3], "idle sync only reads: %s", req)
		}
	})

	conn, err := r.svc.Store().GetConnection(context.Background(), r.gid)
	require.NoError(t, err)
	assert.NotNil(t, conn.LastSyncAt)
	assert.Empty(t, conn.LastError)
}

func TestXReconcileEditPatchesEvent(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	id := r.events()[0].ID

	m.Name = "Flush and descale"
	m.ScheduledDate = day(2027, 4, 2)
	r.lister.set(m)
	assert.Equal(t, gcal.Result{Patched: 1}, r.sync(t))

	evs := r.events()
	require.Len(t, evs, 1)
	assert.Equal(t, id, evs[0].ID, "patched in place, not re-created")
	assert.Equal(t, "🔧 Water heater: Flush and descale", evs[0].Summary)
	assert.Equal(t, "2027-04-02", evs[0].Start.Date)
}

func TestXReconcileCompleteMarksDoneWithoutReminder(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	id := r.events()[0].ID

	m.CompletedDate = day(2027, 3, 16)
	m.Cost = 55
	r.lister.set(m)
	assert.Equal(t, gcal.Result{Patched: 1}, r.sync(t))

	evs := r.events()
	require.Len(t, evs, 1)
	assert.Equal(t, id, evs[0].ID)
	assert.Equal(t, "✅ Water heater: Flush tank", evs[0].Summary)
	assert.Equal(t, "2027-03-16", evs[0].Start.Date, "on the completion date, not the scheduled one")
	require.NotNil(t, evs[0].Reminders)
	assert.False(t, evs[0].Reminders.UseDefault)
	assert.Empty(t, evs[0].Reminders.Overrides, "no reminder for a completed task")
	assert.Contains(t, evs[0].Description, "Cost: $55.00")
}

func TestXReconcileCompletedWithoutScheduleAndIncludeCompleted(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item("Replaced anode", nil)
	m.CompletedDate = day(2027, 1, 5)
	r.lister.set(m)

	assert.Equal(t, gcal.Result{Inserted: 1}, r.sync(t))
	assert.Equal(t, "✅ Water heater: Replaced anode", r.events()[0].Summary)

	// include_completed off removes completed events.
	off := false
	require.NoError(t, r.svc.UpdateSettings(context.Background(), r.gid, gcal.SettingsInput{IncludeCompleted: &off}))
	assert.Equal(t, gcal.Result{Deleted: 1}, r.sync(t))
	assert.Empty(t, r.events())

	// Entries with neither date never produce an event.
	r.lister.set(item("someday", nil))
	assert.Equal(t, gcal.Result{}, r.sync(t))
	assert.Empty(t, r.events())
}

func TestXReconcileDeleteRemovesEvent(t *testing.T) {
	r := newRecEnv(t, nil)
	keep, drop := item("keep", day(2027, 5, 1)), item("drop", day(2027, 5, 2))
	r.lister.set(keep, drop)
	assert.Equal(t, gcal.Result{Inserted: 2}, r.sync(t))

	r.lister.set(keep)
	assert.Equal(t, gcal.Result{Deleted: 1, Unchanged: 1}, r.sync(t))
	evs := r.events()
	require.Len(t, evs, 1)
	assert.Equal(t, keep.ID.String(), evs[0].ExtendedProperties.Private[gcal.IDProperty])

	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	assert.Len(t, links, 1, "the deleted source's link is gone too")
}

func TestXReconcileNeverTouchesForeignEvents(t *testing.T) {
	r := newRecEnv(t, nil)
	foreign := r.fake.AddEvent(householdCal, gcal.Event{
		Summary: "Dentist", Start: &gcal.EventDate{Date: "2027-03-14"}, End: &gcal.EventDate{Date: "2027-03-15"},
	})
	// Carries our marker but isn't parseable as one of ours (no usable source id): not ours to
	// manage, so it must survive too.
	odd := r.fake.AddEvent(householdCal, gcal.Event{
		Summary: "Odd", ExtendedProperties: &gcal.ExtendedProperties{Private: map[string]string{
			gcal.OwnerProperty: gcal.OwnerValue, gcal.IDProperty: "not-a-uuid",
		}},
	})
	// Someone else's event that merely shares a start date with ours.
	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)

	r.sync(t)
	m.Name = "renamed"
	r.lister.set(m)
	r.sync(t)
	r.lister.set() // source removed: our event goes, theirs stays
	r.sync(t)

	var ids []string
	for _, e := range r.events() {
		ids = append(ids, e.ID)
	}
	assert.ElementsMatch(t, []string{foreign, odd}, ids)
	for _, e := range r.events() {
		if e.ID == foreign {
			assert.Equal(t, "Dentist", e.Summary)
		}
	}
	r.fake.Set(func(f *gcaltestFake) {
		for _, req := range f.Requests {
			assert.NotContains(t, req, foreign, "foreign event must never be addressed")
		}
	})
}

func TestXReconcileLinkOnlyEventIsNotDeletedWhenForeign(t *testing.T) {
	// A link row that points at an event which is NOT ours on the calendar (marker missing)
	// must be dropped without deleting the event.
	r := newRecEnv(t, nil)
	foreign := r.fake.AddEvent(householdCal, gcal.Event{Summary: "Not ours"})
	gone := uuid.New()
	require.NoError(t, r.svc.Store().UpsertLink(context.Background(), r.gid, gcal.SourceMaintenance, gone, foreign, "h", time.Now()))

	assert.Equal(t, gcal.Result{}, r.sync(t))
	require.Len(t, r.events(), 1, "event with no Homebox marker is left alone")
	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestXReconcileRebuildsLostLinksWithoutDuplicates(t *testing.T) {
	r := newRecEnv(t, nil)
	a, b := item("a", day(2027, 6, 1)), item("b", day(2027, 6, 2))
	r.lister.set(a, b)
	r.sync(t)
	before := r.events()
	require.Len(t, before, 2)

	// Lose every link, and also remove a source while links are gone.
	n, err := r.svc.Store().DeleteLinks(context.Background(), r.gid)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	r.lister.set(a)

	res := r.sync(t)
	assert.Equal(t, 2, res.Relinked, "both owned events are re-linked by homebox_id")
	assert.Equal(t, 1, res.Deleted, "the orphan whose source vanished is cleaned up via its restored link")
	assert.Equal(t, 0, res.Inserted, "nothing is duplicated")

	evs := r.events()
	require.Len(t, evs, 1)
	assert.Equal(t, a.ID.String(), evs[0].ExtendedProperties.Private[gcal.IDProperty])
	var survivor string
	for _, e := range before {
		if e.ExtendedProperties.Private[gcal.IDProperty] == a.ID.String() {
			survivor = e.ID
		}
	}
	assert.Equal(t, survivor, evs[0].ID, "the original event was reused")

	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	assert.Len(t, links, 1)
}

func TestXReconcileReinsertsEventDeletedOnCalendar(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	old := r.events()[0].ID

	r.fake.Set(func(f *gcaltestFake) { delete(f.Events[householdCal], old) })
	res := r.sync(t)
	assert.Equal(t, 1, res.Inserted)
	evs := r.events()
	require.Len(t, evs, 1)
	assert.NotEqual(t, old, evs[0].ID)
	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, evs[0].ID, links[0].EventID)
}

// patch404 makes the first PATCH look like the event vanished between listing and patching.
type patch404 struct {
	base   http.RoundTripper
	fired  atomic.Bool
	onFire func()
}

func (p *patch404) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPatch && p.fired.CompareAndSwap(false, true) {
		if p.onFire != nil {
			p.onFire()
		}
		return &http.Response{
			StatusCode: http.StatusNotFound, Status: "404 Not Found", Request: req,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(
				`{"error":{"code":404,"message":"Not Found","errors":[{"reason":"notFound"}]}}`)),
		}, nil
	}
	return p.base.RoundTrip(req)
}

func TestXReconcilePatch404ReinsertsEvent(t *testing.T) {
	tr := &patch404{base: http.DefaultTransport}
	r := newRecEnv(t, func(o *gcal.Options) { o.HTTPClient = &http.Client{Transport: tr} })
	// The event disappears between listing and patching.
	tr.onFire = func() { r.fake.Set(func(f *gcaltestFake) { f.Events[householdCal] = map[string]gcal.Event{} }) }

	m := item("Flush tank", day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	old := r.events()[0].ID

	m.Name = "renamed"
	r.lister.set(m)
	res := r.sync(t)
	require.True(t, tr.fired.Load())
	assert.Equal(t, gcal.Result{Inserted: 1}, res, "a 404 on patch falls back to insert")

	evs := r.events()
	require.Len(t, evs, 1)
	assert.NotEqual(t, old, evs[0].ID)
	assert.Equal(t, "🔧 Water heater: renamed", evs[0].Summary)
	links, err := r.svc.Store().ListLinks(context.Background(), r.gid)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, evs[0].ID, links[0].EventID, "the link follows the re-inserted event")
}

func TestXReconcileBacksOffOn429(t *testing.T) {
	var sleeps atomic.Int32
	r := newRecEnv(t, func(o *gcal.Options) {
		o.Sleep = func(context.Context, time.Duration) error { sleeps.Add(1); return nil }
	})
	r.lister.set(item("Flush tank", day(2027, 3, 14)))

	r.fake.Set(func(f *gcaltestFake) { f.FailNext, f.FailStatus = 2, http.StatusTooManyRequests })
	res := r.sync(t)
	assert.Equal(t, 1, res.Inserted, "the sync succeeds after retrying")
	assert.EqualValues(t, 2, sleeps.Load(), "one backoff sleep per 429")
	assert.Len(t, r.events(), 1)
}

func TestXReconcileInvalidGrantMarksConnectionBroken(t *testing.T) {
	r := newRecEnv(t, nil)
	r.lister.set(item("Flush tank", day(2027, 3, 14)))
	r.sync(t)
	require.Len(t, r.events(), 1)

	r.fake.Set(func(f *gcaltestFake) { f.InvalidGrant = true })
	r.lister.set()
	_, err := r.rec.Reconcile(context.Background(), r.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)
	assert.Len(t, r.events(), 1, "nothing is touched with dead credentials")

	st, err := r.svc.Status(context.Background(), r.gid)
	require.NoError(t, err)
	assert.True(t, st.Broken)
	assert.NotEmpty(t, st.LastError)

	// A broken connection stays broken until reconnect, and the periodic sync skips it.
	_, err = r.rec.Reconcile(context.Background(), r.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)
	callsBefore := r.lister.calls.Load()
	r.rec.ReconcileAll(context.Background())
	assert.Equal(t, callsBefore, r.lister.calls.Load())
}

func TestXReconcileOtherFailureKeepsGoingAndReportsError(t *testing.T) {
	// 400 on the first insert only: not retried, and the second event is still created.
	r2 := newRecEnv(t, func(o *gcal.Options) {
		o.HTTPClient = &http.Client{Transport: &failFirstPost{base: http.DefaultTransport}}
	})
	r2.lister.set(item("a", day(2027, 3, 1)), item("b", day(2027, 3, 2)))
	_, err := r2.rec.Reconcile(context.Background(), r2.gid)
	require.Error(t, err)
	assert.NotErrorIs(t, err, gcal.ErrBroken)
	assert.Len(t, r2.events(), 1, "the other entry was still synced")

	st, serr := r2.svc.Status(context.Background(), r2.gid)
	require.NoError(t, serr)
	assert.False(t, st.Broken)
	assert.NotEmpty(t, st.LastError, "the failure is shown on the settings page")

	// The next run retries what failed and clears the error.
	res := r2.sync(t)
	assert.Equal(t, 1, res.Inserted)
	assert.Len(t, r2.events(), 2)
	st, serr = r2.svc.Status(context.Background(), r2.gid)
	require.NoError(t, serr)
	assert.Empty(t, st.LastError)
}

type failFirstPost struct {
	base  http.RoundTripper
	fired atomic.Bool
}

func (p *failFirstPost) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/events") && p.fired.CompareAndSwap(false, true) {
		return &http.Response{
			StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Request: req,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(
				`{"error":{"code":400,"message":"bad event","errors":[{"reason":"invalid"}]}}`)),
		}, nil
	}
	return p.base.RoundTrip(req)
}

func TestXReconcileNotConnectedAndNoCalendar(t *testing.T) {
	e := newEnv(t)
	l := &fakeLister{}
	rec := gcal.NewReconciler(e.svc, gcal.ReconcilerOptions{}, gcal.MaintenanceProvider{Source: l})

	_, err := rec.Reconcile(context.Background(), e.gid)
	require.ErrorIs(t, err, gcal.ErrNotConnected)

	e.connect(t)
	_, err = rec.Reconcile(context.Background(), e.gid)
	require.ErrorIs(t, err, gcal.ErrCalendarNotSet)
	assert.Zero(t, l.calls.Load(), "no sources are read before a calendar is chosen")
}

func TestXReconcileChangedCalendarMovesEvents(t *testing.T) {
	r := newRecEnv(t, nil)
	r.lister.set(item("Flush tank", day(2027, 3, 14)))
	r.sync(t)

	other := "owner@example.com"
	require.NoError(t, r.svc.UpdateSettings(context.Background(), r.gid, gcal.SettingsInput{CalendarID: &other}))
	res := r.sync(t)
	assert.Equal(t, 1, res.Inserted, "the new calendar gets its own copy")
	assert.Len(t, r.fake.EventsOn(other), 1)
}

func TestXReconcileEnqueueDebouncesPerGroup(t *testing.T) {
	r := newRecEnv(t, nil)
	r.lister.set(item("Flush tank", day(2027, 3, 14)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.rec.Run(ctx) }()

	for range 5 {
		r.rec.Enqueue(r.gid)
	}
	require.Eventually(t, func() bool { return len(r.events()) == 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(150 * time.Millisecond) // well past the debounce: no second run for the burst
	assert.EqualValues(t, 1, r.lister.calls.Load(), "five requests collapse into one reconcile")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}

func TestXReconcileEnqueueIsNoOpWhenNotConfigured(t *testing.T) {
	e := newEnv(t)
	cfg := testConfig()
	cfg.GoogleClientID = ""
	svc := gcal.New(e.ent, cfg, e.fake.Options())
	l := &fakeLister{}
	rec := gcal.NewReconciler(svc, gcal.ReconcilerOptions{Debounce: 5 * time.Millisecond}, gcal.MaintenanceProvider{Source: l})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rec.Run(ctx) }()
	rec.Enqueue(e.gid)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, l.calls.Load())

	var nilRec *gcal.Reconciler
	nilRec.Enqueue(e.gid) // must not panic
}

func TestXReconcileAllSyncsEveryReadyGroup(t *testing.T) {
	r := newRecEnv(t, nil)
	r.lister.set(item("Flush tank", day(2027, 3, 14)))
	r.rec.ReconcileAll(context.Background())
	assert.Len(t, r.events(), 1)
}

func TestXMaintenanceDesiredMapping(t *testing.T) {
	conn := gcal.Connection{CalendarID: "c", IncludeCompleted: true, ReminderMinutes: 0}
	m := item("t", day(2027, 1, 2))

	d, ok := gcal.MaintenanceDesired(m, conn, "")
	require.True(t, ok)
	assert.Empty(t, d.Event.Reminders.Overrides, "reminder_minutes 0 means no reminder")
	assert.NotContains(t, d.Event.Description, "http", "no base URL, no deep link")
	assert.Equal(t, gcal.SourceMaintenance, d.SourceType)

	// The hash covers content and calendar.
	d2, _ := gcal.MaintenanceDesired(m, conn, "")
	assert.Equal(t, d.Hash, d2.Hash)
	conn.CalendarID = "other"
	d3, _ := gcal.MaintenanceDesired(m, conn, "")
	assert.NotEqual(t, d.Hash, d3.Hash)
	m.Name = "renamed"
	d4, _ := gcal.MaintenanceDesired(m, conn, "")
	assert.NotEqual(t, d3.Hash, d4.Hash)
}
