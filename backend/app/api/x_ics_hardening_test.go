package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
)

// Review hardening of the public ICS feed: owner-only management, bounded output, ETag/304,
// orphaned groups and no-store headers on every response.

func TestCustomIcsFeedOnlyOwnersManage(t *testing.T) {
	e := newIcsEnv(t, nil)
	path, token := e.feedPath(t)

	e.setOwner(t, false)
	// A member can neither create/rotate nor revoke...
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodPost, "/api/v1/x/calendar/feed", true).Code)
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)
	// ...so nothing changed: the existing link still works.
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, path, false).Code)

	// ...but can see whether a feed exists, and never the token.
	rec := e.do(http.MethodGet, "/api/v1/x/calendar/feed", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, decodeBody[ics.Status](t, rec).Exists)
	assert.NotContains(t, rec.Body.String(), token)

	e.setOwner(t, true)
	assert.Equal(t, http.StatusCreated, e.do(http.MethodPost, "/api/v1/x/calendar/feed", true).Code)
	assert.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)
}

// With no group adapter wired, management fails closed.
func TestCustomIcsFeedManagementFailsClosedWithoutGroups(t *testing.T) {
	e := newIcsEnv(t, nil, func(d *xapi.Deps) { d.IcsGroups = nil })
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodPost, "/api/v1/x/calendar/feed", true).Code)
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodDelete, "/api/v1/x/calendar/feed", true).Code)
}

func TestCustomIcsFeedWindow(t *testing.T) {
	e := newIcsEnv(t, nil)
	now := time.Now().UTC()
	at := func(days int) *time.Time { d := now.AddDate(0, 0, days); return &d }

	e.addEntry(t, e.gid, "Old", "completed 400d ago", nil, at(-400))         // outside the 365-day window
	e.addEntry(t, e.gid, "Recent", "completed 10d ago", nil, at(-10))        // in
	e.addEntry(t, e.gid, "Overdue", "scheduled 30d ago", at(-30), nil)       // not completed, so kept
	e.addEntry(t, e.gid, "Soon", "scheduled in 1y", at(365), nil)            // in
	e.addEntry(t, e.gid, "Far", "scheduled in 3y", at(3*365), nil)           // beyond 2 years
	e.addEntry(t, e.gid, "OldDone", "scheduled then done", at(-500), at(-5)) // completion 5d ago: in

	path, _ := e.feedPath(t)
	got := summaries(e.get(path, nil).Body.String())
	assert.ElementsMatch(t, []string{
		"✅ Recent: completed 10d ago",
		"🔧 Overdue: scheduled 30d ago",
		"🔧 Soon: scheduled in 1y",
		"✅ OldDone: scheduled then done",
	}, got)
}

func TestCustomIcsFeedCapsEventsNewestFirst(t *testing.T) {
	e := newIcsEnv(t, nil, func(d *xapi.Deps) { d.IcsFeedLimit = 3 })
	now := time.Now().UTC()
	for i := 1; i <= 6; i++ { // i days ago, so "d" is the newest
		d := now.AddDate(0, 0, -i)
		e.addEntry(t, e.gid, "Item", strings.Repeat("d", i), nil, &d)
	}
	path, _ := e.feedPath(t)
	got := summaries(e.get(path, nil).Body.String())
	// The three newest are kept; the calendar itself is written in chronological order.
	assert.Equal(t, []string{"✅ Item: ddd", "✅ Item: dd", "✅ Item: d"}, got)
}

func TestCustomIcsFeedETag(t *testing.T) {
	e := newIcsEnv(t, nil)
	sched := time.Now().UTC().AddDate(0, 0, 20)
	e.addEntry(t, e.gid, "Furnace", "filter", &sched, nil)
	path, _ := e.feedPath(t)

	first := e.get(path, nil)
	require.Equal(t, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.True(t, strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`))

	// Stable across fetches even though DTSTAMP moves on.
	time.Sleep(1100 * time.Millisecond)
	assert.Equal(t, etag, e.get(path, nil).Header().Get("ETag"))

	for _, inm := range []string{etag, "W/" + etag, `"nope", ` + etag, "*"} {
		rec := e.get(path, map[string]string{"If-None-Match": inm})
		assert.Equal(t, http.StatusNotModified, rec.Code, inm)
		assert.Empty(t, rec.Body.String(), inm)
		assert.Equal(t, etag, rec.Header().Get("ETag"), inm)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), inm)
	}
	assert.Equal(t, http.StatusOK, e.get(path, map[string]string{"If-None-Match": `"stale"`}).Code)

	// A change to the data changes the ETag, so the old one no longer matches.
	other := time.Now().UTC().AddDate(0, 0, 30)
	e.addEntry(t, e.gid, "Boiler", "flush", &other, nil)
	rec := e.get(path, map[string]string{"If-None-Match": etag})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEqual(t, etag, rec.Header().Get("ETag"))
	assert.Contains(t, rec.Body.String(), "Boiler")
}

// A token whose group no longer exists stops working, and its row is removed.
func TestCustomIcsFeedOrphanedGroup(t *testing.T) {
	e := newIcsEnv(t, nil)
	ctx := context.Background()
	other, err := e.a.repos.Groups.GroupCreate(ctx, "doomed", uuid.Nil)
	require.NoError(t, err)
	store := ics.NewStore(e.deps.Ent)
	tok, err := store.Issue(ctx, other.ID, uuid.New())
	require.NoError(t, err)
	path := "/api/v1/x/calendar/" + tok + ".ics"
	require.Equal(t, http.StatusOK, e.get(path, nil).Code)

	_, err = e.a.repos.Groups.GroupDelete(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, e.get(path, nil).Code)
	st, err := store.Status(ctx, other.ID)
	require.NoError(t, err)
	assert.False(t, st.Exists, "the orphaned token row is cleaned up on first use")
}

func TestCustomIcsFeedNoStoreHeadersEverywhere(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e := newIcsEnv(t, ics.NewLimiter(2, time.Minute, func() time.Time { return now }))
	check := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), name)
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), name)
		assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"), name)
	}

	issue := e.do(http.MethodPost, "/api/v1/x/calendar/feed", true) // contains the secret URL
	require.Equal(t, http.StatusCreated, issue.Code)
	check("issue", issue)
	check("status", e.do(http.MethodGet, "/api/v1/x/calendar/feed", true))

	body := decodeBody[struct {
		URL string `json:"url"`
	}](t, issue)
	path := strings.TrimPrefix(body.URL, gcalTestBaseURL)

	ok := e.get(path, nil)
	require.Equal(t, http.StatusOK, ok.Code)
	check("200", ok)
	nf := e.get("/api/v1/x/calendar/"+strings.Repeat("Q", 43)+".ics", nil)
	require.Equal(t, http.StatusNotFound, nf.Code)
	check("404", nf)
	limited := e.get(path, nil)
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	check("429", limited)
}
