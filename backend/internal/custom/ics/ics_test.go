package ics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

// unfold reverses RFC 5545 line folding and splits into content lines.
func unfold(t *testing.T, b []byte) []string {
	t.Helper()
	s := string(b)
	require.True(t, strings.HasSuffix(s, "\r\n"))
	assert.NotContains(t, strings.ReplaceAll(s, "\r\n", ""), "\n", "bare LF in output")
	assert.NotContains(t, strings.ReplaceAll(s, "\r\n", ""), "\r", "bare CR in output")
	for _, phys := range strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n") {
		assert.LessOrEqual(t, len(phys), 75, "physical line too long: %q", phys)
	}
	s = strings.ReplaceAll(s, "\r\n ", "")
	return strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n")
}

func TestXIcsEscapeText(t *testing.T) {
	assert.Equal(t, `a\\b\;c\,d\ne`, ics.EscapeText("a\\b;c,d\ne"))
	assert.Equal(t, `one\ntwo\nthree`, ics.EscapeText("one\r\ntwo\rthree"))
	assert.Equal(t, "tab\there", ics.EscapeText("tab\there"))
	assert.Equal(t, "ab", ics.EscapeText("a\x00\x07b"), "control characters are dropped")
	assert.Equal(t, "🔧 ünï", ics.EscapeText("🔧 ünï"))
}

func TestXIcsRenderStructure(t *testing.T) {
	now := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []ics.Event{
		{UID: "b@homebox", Start: day(2027, 5, 2), End: day(2027, 5, 3), Summary: "🔧 Furnace: filter, big; \\ done", Description: "line1\nline2", ReminderMinutes: 1440},
		{UID: "a@homebox", Start: day(2027, 5, 2), End: day(2027, 5, 3), Summary: "✅ Boiler: flush"},
		{UID: "c@homebox", Start: day(2027, 1, 9), End: day(2027, 1, 10), Summary: "first"},
	}
	lines := unfold(t, ics.Render("Homebox Maintenance", events, now))

	assert.Equal(t, "BEGIN:VCALENDAR", lines[0])
	assert.Equal(t, "END:VCALENDAR", lines[len(lines)-1])
	assert.Contains(t, lines, "VERSION:2.0")
	assert.Contains(t, lines, "X-WR-CALNAME:Homebox Maintenance")
	assert.Contains(t, lines, "DTSTAMP:20270102T030405Z")

	// Sorted by start date, then UID.
	var uids []string
	for _, l := range lines {
		if strings.HasPrefix(l, "UID:") {
			uids = append(uids, l)
		}
	}
	assert.Equal(t, []string{"UID:c@homebox", "UID:a@homebox", "UID:b@homebox"}, uids)

	assert.Contains(t, lines, "DTSTART;VALUE=DATE:20270502")
	assert.Contains(t, lines, "DTEND;VALUE=DATE:20270503")
	assert.Contains(t, lines, `SUMMARY:🔧 Furnace: filter\, big\; \\ done`)
	assert.Contains(t, lines, `DESCRIPTION:line1\nline2`)
	assert.Contains(t, lines, "TRIGGER:-PT1440M")
	assert.Equal(t, 1, count(lines, "BEGIN:VALARM"), "only the reminder event has an alarm")
	assert.Equal(t, 3, count(lines, "BEGIN:VEVENT"))
	assert.Equal(t, 3, count(lines, "END:VEVENT"))
}

func TestXIcsRenderEmpty(t *testing.T) {
	lines := unfold(t, ics.Render("x", nil, time.Now()))
	assert.Equal(t, 0, count(lines, "BEGIN:VEVENT"))
	assert.Equal(t, "END:VCALENDAR", lines[len(lines)-1])
}

func TestXIcsFolding(t *testing.T) {
	long := strings.Repeat("abcdefghij", 40) // 400 octets
	multi := strings.Repeat("🔧é", 60)        // multi-byte runes straddle fold points
	for _, summary := range []string{long, multi, strings.Repeat("x", 62), strings.Repeat("y", 63)} {
		out := ics.Render("n", []ics.Event{{UID: "u", Start: day(2027, 1, 1), End: day(2027, 1, 2), Summary: summary}}, time.Now())
		for _, phys := range strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n") {
			assert.LessOrEqual(t, len(phys), 75)
			assert.True(t, utf8Valid(phys), "fold split a UTF-8 sequence: %q", phys)
		}
		lines := unfold(t, out)
		assert.Contains(t, lines, "SUMMARY:"+ics.EscapeText(summary), "unfolding restores the original")
	}
}

