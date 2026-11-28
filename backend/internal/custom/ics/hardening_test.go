package ics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

func TestXIcsLimiterCapsKeys(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	l := ics.NewLimiter(5, time.Minute, func() time.Time { return now })
	l.SetMaxKeys(3)

	for i := 0; i < 3; i++ {
		assert.True(t, l.Allow(fmt.Sprintf("k%d", i)))
	}
	assert.Equal(t, 3, l.Len())

	// Full of live buckets: a new key is refused and nothing is evicted.
	assert.False(t, l.Allow("new"))
	assert.Equal(t, 3, l.Len(), "the map never grows past the cap")
	assert.True(t, l.Allow("k0"), "tracked clients keep working")

	// Once the window passes, expired buckets are reclaimed and new keys fit again.
	now = now.Add(time.Minute)
	assert.True(t, l.Allow("new"))
	assert.LessOrEqual(t, l.Len(), 3)
}

func TestXIcsClientIPNormalisation(t *testing.T) {
	req := func(remote, cf string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		return r
	}
	// IPv4-mapped IPv6 is the same client as plain IPv4.
	assert.Equal(t, "8.8.8.8", ics.ClientIP(req("[::ffff:8.8.8.8]:1", "")))
	assert.Equal(t, ics.ClientIP(req("8.8.8.8:1", "")), ics.ClientIP(req("[::ffff:8.8.8.8]:2", "")))
	assert.Equal(t, "9.9.9.9", ics.ClientIP(req("172.18.0.2:1", "::ffff:9.9.9.9")))

	// IPv6 is keyed by /64: addresses in one subnet share a bucket, other subnets don't.
	a := ics.ClientIP(req("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1", ""))
	b := ics.ClientIP(req("[2001:db8:1:2:1111:2222:3333:4444]:1", ""))
	c := ics.ClientIP(req("[2001:db8:1:3::1]:1", ""))
	assert.Equal(t, "2001:db8:1:2::/64", a)
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	// Same through the trusted header, and zones are dropped.
	assert.Equal(t, a, ics.ClientIP(req("10.0.0.5:1", "2001:db8:1:2:5::9")))
	assert.Equal(t, "fe80::/64", ics.ClientIP(req("[fe80::1%eth0]:1", "")))

	// A public peer still can't pick its own bucket via the header.
	assert.Equal(t, "8.8.8.8", ics.ClientIP(req("8.8.8.8:1", "1.2.3.4")))
}

func TestXIcsStoreGroupHelpers(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	s := ics.NewStore(xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL))))

	g1, g2, g3, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var tok [3]string
	for i, g := range []uuid.UUID{g1, g2, g3} {
		tok[i], err = s.Issue(ctx, g, user)
		require.NoError(t, err)
	}

	ids, err := s.GroupIDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{g1, g2, g3}, ids)

	n, err := s.DeleteForGroups(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, n)

	n, err = s.DeleteForGroups(ctx, []uuid.UUID{g1, g3, uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	_, ok, err := s.Lookup(ctx, tok[0])
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = s.Lookup(ctx, tok[1])
	require.NoError(t, err)
	assert.True(t, ok, "other groups are untouched")
}
