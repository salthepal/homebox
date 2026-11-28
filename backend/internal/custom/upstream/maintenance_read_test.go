package upstream_test

import (
	"context"
	"sync"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
)

// notifications records the group IDs the hook reported.
type notifications struct {
	mu   sync.Mutex
	gids []uuid.UUID
}

func (n *notifications) add(g uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gids = append(n.gids, g)
}

func (n *notifications) snapshot() []uuid.UUID {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]uuid.UUID(nil), n.gids...)
}

type hookEnv struct {
	client *ent.Client
	gid    uuid.UUID
	item   uuid.UUID
	got    *notifications
}

// newHookEnv builds a real upstream schema with one group and one item, and registers the
// hook under test.
func newHookEnv(t *testing.T) hookEnv {
	t.Helper()
	ctx := context.Background()
	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))

	g, err := client.Group.Create().SetName("hook-test").Save(ctx)
	require.NoError(t, err)
	et, err := client.EntityType.Create().SetName("Item").SetIsLocation(false).SetGroupID(g.ID).Save(ctx)
	require.NoError(t, err)
	it, err := client.Entity.Create().SetName("Water heater").SetGroupID(g.ID).SetEntityTypeID(et.ID).Save(ctx)
	require.NoError(t, err)

	got := &notifications{}
	upstream.OnMaintenanceChange(client, got.add)
	return hookEnv{client: client, gid: g.ID, item: it.ID, got: got}
}

func (h hookEnv) create(c *ent.MaintenanceEntryClient) (*ent.MaintenanceEntry, error) {
	return c.Create().SetEntityID(h.item).SetName("Flush tank").
		SetScheduledDate(time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)).Save(context.Background())
}

func TestXOnMaintenanceChangeNotifiesImmediatelyOutsideTransactions(t *testing.T) {
	h := newHookEnv(t)
	ctx := context.Background()

	e, err := h.create(h.client.MaintenanceEntry)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{h.gid}, h.got.snapshot(), "create")

	require.NoError(t, h.client.MaintenanceEntry.UpdateOneID(e.ID).SetName("renamed").Exec(ctx))
	assert.Len(t, h.got.snapshot(), 2, "update")

	_, err = h.client.MaintenanceEntry.Delete().Where().Exec(ctx)
	require.NoError(t, err)
	assert.Len(t, h.got.snapshot(), 3, "bulk delete resolves the group before the rows vanish")
	for _, g := range h.got.snapshot() {
		assert.Equal(t, h.gid, g)
	}
}

func TestXOnMaintenanceChangeDefersToCommit(t *testing.T) {
	h := newHookEnv(t)
	ctx := context.Background()

	tx, err := h.client.Tx(ctx)
	require.NoError(t, err)
	e, err := h.create(tx.MaintenanceEntry)
	require.NoError(t, err)
	require.NoError(t, tx.MaintenanceEntry.UpdateOneID(e.ID).SetName("renamed").Exec(ctx))
	assert.Empty(t, h.got.snapshot(), "nothing is reported while the transaction is open")

	require.NoError(t, tx.Commit())
	got := h.got.snapshot()
	assert.NotEmpty(t, got, "reported after commit")
	for _, g := range got {
		assert.Equal(t, h.gid, g)
	}

	// The committed row is what a reconcile would now read.
	items, err := upstream.NewMaintenanceReader(h.client).ListForGroup(ctx, h.gid)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "renamed", items[0].Name)
}

func TestXOnMaintenanceChangeSkipsRolledBackTransactions(t *testing.T) {
	h := newHookEnv(t)
	ctx := context.Background()

	tx, err := h.client.Tx(ctx)
	require.NoError(t, err)
	_, err = h.create(tx.MaintenanceEntry)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	assert.Empty(t, h.got.snapshot(), "a rolled-back change must not trigger a sync")
	items, err := upstream.NewMaintenanceReader(h.client).ListForGroup(ctx, h.gid)
	require.NoError(t, err)
	assert.Empty(t, items)
}
