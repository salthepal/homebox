package gcal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
)

const ownerCal = "owner@example.com"

func ownedMark() *gcal.ExtendedProperties {
	return &gcal.ExtendedProperties{Private: map[string]string{gcal.OwnerProperty: gcal.OwnerValue}}
}

// statusServer answers every request with the status while counting calls.
func statusServer(t *testing.T, status int, calls *atomic.Int32) *gcal.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return &gcal.Client{
		HTTP: srv.Client(), BaseURL: srv.URL,
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func TestXGcalRetryPolicyByMethod(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32

	c := statusServer(t, http.StatusInternalServerError, &calls)
	_, err := c.InsertEvent(ctx, "cal", gcal.Event{Summary: "x"})
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "5xx on insert is not retried (could duplicate the event)")

	calls.Store(0)
	_, err = c.ListEvents(ctx, "cal", gcal.ListEventsParams{})
	require.Error(t, err)
	assert.EqualValues(t, 4, calls.Load(), "5xx on GET retries")

	calls.Store(0)
	_, err = c.PatchEvent(ctx, "cal", "e", gcal.Event{})
	require.Error(t, err)
	assert.EqualValues(t, 4, calls.Load(), "5xx on PATCH retries")

	calls.Store(0)
	require.Error(t, c.DeleteEvent(ctx, "cal", "e"))
	assert.EqualValues(t, 4, calls.Load(), "5xx on DELETE retries")

	calls.Store(0)
	c = statusServer(t, http.StatusTooManyRequests, &calls)
	_, err = c.InsertEvent(ctx, "cal", gcal.Event{})
	require.Error(t, err)
	assert.EqualValues(t, 4, calls.Load(), "429 on insert still retries")
}

func TestXGcalCallbackRevalidatesMembership(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, err := e.svc.ConnectURL(e.uid, e.gid)
	require.NoError(t, err)
	state := mustParse(t, u).Query().Get("state")

	var checked [2]uuid.UUID
	opts := e.fake.Options()
	opts.IsMember = func(_ context.Context, gid, uid uuid.UUID) (bool, error) {
		checked = [2]uuid.UUID{gid, uid}
		return false, nil
	}
	svc := gcal.New(e.ent, testConfig(), opts)

	gid, err := svc.HandleCallback(ctx, gcaltest.Code, state)
	require.ErrorIs(t, err, gcal.ErrNotMember)
	assert.Equal(t, e.gid, gid)
	assert.Equal(t, [2]uuid.UUID{e.gid, e.uid}, checked)
	_, err = svc.Status(ctx, e.gid)
	require.NoError(t, err)
	_, err = e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrNotConnected, "nothing stored for a removed member")
	for _, r := range e.fake.Requests {
		assert.NotContains(t, r, "calendar", "no Google calls before the membership check passes")
	}

	// A lookup error fails closed too.
	opts.IsMember = func(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, errors.New("db down") }
	_, err = gcal.New(e.ent, testConfig(), opts).HandleCallback(ctx, gcaltest.Code, state)
	require.ErrorIs(t, err, gcal.ErrNotMember)
}

func TestXGcalCallbackNilIsMemberFailsClosed(t *testing.T) {
	e := newEnv(t)
	u, err := e.svc.ConnectURL(e.uid, e.gid)
	require.NoError(t, err)
	state := mustParse(t, u).Query().Get("state")

	opts := e.fake.Options()
	opts.IsMember = nil
	svc := gcal.New(e.ent, testConfig(), opts)
	_, err = svc.HandleCallback(context.Background(), gcaltest.Code, state)
	require.ErrorIs(t, err, gcal.ErrNotMember)
	_, err = e.svc.Calendars(context.Background(), e.gid)
	require.ErrorIs(t, err, gcal.ErrNotConnected)
}

func TestXGcalSweepOrphans(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t) // kept group
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))

	gone, goneUser := uuid.New(), uuid.New()
	enc, err := gcal.Encrypt(testKey, []byte(gcaltest.RefreshToken))
	require.NoError(t, err)
	s := e.svc.Store()
	require.NoError(t, s.SaveConnection(ctx, gone, goneUser, "gone@example.com", enc))
	ev := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧", ExtendedProperties: ownedMark()})
	require.NoError(t, s.UpsertLink(ctx, gone, gcal.SourceMaintenance, uuid.New(), ev, "h", time.Now()))
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), "kept-ev", "h", time.Now()))

	var asked []uuid.UUID
	opts := e.fake.Options()
	opts.GroupsExist = func(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
		if asked == nil {
			asked = ids
		}
		return map[uuid.UUID]struct{}{e.gid: {}}, nil
	}
	n, err := gcal.New(e.ent, testConfig(), opts).SweepOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.ElementsMatch(t, []uuid.UUID{e.gid, gone}, asked)

	_, err = s.GetConnection(ctx, gone)
	require.ErrorIs(t, err, gcal.ErrNotConnected)
	links, err := s.ListLinks(ctx, gone)
	require.NoError(t, err)
	assert.Empty(t, links)
	assert.Equal(t, gcaltest.RefreshToken, e.fake.Revoked, "token revoked best-effort")
	assert.Len(t, e.fake.EventsOn(hh), 1, "Google events are never touched")

	_, err = s.GetConnection(ctx, e.gid)
	require.NoError(t, err)
	links, err = s.ListLinks(ctx, e.gid)
	require.NoError(t, err)
	assert.Len(t, links, 1, "kept group untouched")

	n, err = gcal.New(e.ent, testConfig(), opts).SweepOrphans(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "second run is a no-op")
}

// failDelete rejects DELETE requests so a calendar switch fails midway.
type failDelete struct{}

