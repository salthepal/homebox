package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/usergroup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

type icsEnv struct {
	a     *app
	token string
	gid   uuid.UUID
	deps  xapi.Deps
	r     chi.Router
}

// newIcsEnv wires the feed against real upstream tables. The test user starts as an owner
// of the group (the feed can only be managed by owners); setOwner(false) demotes it.
func newIcsEnv(t *testing.T, limiter *ics.Limiter, opts ...func(*xapi.Deps)) icsEnv {
	t.Helper()
	a, token, gid, deps := newCustomTestApp(t)
	deps.Config = xconfig.Config{PublicBaseURL: gcalTestBaseURL}
	deps.IcsFeed = upstream.NewMaintenanceReader(a.db)
	deps.IcsGroups = upstream.NewGroups(a.db)
	deps.IcsLimiter = limiter
	for _, o := range opts {
		o(&deps)
	}
	r := chi.NewRouter()
	mountCustomRoutes(a, r, errchain.New(mid.Errors(zerolog.Nop())), deps)
	e := icsEnv{a: a, token: token, gid: gid, deps: deps, r: r}
	e.setOwner(t, true)
	return e
}

// setOwner makes the test user an owner of (true) or an ordinary member of (false) its group.
func (e icsEnv) setOwner(t *testing.T, owner bool) {
	t.Helper()
	role := usergroup.RoleUser
	if owner {
		role = usergroup.RoleOwner
	}
	n, err := e.a.db.UserGroup.Update().Where(usergroup.GroupID(e.gid)).SetRole(role).Save(context.Background())
	require.NoError(t, err)
	require.Positive(t, n)
}

func (e icsEnv) get(path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func summaries(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(l, "SUMMARY:") {
			out = append(out, strings.TrimPrefix(l, "SUMMARY:"))
		}
	}
	return out
}

func (e icsEnv) do(method, path string, authed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if authed {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func (e icsEnv) addEntry(t *testing.T, gid uuid.UUID, item, name string, sched, done *time.Time) {
	t.Helper()
	ctx := context.Background()
	it, err := e.a.repos.Entities.Create(ctx, gid, repo.EntityCreate{Name: item, Quantity: 1})
	require.NoError(t, err)
	c := repo.MaintenanceEntryCreate{Name: name, Cost: 12}
	if sched != nil {
		c.ScheduledDate = types.DateFromTime(*sched)
	}
	if done != nil {
		c.CompletedDate = types.DateFromTime(*done)
	}
	_, err = e.a.repos.MaintEntry.Create(ctx, gid, it.ID, c)
	require.NoError(t, err)
}

// feedURL issues a token through the authenticated API and returns the path to fetch.
func (e icsEnv) feedPath(t *testing.T) (path, token string) {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/v1/x/calendar/feed", true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := decodeBody[struct {
		URL string `json:"url"`
	}](t, rec)
	const prefix = gcalTestBaseURL
	require.True(t, strings.HasPrefix(body.URL, prefix+"/api/v1/x/calendar/"), body.URL)
	require.True(t, strings.HasSuffix(body.URL, ".ics"))
	path = strings.TrimPrefix(body.URL, prefix)
	token = strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/x/calendar/"), ".ics")
	return path, token
}

func TestCustomIcsFeedManagementRequiresAuth(t *testing.T) {
	e := newIcsEnv(t, nil)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/x/calendar/feed"},
		{http.MethodPost, "/api/v1/x/calendar/feed"},
		{http.MethodDelete, "/api/v1/x/calendar/feed"},
	} {
		assert.Equal(t, http.StatusUnauthorized, e.do(c.method, c.path, false).Code, c.method+" "+c.path)
	}
}