func TestXIcsFromDesired(t *testing.T) {
	id := uuid.New()
	d := gcal.Desired{
		SourceType: gcal.SourceMaintenance, SourceID: id,
		Event: gcal.Event{
			Summary: "🔧 A: b", Description: "d",
			Start:     &gcal.EventDate{Date: "2027-03-14"},
			End:       &gcal.EventDate{Date: "2027-03-15"},
			Reminders: &gcal.Reminders{Overrides: []gcal.ReminderOverride{{Method: "popup", Minutes: 90}}},
		},
	}
	ev, ok := ics.FromDesired(d)
	require.True(t, ok)
	assert.Equal(t, "maintenance-"+id.String()+"@homebox", ev.UID, "UID is stable for a source")
	assert.Equal(t, day(2027, 3, 14), ev.Start)
	assert.Equal(t, day(2027, 3, 15), ev.End)
	assert.Equal(t, 90, ev.ReminderMinutes)

	d.Event.Start = nil
	_, ok = ics.FromDesired(d)
	assert.False(t, ok)
	d.Event.Start = &gcal.EventDate{Date: "garbage"}
	_, ok = ics.FromDesired(d)
	assert.False(t, ok)
	d.Event.Start = &gcal.EventDate{Date: "2027-03-14"}
	d.Event.End = &gcal.EventDate{Date: "2027-03-14"}
	_, ok = ics.FromDesired(d)
	assert.False(t, ok, "end must be after start")
}

func TestXIcsLimiter(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	l := ics.NewLimiter(3, time.Minute, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		assert.True(t, l.Allow("1.1.1.1"), "request %d", i)
	}
	assert.False(t, l.Allow("1.1.1.1"))
	assert.True(t, l.Allow("2.2.2.2"), "keys are independent")

	now = now.Add(time.Minute)
	assert.True(t, l.Allow("1.1.1.1"), "window reset")
}

func TestXIcsClientIP(t *testing.T) {
	req := func(remote, cf string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		return r
	}
	assert.Equal(t, "8.8.8.8", ics.ClientIP(req("8.8.8.8:1234", "")))
	assert.Equal(t, "8.8.8.8", ics.ClientIP(req("8.8.8.8:1234", "9.9.9.9")), "public peers can't choose their bucket")
	assert.Equal(t, "9.9.9.9", ics.ClientIP(req("172.18.0.2:1234", "9.9.9.9")), "private peer = our tunnel connector")
	assert.Equal(t, "172.18.0.2", ics.ClientIP(req("172.18.0.2:1234", "not-an-ip")))
	assert.Equal(t, "127.0.0.1", ics.ClientIP(req("127.0.0.1:80", "")))
}

func TestXIcsStore(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	s := ics.NewStore(xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL))))

	gA, gB, user := uuid.New(), uuid.New(), uuid.New()

	st, err := s.Status(ctx, gA)
	require.NoError(t, err)
	assert.False(t, st.Exists)

	tokA, err := s.Issue(ctx, gA, user)
	require.NoError(t, err)
	assert.Len(t, tokA, 43, "256 bits, base64url")
	tokB, err := s.Issue(ctx, gB, user)
	require.NoError(t, err)
	assert.NotEqual(t, tokA, tokB)

	st, err = s.Status(ctx, gA)
	require.NoError(t, err)
	assert.True(t, st.Exists)
	assert.NotNil(t, st.CreatedAt)
	assert.Nil(t, st.RotatedAt)

	// The token is stored hashed, never in the clear.
	var stored string
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT token_hash FROM x_ics_feeds WHERE token_hash <> ''  ORDER BY created_at LIMIT 1").Scan(&stored))
	assert.NotContains(t, []string{tokA, tokB}, stored)
	assert.Len(t, stored, 64)

	// Lookup is per token, so groups are isolated.
	gid, ok, err := s.Lookup(ctx, tokA)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, gA, gid)
	gid, ok, err = s.Lookup(ctx, tokB)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, gB, gid)
	for _, bad := range []string{"", "nope", strings.Repeat("a", 5000), tokA + "x", tokA[:42]} {
		gid, ok, err = s.Lookup(ctx, bad)
		require.NoError(t, err)
		assert.False(t, ok, "token %.20q", bad)
		assert.Equal(t, uuid.Nil, gid)
	}

	// Rotate invalidates the old token and keeps the group's single row.
	tokA2, err := s.Issue(ctx, gA, user)
	require.NoError(t, err)
	assert.NotEqual(t, tokA, tokA2)
	_, ok, err = s.Lookup(ctx, tokA)
	require.NoError(t, err)
	assert.False(t, ok, "old token no longer works")
	gid, ok, err = s.Lookup(ctx, tokA2)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, gA, gid)
	st, err = s.Status(ctx, gA)
	require.NoError(t, err)
	assert.NotNil(t, st.RotatedAt)
	var rows int
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM x_ics_feeds").Scan(&rows))
	assert.Equal(t, 2, rows)

	// Revoke affects only that group, and is idempotent.
	require.NoError(t, s.Revoke(ctx, gA))
	require.NoError(t, s.Revoke(ctx, gA))
	_, ok, err = s.Lookup(ctx, tokA2)
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = s.Lookup(ctx, tokB)
	require.NoError(t, err)
	assert.True(t, ok, "other group unaffected")
	st, err = s.Status(ctx, gA)
	require.NoError(t, err)
	assert.False(t, st.Exists)
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func count(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if l == want {
			n++
		}
	}
	return n
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
