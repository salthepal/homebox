package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal/gcaltest"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

// gcalTestBaseURL is the public base URL used by the gcal tests in this package.
const gcalTestBaseURL = "https://homebox.example.org"

// gcalTestSecret is the fake Google client secret used by the gcal tests in this package.
const gcalTestSecret = "secret"

type gcalAPI struct {
	t     *testing.T
	r     chi.Router
	token string
	fake  *gcaltest.Fake
}

// realGcalOptions is production's upstream wiring with Google's endpoints pointed at a fake.
func realGcalOptions(a *app, fake *gcaltest.Fake) gcal.Options {
	o := gcalOptions(a)
	fo := fake.Options()
	o.AuthURL, o.TokenURL, o.RevokeURL, o.APIBase = fo.AuthURL, fo.TokenURL, fo.RevokeURL, fo.APIBase
	return o
}

func newGcalAPI(t *testing.T, configured bool) *gcalAPI {
	t.Helper()
	a, token, _, deps := newCustomTestApp(t)
	fake := gcaltest.New(t)
	if configured {
		deps.Config = xconfig.Config{
			PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: gcalTestSecret,
			SecretKey: bytes.Repeat([]byte{3}, 32), GcalSyncInterval: time.Minute * 15,
		}
	}
	// Real upstream membership/existence adapters, pointed at the fake Google.
	deps.Gcal = realGcalOptions(a, fake)

	r := chi.NewRouter()
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	mountCustomRoutes(a, r, chain, xapi.Deps{Ent: deps.Ent, Config: deps.Config, Gcal: deps.Gcal})
	return &gcalAPI{t: t, r: r, token: token, fake: fake}
}

func (g *gcalAPI) do(method, path string, body any, authed bool) *httptest.ResponseRecorder {
	g.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(g.t, err)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	rec := httptest.NewRecorder()
	g.r.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func TestCustomGcalRequiresAuthExceptCallback(t *testing.T) {
	g := newGcalAPI(t, true)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/x/gcal/status"},
		{http.MethodPost, "/api/v1/x/gcal/connect"},
		{http.MethodGet, "/api/v1/x/gcal/calendars"},
		{http.MethodPut, "/api/v1/x/gcal/settings"},
		{http.MethodPost, "/api/v1/x/gcal/disconnect"},
	} {
		assert.Equal(t, http.StatusUnauthorized, g.do(c.method, c.path, nil, false).Code, "%s %s", c.method, c.path)
	}

	// The callback is a browser redirect with no Bearer token: it is reachable without one
	// and redirects (here to an error, since the state is bogus) instead of returning 401.
	rec := g.do(http.MethodGet, "/api/v1/x/gcal/callback?code=x&state=bogus", nil, false)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://homebox.example.org/x/calendar?error=state", rec.Header().Get("Location"))
}

func TestCustomGcalNotConfigured(t *testing.T) {
	g := newGcalAPI(t, false)

	rec := g.do(http.MethodGet, "/api/v1/x/gcal/status", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	st := decodeBody[gcal.Status](t, rec)
	assert.False(t, st.Enabled)
	assert.Contains(t, st.Missing, "HBOX_X_SECRET_KEY")
	assert.False(t, st.Connected)

	rec = g.do(http.MethodPost, "/api/v1/x/gcal/connect", nil, true)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "HBOX_X_GOOGLE_CLIENT_ID")

	// With no public base URL there is nowhere to redirect, so the callback 404s.
	assert.Equal(t, http.StatusNotFound, g.do(http.MethodGet, "/api/v1/x/gcal/callback?code=x&state=y", nil, false).Code)
}

