package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

// The daily sweep removes feed tokens of groups that were deleted, using real upstream
// groups, and leaves live groups' tokens alone.
func TestCustomIcsSweepOrphansWithRealGroups(t *testing.T) {
	a, _, kept, deps := newCustomTestApp(t)
	ctx := context.Background()
	store := ics.NewStore(deps.Ent)

	doomed, err := a.repos.Groups.GroupCreate(ctx, "doomed", uuid.Nil)
	require.NoError(t, err)
	keptTok, err := store.Issue(ctx, kept, uuid.New())
	require.NoError(t, err)
	doomedTok, err := store.Issue(ctx, doomed.ID, uuid.New())
	require.NoError(t, err)
	// A row for a group id that never existed is an orphan too.
	ghost := uuid.New()
	ghostTok, err := store.Issue(ctx, ghost, uuid.New())
	require.NoError(t, err)

	require.NoError(t, a.db.Group.DeleteOneID(doomed.ID).Exec(ctx))

	n, err := store.SweepOrphans(ctx, upstream.NewGroups(a.db).ExistingIDs)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, ok, err := store.Lookup(ctx, doomedTok)
	require.NoError(t, err)
	assert.False(t, ok, "deleted group's feed row is removed")
	_, ok, err = store.Lookup(ctx, ghostTok)
	require.NoError(t, err)
	assert.False(t, ok)
	gid, ok, err := store.Lookup(ctx, keptTok)
	require.NoError(t, err)
	assert.True(t, ok, "live group's feed row stays")
	assert.Equal(t, kept, gid)

	// Nothing left to do on a second run.
	n, err = store.SweepOrphans(ctx, upstream.NewGroups(a.db).ExistingIDs)
	require.NoError(t, err)
	assert.Zero(t, n)
}