func TestCustomIcsFeedLifecycle(t *testing.T) {
	e := newIcsEnv(t, nil)
	sched := time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)
	done := time.Date(2027, 2, 2, 0, 0, 0, 0, time.UTC)
	e.addEntry(t, e.gid, "Water heater", "Flush, tank; yearly", &sched, nil)
	e.addEntry(t, e.gid, "Furnace", "Replace filter", nil, &done)
	e.addEntry(t, e.gid, "Roof", "no dates", nil, nil)

	// No feed yet.
	st := decodeBody[ics.Status](t, e.do(http.MethodGet, "/api/v1/x/calendar/feed", true))
	assert.False(t, st.Exists)

	path, token := e.feedPath(t)

	// Status never reveals the token.
	rec := e.do(http.MethodGet, "/api/v1/x/calendar/feed", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), token)
	assert.True(t, decodeBody[ics.Status](t, rec).Exists)

	// The feed itself needs no Authorization header.
	rec = e.do(http.MethodGet, path, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/calendar; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	body := rec.Body.String()
	assert.True(t, strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n"))
	assert.True(t, strings.HasSuffix(body, "END:VCALENDAR\r\n"))
	assert.Contains(t, body, "X-WR-CALNAME:Homebox Maintenance\r\n")
	assert.Equal(t, 2, strings.Count(body, "BEGIN:VEVENT"), "undated entries are not calendar material")
	assert.Contains(t, body, `SUMMARY:🔧 Water heater: Flush\, tank\; yearly`+"\r\n")
	assert.Contains(t, body, "DTSTART;VALUE=DATE:20270314\r\n")
	assert.Contains(t, body, "DTEND;VALUE=DATE:20270315\r\n")
	assert.Contains(t, body, "SUMMARY:✅ Furnace: Replace filter\r\n")
	assert.Contains(t, body, "DTSTART;VALUE=DATE:20270202\r\n")
	assert.Contains(t, body, "TRIGGER:-PT1440M")
	assert.Equal(t, 1, strings.Count(body, "BEGIN:VALARM"), "completed entries have no reminder")
	assert.NotContains(t, body, "Roof")

	// Stable UIDs across fetches.
	again := e.do(http.MethodGet, path, false).Body.String()
	uids := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\r\n") {
			if strings.HasPrefix(l, "UID:") {
				out = append(out, l)
			}
		}
		return out
	}
	assert.Equal(t, uids(body), uids(again))

	// Rotate: old URL dies, new one works.
	newPath, newToken := e.feedPath(t)
	assert.NotEqual(t, token, newToken)
	assert.Equal(t, http.StatusNotFound, e.do(http.MethodGet, path, false).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, newPath, false).Code)

	// Revoke: the feed is gone.
	assert.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)
	assert.Equal(t, http.StatusNotFound, e.do(http.MethodGet, newPath, false).Code)
	assert.False(t, decodeBody[ics.Status](t, e.do(http.MethodGet, "/api/v1/x/calendar/feed", true)).Exists)
	assert.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code, "idempotent")
}

func TestCustomIcsFeedCrossGroupIsolation(t *testing.T) {
	e := newIcsEnv(t, nil)
	ctx := context.Background()
	other, err := e.a.repos.Groups.GroupCreate(ctx, "other-group", uuid.Nil)
	require.NoError(t, err)

	sched := time.Date(2027, 8, 1, 0, 0, 0, 0, time.UTC)
	e.addEntry(t, e.gid, "Mine", "my task", &sched, nil)
	e.addEntry(t, other.ID, "Theirs", "their task", &sched, nil)

	path, _ := e.feedPath(t)
	otherTok, err := ics.NewStore(e.deps.Ent).Issue(ctx, other.ID, uuid.New())
	require.NoError(t, err)

	mine := e.do(http.MethodGet, path, false).Body.String()
	assert.Contains(t, mine, "Mine: my task")
	assert.NotContains(t, mine, "their task")

	theirs := e.do(http.MethodGet, "/api/v1/x/calendar/"+otherTok+".ics", false).Body.String()
	assert.Contains(t, theirs, "Theirs: their task")
	assert.NotContains(t, theirs, "my task")

	// Revoking my feed doesn't touch theirs.
	require.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)
	assert.Equal(t, http.StatusNotFound, e.do(http.MethodGet, path, false).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/v1/x/calendar/"+otherTok+".ics", false).Code)
}

// Unknown, malformed and revoked tokens are indistinguishable: same status, same body.
func TestCustomIcsFeedNoExistenceOracle(t *testing.T) {
	e := newIcsEnv(t, nil)
	path, _ := e.feedPath(t)
	require.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)

	responses := map[string]*httptest.ResponseRecorder{
		"revoked":   e.do(http.MethodGet, path, false),
		"unknown":   e.do(http.MethodGet, "/api/v1/x/calendar/"+strings.Repeat("A", 43)+".ics", false),
		"malformed": e.do(http.MethodGet, "/api/v1/x/calendar/not-a-token.ics", false),
		"huge":      e.do(http.MethodGet, "/api/v1/x/calendar/"+strings.Repeat("z", 5000)+".ics", false),
	}
	for name, rec := range responses {
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
		assert.Equal(t, responses["revoked"].Body.String(), rec.Body.String(), name)
		assert.Equal(t, responses["revoked"].Header().Get("Content-Type"), rec.Header().Get("Content-Type"), name)
	}
}

func TestCustomIcsFeedRateLimited(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e := newIcsEnv(t, ics.NewLimiter(3, time.Minute, func() time.Time { return now }))
	path, _ := e.feedPath(t)

	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, e.do(http.MethodGet, path, false).Code, "request %d", i)
	}
	rec := e.do(http.MethodGet, path, false)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))

	// Bad guesses use up the same budget (limit applies before the token check).
	now = now.Add(time.Minute)
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusNotFound, e.do(http.MethodGet, "/api/v1/x/calendar/"+strings.Repeat("B", 43)+".ics", false).Code)
	}
	assert.Equal(t, http.StatusTooManyRequests, e.do(http.MethodGet, path, false).Code)
}

func TestCustomIcsFeedDoesNotShadowStaticRoutes(t *testing.T) {
	e := newIcsEnv(t, nil)
	// "feed" has no .ics suffix, so it must reach the management handlers, not the feed route.
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/api/v1/x/calendar/feed", false).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/v1/x/calendar/feed", true).Code)
}