func TestCustomGcalConnectFlowOverHTTP(t *testing.T) {
	g := newGcalAPI(t, true)

	// Not connected yet.
	rec := g.do(http.MethodGet, "/api/v1/x/gcal/calendars", nil, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Start: get the consent URL.
	rec = g.do(http.MethodPost, "/api/v1/x/gcal/connect", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	consent, err := url.Parse(decodeBody[map[string]string](t, rec)["url"])
	require.NoError(t, err)
	assert.Equal(t, "consent", consent.Query().Get("prompt"))
	state := consent.Query().Get("state")
	require.NotEmpty(t, state)

	// Google redirects the browser back (no Bearer token).
	cb := "/api/v1/x/gcal/callback?" + url.Values{"code": {gcaltest.Code}, "state": {state}}.Encode()
	rec = g.do(http.MethodGet, cb, nil, false)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	assert.Equal(t, "https://homebox.example.org/x/calendar?connected=1", rec.Header().Get("Location"))

	rec = g.do(http.MethodGet, "/api/v1/x/gcal/status", nil, true)
	st := decodeBody[gcal.Status](t, rec)
	assert.True(t, st.Enabled)
	assert.True(t, st.Connected)
	assert.Equal(t, "owner@example.com", st.GoogleEmail)
	assert.Equal(t, "https://homebox.example.org/api/v1/x/gcal/callback", st.RedirectURI)

	// The picker shows only calendars the account can write to.
	rec = g.do(http.MethodGet, "/api/v1/x/gcal/calendars", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cals := decodeBody[struct {
		Calendars []gcal.Calendar `json:"calendars"`
	}](t, rec).Calendars
	require.Len(t, cals, 2)
	for _, c := range cals {
		assert.True(t, c.Writable(), c.ID)
	}

	// Choose the household calendar and set options.
	rec = g.do(http.MethodPut, "/api/v1/x/gcal/settings", map[string]any{
		"calendar_id": "household@group.calendar.google.com", "include_completed": false, "reminder_minutes": 30,
	}, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	st = decodeBody[gcal.Status](t, rec)
	assert.Equal(t, "Household", st.CalendarSummary)
	assert.False(t, st.IncludeCompleted)
	assert.Equal(t, 30, st.ReminderMinutes)

	// Invalid choices are 400s.
	rec = g.do(http.MethodPut, "/api/v1/x/gcal/settings", map[string]any{"calendar_id": "holidays@group.v.calendar.google.com"}, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = g.do(http.MethodPut, "/api/v1/x/gcal/settings", map[string]any{"reminder_minutes": -5}, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Disconnect (body optional) removes the connection.
	rec = g.do(http.MethodPost, "/api/v1/x/gcal/disconnect", map[string]any{"remove_events": true}, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = g.do(http.MethodGet, "/api/v1/x/gcal/status", nil, true)
	assert.False(t, decodeBody[gcal.Status](t, rec).Connected)
	rec = g.do(http.MethodPost, "/api/v1/x/gcal/disconnect", nil, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCustomGcalCallbackFailures(t *testing.T) {
	g := newGcalAPI(t, true)

	rec := g.do(http.MethodGet, "/api/v1/x/gcal/callback?error=access_denied&state=x", nil, false)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://homebox.example.org/x/calendar?error=denied", rec.Header().Get("Location"))

	// A valid state with a code Google rejects.
	rec = g.do(http.MethodPost, "/api/v1/x/gcal/connect", nil, true)
	consent, err := url.Parse(decodeBody[map[string]string](t, rec)["url"])
	require.NoError(t, err)
	cb := "/api/v1/x/gcal/callback?" + url.Values{"code": {"bad"}, "state": {consent.Query().Get("state")}}.Encode()
	rec = g.do(http.MethodGet, cb, nil, false)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://homebox.example.org/x/calendar?error=failed", rec.Header().Get("Location"))

	rec = g.do(http.MethodGet, "/api/v1/x/gcal/status", nil, true)
	assert.False(t, decodeBody[gcal.Status](t, rec).Connected)
}

func TestCustomGcalCallbackRejectsNonMember(t *testing.T) {
	a, _, gid, deps := newCustomTestApp(t)
	fake := gcaltest.New(t)
	key := bytes.Repeat([]byte{3}, 32)
	cfg := xconfig.Config{
		PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: gcalTestSecret,
		SecretKey: key, GcalSyncInterval: time.Minute * 15,
	}
	opts := realGcalOptions(a, fake)
	r := chi.NewRouter()
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	mountCustomRoutes(a, r, chain, xapi.Deps{Ent: deps.Ent, Config: cfg, Gcal: opts})

	// A signed state for a user who is not in the group (e.g. removed after clicking Connect).
	state, err := gcal.SignState(key, uuid.New(), gid, time.Now())
	require.NoError(t, err)
	cb := "/api/v1/x/gcal/callback?" + url.Values{"code": {gcaltest.Code}, "state": {state}}.Encode()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cb, nil))
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, gcalTestBaseURL+"/x/calendar?error=membership", rec.Header().Get("Location"))

	conns, err := gcal.NewStore(deps.Ent).ListConnections(context.Background())
	require.NoError(t, err)
	assert.Empty(t, conns, "nothing stored")
}

func TestCustomGcalSweepOrphansWithRealGroups(t *testing.T) {
	a, _, kept, deps := newCustomTestApp(t)
	ctx := context.Background()
	fake := gcaltest.New(t)
	opts := realGcalOptions(a, fake)
	cfg := xconfig.Config{
		PublicBaseURL: gcalTestBaseURL, GoogleClientID: "id", GoogleClientSecret: gcalTestSecret,
		SecretKey: bytes.Repeat([]byte{3}, 32),
	}
	svc := gcal.New(deps.Ent, cfg, opts)

	doomed, err := a.repos.Groups.GroupCreate(ctx, "doomed", uuid.Nil)
	require.NoError(t, err)
	enc, err := gcal.Encrypt(cfg.SecretKey, []byte(gcaltest.RefreshToken))
	require.NoError(t, err)
	// Distinct accounts, so the doomed group's token is the last one for its account.
	for gid, email := range map[uuid.UUID]string{kept: "kept@example.com", doomed.ID: "doomed@example.com"} {
		require.NoError(t, svc.Store().SaveConnection(ctx, gid, uuid.New(), email, enc))
		require.NoError(t, svc.Store().UpsertLink(ctx, gid, gcal.SourceMaintenance, uuid.New(), "ev", "h", time.Now()))
	}
	require.NoError(t, a.db.Group.DeleteOneID(doomed.ID).Exec(ctx))

	n, err := svc.SweepOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = svc.Store().GetConnection(ctx, doomed.ID)
	require.ErrorIs(t, err, gcal.ErrNotConnected)
	links, err := svc.Store().ListLinks(ctx, doomed.ID)
	require.NoError(t, err)
	assert.Empty(t, links)
	assert.Equal(t, gcaltest.RefreshToken, fake.Revoked)
	_, err = svc.Store().GetConnection(ctx, kept)
	require.NoError(t, err, "live group's connection kept")
}
