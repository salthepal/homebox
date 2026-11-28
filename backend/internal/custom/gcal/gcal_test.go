package gcal_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

var testKey = bytes.Repeat([]byte{7}, 32)

func testConfig() xconfig.Config {
	return xconfig.Config{
		PublicBaseURL:      "https://homebox.example.org",
		GoogleClientID:     "client-id",
		GoogleClientSecret: "client-secret",
		SecretKey:          testKey,
		GcalSyncInterval:   15 * time.Minute,
	}
}

type env struct {
	svc  *gcal.Service
	fake *gcaltest.Fake
	db   xtest.DB
	ent  *xent.Client
	gid  uuid.UUID
	uid  uuid.UUID
}

func newEnv(t *testing.T) env {
	t.Helper()
	d := xtest.Open(t)
	_, err := migrations.Run(context.Background(), d.SQL, d.Driver)
	require.NoError(t, err)
	c := xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))
	f := gcaltest.New(t)
	return env{
		svc: gcal.New(c, testConfig(), f.Options()), fake: f, db: d, ent: c,
		gid: uuid.New(), uid: uuid.New(),
	}
}

// connect runs the real connect flow against the fake: ConnectURL, then the callback.
func (e env) connect(t *testing.T) {
	t.Helper()
	u, err := e.svc.ConnectURL(e.uid, e.gid)
	require.NoError(t, err)
	state := mustParse(t, u).Query().Get("state")
	gid, err := e.svc.HandleCallback(context.Background(), gcaltest.Code, state)
	require.NoError(t, err)
	require.Equal(t, e.gid, gid)
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

func TestXGcalCrypto(t *testing.T) {
	sealed, err := gcal.Encrypt(testKey, []byte("refresh-token"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "refresh-token")

	pt, err := gcal.Decrypt(testKey, sealed)
	require.NoError(t, err)
	assert.Equal(t, "refresh-token", string(pt))

	again, err := gcal.Encrypt(testKey, []byte("refresh-token"))
	require.NoError(t, err)
	assert.NotEqual(t, sealed, again, "fresh nonce per call")

	_, err = gcal.Decrypt(bytes.Repeat([]byte{8}, 32), sealed)
	require.Error(t, err, "wrong key")

	sealed[len(sealed)-1] ^= 1
	_, err = gcal.Decrypt(testKey, sealed)
	require.Error(t, err, "tampered")

	_, err = gcal.Decrypt(testKey, []byte("short"))
	require.Error(t, err)
	_, err = gcal.Encrypt(nil, []byte("x"))
	require.Error(t, err, "no key")
}

func TestXGcalState(t *testing.T) {
	uid, gid := uuid.New(), uuid.New()
	now := time.Now()

	s, err := gcal.SignState(testKey, uid, gid, now)
	require.NoError(t, err)
	gotU, gotG, err := gcal.VerifyState(testKey, s, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, uid, gotU)
	assert.Equal(t, gid, gotG)

	_, _, err = gcal.VerifyState(testKey, s, now.Add(gcal.StateTTL+time.Second))
	require.ErrorIs(t, err, gcal.ErrInvalidState, "expired")

	_, _, err = gcal.VerifyState(bytes.Repeat([]byte{9}, 32), s, now)
	require.ErrorIs(t, err, gcal.ErrInvalidState, "signed with another key")

	// Swap the payload for another group's, keep the old signature.
	other, err := gcal.SignState(testKey, uid, uuid.New(), now)
	require.NoError(t, err)
	forged := other[:bytes.IndexByte([]byte(other), '.')] + s[bytes.IndexByte([]byte(s), '.'):]
	_, _, err = gcal.VerifyState(testKey, forged, now)
	require.ErrorIs(t, err, gcal.ErrInvalidState, "forged")

	for _, bad := range []string{"", "nodot", ".", "a.b", s + "x"} {
		_, _, err = gcal.VerifyState(testKey, bad, now)
		require.ErrorIs(t, err, gcal.ErrInvalidState, "%q", bad)
	}
	_, _, err = gcal.VerifyState(nil, s, now)
	require.ErrorIs(t, err, gcal.ErrInvalidState, "no key")
}

func TestXGcalClientRetries(t *testing.T) {
	var calls atomic.Int32
	var failures atomic.Int32
	failures.Store(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if failures.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"a","summary":"A","accessRole":"owner"}]}`))
	}))
	t.Cleanup(srv.Close)

	var sleeps []time.Duration
	c := &gcal.Client{
		HTTP: srv.Client(), BaseURL: srv.URL, Backoff: 10 * time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil },
	}
	cals, err := c.ListCalendars(context.Background())
	require.NoError(t, err)
	require.Len(t, cals, 1)
	assert.EqualValues(t, 3, calls.Load())
	assert.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}, sleeps, "exponential backoff")

	// 429 retries too; a permanent 5xx gives up after max attempts with the API error.
	calls.Store(0)
	failures.Store(100)
	sleeps = nil
	_, err = c.ListCalendars(context.Background())
	var ae *gcal.APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusServiceUnavailable, ae.Status)
	assert.EqualValues(t, 4, calls.Load())

	// 4xx (other than 429) is not retried.
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(notFound.Close)
	calls.Store(0)
	c.BaseURL = notFound.URL
	err = c.DeleteEvent(context.Background(), "cal", "evt")
	require.Error(t, err)
	assert.True(t, gcal.IsNotFound(err))
	assert.EqualValues(t, 1, calls.Load())
}

func TestXGcalClientRetryAfterAndContext(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	var slept time.Duration
	c := &gcal.Client{HTTP: srv.Client(), BaseURL: srv.URL,
		Sleep: func(_ context.Context, d time.Duration) error { slept = d; return nil }}
	_, err := c.ListCalendars(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, slept, "Retry-After honoured")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c2 := &gcal.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	_, err = c2.ListCalendars(ctx)
	require.Error(t, err)
}

func TestXGcalClientEvents(t *testing.T) {
	f := gcaltest.New(t)
	c := &gcal.Client{HTTP: &http.Client{Transport: bearer{}}, BaseURL: f.Server.URL + "/calendar"}
	ctx := context.Background()
	cal := "household@group.calendar.google.com"

	ours := gcal.Event{
		Summary: "🔧 Furnace: filter", Start: &gcal.EventDate{Date: "2027-01-05"}, End: &gcal.EventDate{Date: "2027-01-06"},
		Reminders:          &gcal.Reminders{Overrides: []gcal.ReminderOverride{{Method: "popup", Minutes: 1440}}},
		ExtendedProperties: &gcal.ExtendedProperties{Private: map[string]string{gcal.OwnerProperty: gcal.OwnerValue, gcal.IDProperty: "x"}},
	}
	created, err := c.InsertEvent(ctx, cal, ours)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	f.AddEvent(cal, gcal.Event{Summary: "Dentist"}) // someone else's

	list, err := c.ListEvents(ctx, cal, gcal.ListEventsParams{PrivateExtendedProperty: gcal.OwnerProperty + "=" + gcal.OwnerValue})
	require.NoError(t, err)
	require.Len(t, list, 1, "filter returns only Homebox events")
	assert.True(t, list[0].IsOurs())
	all, err := c.ListEvents(ctx, cal, gcal.ListEventsParams{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	patched, err := c.PatchEvent(ctx, cal, created.ID, gcal.Event{Summary: "✅ Furnace: filter"})
	require.NoError(t, err)
	assert.Equal(t, "✅ Furnace: filter", patched.Summary)

	require.NoError(t, c.DeleteEvent(ctx, cal, created.ID))
	err = c.DeleteEvent(ctx, cal, created.ID)
	assert.True(t, gcal.IsNotFound(err))
	_, err = c.PatchEvent(ctx, cal, created.ID, gcal.Event{Summary: "x"})
	assert.True(t, gcal.IsNotFound(err))
}

// bearer adds the fake's access token.
type bearer struct{}

func (bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer fake-access")
	return http.DefaultTransport.RoundTrip(r)
}

func TestXGcalNotConfigured(t *testing.T) {
	d := xtest.Open(t)
	_, err := migrations.Run(context.Background(), d.SQL, d.Driver)
	require.NoError(t, err)
	c := xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))
	svc := gcal.New(c, xconfig.Config{GoogleClientID: "x"}, gcal.Options{})

	st, err := svc.Status(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.False(t, st.Enabled)
	assert.ElementsMatch(t, []string{"HBOX_X_PUBLIC_BASE_URL", "HBOX_X_GOOGLE_CLIENT_SECRET", "HBOX_X_SECRET_KEY"}, st.Missing)
	assert.False(t, st.Connected)

	_, err = svc.ConnectURL(uuid.New(), uuid.New())
	var nc *gcal.NotConfiguredError
	require.ErrorAs(t, err, &nc)
	assert.Contains(t, nc.Error(), "HBOX_X_SECRET_KEY")

	_, err = svc.HandleCallback(context.Background(), "c", "s")
	require.ErrorAs(t, err, &nc)
}

func TestXGcalConnectFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	st, err := e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.True(t, st.Enabled)
	assert.Empty(t, st.Missing)
	assert.False(t, st.Connected)
	assert.Equal(t, "https://homebox.example.org/api/v1/x/gcal/callback", st.RedirectURI)

	u, err := e.svc.ConnectURL(e.uid, e.gid)
	require.NoError(t, err)
	q := mustParse(t, u).Query()
	assert.Equal(t, "client-id", q.Get("client_id"))
	assert.Equal(t, "offline", q.Get("access_type"))
	assert.Equal(t, "consent", q.Get("prompt"))
	assert.Equal(t, "https://homebox.example.org/api/v1/x/gcal/callback", q.Get("redirect_uri"))
	assert.Contains(t, q.Get("scope"), gcal.ScopeEvents)
	assert.Contains(t, q.Get("scope"), gcal.ScopeCalendarList)

	e.connect(t)

	st, err = e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.True(t, st.Connected)
	assert.False(t, st.Broken)
	assert.Equal(t, "owner@example.com", st.GoogleEmail, "primary calendar id is the account email")
	assert.Empty(t, st.CalendarID, "calendar not chosen yet")

	// The refresh token is encrypted at rest.
	var raw []byte
	require.NoError(t, e.db.SQL.QueryRow("SELECT enc_refresh_token FROM x_gcal_connections").Scan(&raw))
	assert.NotContains(t, string(raw), gcaltest.RefreshToken)
	pt, err := gcal.Decrypt(testKey, raw)
	require.NoError(t, err)
	assert.Equal(t, gcaltest.RefreshToken, string(pt))

	// Another group sees nothing.
	other, err := e.svc.Status(ctx, uuid.New())
	require.NoError(t, err)
	assert.False(t, other.Connected)

	// Picker lists writable calendars only, primary first.
	cals, err := e.svc.Calendars(ctx, e.gid)
	require.NoError(t, err)
	require.Len(t, cals, 2)
	assert.Equal(t, "owner@example.com", cals[0].ID)
	assert.Equal(t, "household@group.calendar.google.com", cals[1].ID)

	// Choose the shared calendar.
	hh := "household@group.calendar.google.com"
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	st, _ = e.svc.Status(ctx, e.gid)
	assert.Equal(t, hh, st.CalendarID)
	assert.Equal(t, "Household", st.CalendarSummary)
	assert.True(t, st.IncludeCompleted)
	assert.Equal(t, 1440, st.ReminderMinutes)

	// Options.
	f, mins := false, 60
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{IncludeCompleted: &f, ReminderMinutes: &mins}))
	st, _ = e.svc.Status(ctx, e.gid)
	assert.False(t, st.IncludeCompleted)
	assert.Equal(t, 60, st.ReminderMinutes)
	assert.Equal(t, hh, st.CalendarID, "unchanged fields stay")

	// Validation: read-only and unknown calendars, bad reminder values.
	for _, bad := range []string{"holidays@group.v.calendar.google.com", "nope@example.com", "  "} {
		err := e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &bad})
		require.ErrorIs(t, err, gcal.ErrInvalidSetting, "%q", bad)
	}
	neg, huge := -1, gcal.MaxReminderMinutes+1
	require.ErrorIs(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{ReminderMinutes: &neg}), gcal.ErrInvalidSetting)
	require.ErrorIs(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{ReminderMinutes: &huge}), gcal.ErrInvalidSetting)

	// Settings for a group without a connection.
	require.ErrorIs(t, e.svc.UpdateSettings(ctx, uuid.New(), gcal.SettingsInput{IncludeCompleted: &f}), gcal.ErrNotConnected)
	_, err = e.svc.Calendars(ctx, uuid.New())
	require.ErrorIs(t, err, gcal.ErrNotConnected)

	// Reconnecting keeps the calendar choice and options.
	e.connect(t)
	st, _ = e.svc.Status(ctx, e.gid)
	assert.Equal(t, hh, st.CalendarID)
	assert.False(t, st.IncludeCompleted)
}

func TestXGcalCallbackRejections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	_, err := e.svc.HandleCallback(ctx, gcaltest.Code, "forged.state")
	require.ErrorIs(t, err, gcal.ErrInvalidState)

	expired, err := gcal.SignState(testKey, e.uid, e.gid, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	_, err = e.svc.HandleCallback(ctx, gcaltest.Code, expired)
	require.ErrorIs(t, err, gcal.ErrInvalidState)

	u, err := e.svc.ConnectURL(e.uid, e.gid)
	require.NoError(t, err)
	state := mustParse(t, u).Query().Get("state")

	_, err = e.svc.HandleCallback(ctx, "wrong-code", state)
	require.Error(t, err)

	_, err = e.svc.HandleCallback(ctx, "", state)
	require.Error(t, err)

	e.fake.Set(func(f *gcaltest.Fake) { f.NoRefreshToken = true })
	_, err = e.svc.HandleCallback(ctx, gcaltest.Code, state)
	require.ErrorContains(t, err, "no refresh token")

	_, err = e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrNotConnected, "failed callbacks store nothing")
}

func TestXGcalBrokenConnection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)

	e.fake.Set(func(f *gcaltest.Fake) { f.InvalidGrant = true })
	_, err := e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)

	st, err := e.svc.Status(ctx, e.gid)
	require.NoError(t, err)
	assert.True(t, st.Broken)
	assert.NotEmpty(t, st.LastError)

	// Further calls fail fast without contacting Google.
	e.fake.Set(func(f *gcaltest.Fake) { f.Requests = nil })
	_, err = e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)
	assert.Empty(t, e.fake.Requests)

	// Reconnect repairs it.
	e.fake.Set(func(f *gcaltest.Fake) { f.InvalidGrant = false })
	e.connect(t)
	st, _ = e.svc.Status(ctx, e.gid)
	assert.False(t, st.Broken)
	assert.Empty(t, st.LastError)
	_, err = e.svc.Calendars(ctx, e.gid)
	require.NoError(t, err)
}

func TestXGcalAPIAuthErrorMarksBroken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)

	// Token refresh works but the API rejects the access token.
	e.fake.Set(func(f *gcaltest.Fake) { f.FailNext, f.FailStatus = 1, http.StatusUnauthorized })
	_, err := e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)
	st, _ := e.svc.Status(ctx, e.gid)
	assert.True(t, st.Broken)
}

func TestXGcalLinksStore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s := e.svc.Store()
	src := uuid.New()
	now := time.Now()

	_, ok, err := s.GetLink(ctx, e.gid, gcal.SourceMaintenance, src)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, src, "evt1", "h1", now))
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, src, "evt1", "h2", now), "upsert updates")
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceProjectStep, src, "evt2", "h3", now), "same id, other type")
	require.NoError(t, s.UpsertLink(ctx, uuid.New(), gcal.SourceMaintenance, src, "evt3", "h4", now), "other group")

	l, ok, err := s.GetLink(ctx, e.gid, gcal.SourceMaintenance, src)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "h2", l.ContentHash)

	links, err := s.ListLinks(ctx, e.gid)
	require.NoError(t, err)
	assert.Len(t, links, 2, "scoped to the group")

	require.NoError(t, s.DeleteLink(ctx, e.gid, gcal.SourceProjectStep, src))
	require.NoError(t, s.DeleteLink(ctx, e.gid, gcal.SourceProjectStep, src), "idempotent")
	n, err := s.DeleteLinks(ctx, e.gid)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var left int
	require.NoError(t, e.db.SQL.QueryRow("SELECT COUNT(*) FROM x_gcal_links").Scan(&left))
	assert.Equal(t, 1, left, "other group's link untouched")
}

func TestXGcalDisconnect(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := "household@group.calendar.google.com"
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))

	mark := &gcal.ExtendedProperties{Private: map[string]string{gcal.OwnerProperty: gcal.OwnerValue}}
	linked := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧 linked", ExtendedProperties: mark})
	// Ours by marker but no link (e.g. a lost links row): not provably ours, so left alone.
	unlinked := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧 unlinked", ExtendedProperties: mark})
	// Linked in our table but without the marker: also left alone.
	nomark := e.fake.AddEvent(hh, gcal.Event{Summary: "someone's"})
	foreign := e.fake.AddEvent(hh, gcal.Event{Summary: "Dentist"})
	other := e.fake.AddEvent("owner@example.com", gcal.Event{Summary: "mine", ExtendedProperties: mark})

	s := e.svc.Store()
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), linked, "h", time.Now()))
	require.NoError(t, s.UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), nomark, "h", time.Now()))

	res, err := e.svc.Disconnect(ctx, e.gid, true)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RemovedEvents)
	assert.False(t, res.RemoveSkipped)

	ids := map[string]bool{}
	for _, ev := range e.fake.EventsOn(hh) {
		ids[ev.ID] = true
	}
	assert.False(t, ids[linked], "linked + marked event removed")
	assert.True(t, ids[unlinked])
	assert.True(t, ids[nomark])
	assert.True(t, ids[foreign])
	assert.Len(t, e.fake.EventsOn("owner@example.com"), 1, "other calendars untouched")
	_ = other

	assert.Equal(t, gcaltest.RefreshToken, e.fake.Revoked, "token revoked at Google")
	st, _ := e.svc.Status(ctx, e.gid)
	assert.False(t, st.Connected)
	links, _ := s.ListLinks(ctx, e.gid)
	assert.Empty(t, links)

	_, err = e.svc.Disconnect(ctx, e.gid, false)
	require.ErrorIs(t, err, gcal.ErrNotConnected)
}

func TestXGcalDisconnectKeepsEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := "household@group.calendar.google.com"
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	mark := &gcal.ExtendedProperties{Private: map[string]string{gcal.OwnerProperty: gcal.OwnerValue}}
	id := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧", ExtendedProperties: mark})
	require.NoError(t, e.svc.Store().UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), id, "h", time.Now()))

	res, err := e.svc.Disconnect(ctx, e.gid, false)
	require.NoError(t, err)
	assert.Zero(t, res.RemovedEvents)
	assert.Len(t, e.fake.EventsOn(hh), 1, "remove_events=false leaves the calendar alone")
}

func TestXGcalDisconnectBrokenSkipsRemoval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := "household@group.calendar.google.com"
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	e.fake.Set(func(f *gcaltest.Fake) { f.InvalidGrant = true })
	_, err := e.svc.Calendars(ctx, e.gid)
	require.ErrorIs(t, err, gcal.ErrBroken)

	res, err := e.svc.Disconnect(ctx, e.gid, true)
	require.NoError(t, err)
	assert.True(t, res.RemoveSkipped)
	st, _ := e.svc.Status(ctx, e.gid)
	assert.False(t, st.Connected, "credentials are removed even when events can't be")
}

func TestXGcalDisconnectFailureKeepsConnection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(t)
	hh := "household@group.calendar.google.com"
	require.NoError(t, e.svc.UpdateSettings(ctx, e.gid, gcal.SettingsInput{CalendarID: &hh}))
	mark := &gcal.ExtendedProperties{Private: map[string]string{gcal.OwnerProperty: gcal.OwnerValue}}
	id := e.fake.AddEvent(hh, gcal.Event{Summary: "🔧", ExtendedProperties: mark})
	require.NoError(t, e.svc.Store().UpsertLink(ctx, e.gid, gcal.SourceMaintenance, uuid.New(), id, "h", time.Now()))

	e.fake.Set(func(f *gcaltest.Fake) { f.FailNext, f.FailStatus = 100, http.StatusForbidden })
	_, err := e.svc.Disconnect(ctx, e.gid, true)
	require.Error(t, err)
	st, _ := e.svc.Status(ctx, e.gid)
	assert.True(t, st.Connected, "connection kept so the user can retry")
}
