package migrations_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

func TestXMigrationsAfterUpstream(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)

	applied, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	assert.NotEmpty(t, applied, "first run applies custom migrations")

	// Idempotent: second run applies nothing.
	applied, err = migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	assert.Empty(t, applied)

	var v string
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT value FROM x_meta WHERE key = 'schema'").Scan(&v))
	assert.Equal(t, "custom", v)

	// Both version tables exist and are independent.
	var upstreamMax, customMax int64
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&upstreamMax))
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT MAX(version_id) FROM "+migrations.TableName).Scan(&customMax))
	assert.Greater(t, upstreamMax, int64(20000000000000), "upstream uses timestamp versions")
	assert.Less(t, customMax, int64(100000), "custom uses sequential versions")
}

// Upstream goose must not see our versions as "missing", and our provider must not pick up
// upstream's globally registered Go migrations.
func TestXMigrationsDoNotDisturbUpstream(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)

	// Re-running upstream after ours is a no-op and must not error.
	xtest.UpstreamMigrate(t, d)

	p, err := migrations.Provider(d.SQL, d.Driver)
	require.NoError(t, err)
	for _, s := range p.ListSources() {
		assert.Less(t, s.Version, int64(100000), "unexpected migration source %s", s.Path)
	}
}

func TestXMigrationsUnknownDriver(t *testing.T) {
	_, err := migrations.Provider(nil, "mysql")
	assert.Error(t, err)
}
