package ics_test

import (
	"context"
	"sync"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
)

// Concurrent first-time issuance for one group must not fail with a constraint error: every
// caller gets a token, there is exactly one row, and exactly one of the returned tokens (the
// last writer's) is valid afterwards.
func TestXIcsIssueConcurrent(t *testing.T) {
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := migrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)
	s := ics.NewStore(xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL))))

	const (
		groups  = 5
		writers = 12
	)
	for g := 0; g < groups; g++ {
		gid, user := uuid.New(), uuid.New()
		tokens := make([]string, writers)
		errs := make([]error, writers)

		var ready, done sync.WaitGroup
		start := make(chan struct{})
		ready.Add(writers)
		done.Add(writers)
		for i := 0; i < writers; i++ {
			go func() {
				defer done.Done()
				ready.Done()
				<-start // release everyone at once to maximise the race
				tokens[i], errs[i] = s.Issue(ctx, gid, user)
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()

		for i, err := range errs {
			require.NoError(t, err, "writer %d", i)
			require.NotEmpty(t, tokens[i])
		}

		var rows int
		require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM x_ics_feeds WHERE group_id = "+placeholder(d.Driver), gid).Scan(&rows))
		assert.Equal(t, 1, rows, "exactly one row for the group")

		valid := 0
		for _, tok := range tokens {
			got, ok, err := s.Lookup(ctx, tok)
			require.NoError(t, err)
			if ok {
				valid++
				assert.Equal(t, gid, got)
			}
		}
		assert.Equal(t, 1, valid, "exactly one issued token is valid (the last writer's)")
	}

	var total int
	require.NoError(t, d.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM x_ics_feeds").Scan(&total))
	assert.Equal(t, groups, total)
}

func placeholder(driver string) string {
	if driver == "postgres" {
		return "$1"
	}
	return "?"
}