func (failDelete) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodDelete {
		return &http.Response{
			StatusCode: http.StatusForbidden, Body: http.NoBody, Header: http.Header{}, Request: r,
		}, nil
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestXGcalSwitchCalendarCleansOld(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))

	ours := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧 ours", ExtendedProperties: ownedMark()})
	foreign := e.fake.AddEvent(hh, gcal.Event{Summary: "Dentist"})
	s := e.svc.Store()
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), ours, "h", time.Now()))

	// Re-saving the same calendar changes nothing.
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	assert.Len(t, e.fakeIDs(hh), 2)
	links, _ := s.ListLinks(ctx, e.gid)
	require.Len(t, links, 1)

	oc := ownerCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &oc}))
	assert.Equal(t, []string{foreign}, e.fakeIDs(hh), "only our event left the old calendar")
	links, _ = s.ListLinks(ctx, e.gid)
	assert.Empty(t, links, "links cleared for a clean next sync")
	st, err := e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, ownerCal, st.CalendarID)
	assert.Empty(t, e.fake.EventsOn(ownerCal))
}

func TestXGcalSwitchCalendarFailureChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	ours := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧", ExtendedProperties: ownedMark()})
	s := e.svc.Store()
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), ours, "h", time.Now()))

	opts := e.fake.Options()
	opts.HTTPClient = &http.Client{Transport: failDelete{}}
	svc := gcal.New(e.ent, testConfig(), opts)
	oc := ownerCal
	require.Error(t, svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &oc}))

	st, err := svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, householdCal, st.CalendarID, "calendar unchanged")
	links, _ := s.ListLinks(ctx, e.gid)
	assert.Len(t, links, 1, "links kept")
	assert.Len(t, e.fake.EventsOn(hh), 1)
}

func TestXGcalDisconnectWithIncompleteConfig(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))

	cfg := testConfig()
	cfg.GoogleClientSecret = ""
	svc := gcal.New(e.ent, cfg, e.fake.Options())
	st, err := svc.Status(ctx, e.gid)
	require.NoError(t, err)
	require.False(t, st.Enabled)
	require.True(t, st.Connected, "stored connection is still reported")

	res, err := svc.Disconnect(ctx, e.gid, true)
	require.NoError(t, err)
	assert.True(t, res.RemoveSkipped)
	st, err = svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.False(t, st.Connected, "local disconnect works without Google configured")
}

func (e env) fakeIDs(cal string) []string {
	var ids []string
	for _, ev := range e.fake.EventsOn(cal) {
		ids = append(ids, ev.ID)
	}
	return ids
}

const otherAccount = "other@example.com"

func (e env) connectGroup(t *testing.T, gid, uid uuid.UUID) {
	t.Helper()
	u, err := e.svc.ConnectURL(uid, gid)
	require.NoError(t, err)
	_, err = e.svc.HandleCallback(context.Background(), gcaltest.Code, mustParse(t, u).Query().Get("state"))
	require.NoError(t, err)
}

func TestXGcalReconnectAccountChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	s := e.svc.Store()
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), "ev1", "h", time.Now()))

	e.connect(t) // same account: a token refresh
	st, err := e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, householdCal, st.CalendarID, "same-account reconnect keeps the calendar")
	links, _ := s.ListLinks(ctx, e.gid)
	assert.Len(t, links, 1, "and the links")

	e.fake.Set(func(f *gcaltest.Fake) { f.Calendars[0].ID = otherAccount })
	e.connect(t)
	st, err = e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, otherAccount, st.GoogleEmail)
	assert.Empty(t, st.CalendarID, "different account clears the calendar choice")
	links, _ = s.ListLinks(ctx, e.gid)
	assert.Empty(t, links, "and drops links so sources are inserted into the new calendar")
}

func TestXGcalRevokeOnlyForLastConnectionOfAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	gid2, uid2 := uuid.New(), uuid.New()
	e.connect(t)
	e.connectGroup(t, gid2, uid2)

	_, err := e.svc.Disconnect(ctx, e.gid, false)
	require.NoError(t, err)
	assert.Empty(t, e.fake.Revoked, "shared account: grant must not be revoked")
	_, err = e.svc.Calendars(ctx, gid2)
	require.NoError(t, err, "the other group stays usable")

	_, err = e.svc.Disconnect(ctx, gid2, false)
	require.NoError(t, err)
	assert.Equal(t, gcaltest.RefreshToken, e.fake.Revoked, "last connection revokes")
}

func TestXGcalSweepRevokesOnlyForLastConnectionOfAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	gid2, uid2 := uuid.New(), uuid.New()
	e.connect(t)
	e.connectGroup(t, gid2, uid2)

	alive := map[uuid.UUID]struct{}{e.gid: {}}
	opts := e.fake.Options()
	opts.GroupsExist = func(context.Context, []uuid.UUID) (map[uuid.UUID]struct{}, error) { return alive, nil }
	svc := gcal.New(e.ent, testConfig(), opts)

	n, err := svc.SweepOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Empty(t, e.fake.Revoked, "another group still uses the account")
	_, err = svc.Calendars(ctx, e.gid)
	require.NoError(t, err)

	alive = map[uuid.UUID]struct{}{}
	n, err = svc.SweepOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, gcaltest.RefreshToken, e.fake.Revoked)
}

func TestXGcalSwitchCalendarOldCalendarGone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := householdCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	s := e.svc.Store()
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), "ev1", "h", time.Now()))
	e.fake.Set(func(f *gcaltest.Fake) { f.Gone = map[string]bool{hh: true} })

	oc := ownerCal
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &oc}))
	st, err := e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, ownerCal, st.CalendarID, "can switch away from a deleted calendar")
	links, _ := s.ListLinks(ctx, e.gid)
	assert.Empty(t, links)
}
