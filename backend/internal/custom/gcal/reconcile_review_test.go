package gcal_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
)

const (
	taskFlush     = "Flush tank"
	popupReminder = "popup"
)

// newBareRec is a reconciler without a public base URL, so an item with no description
// and no cost maps to an event with an empty description.
func newBareRec(r recEnv) *gcal.Reconciler {
	return gcal.NewReconciler(r.svc, gcal.ReconcilerOptions{Debounce: 30 * time.Millisecond},
		gcal.MaintenanceProvider{Source: r.lister})
}

// Google's PATCH leaves omitted fields alone, so clearing a field means sending it
// explicitly. The fake has the same merge semantics.
func TestXReconcilePatchClearsDescription(t *testing.T) {
	r := newRecEnv(t, nil)
	rec := newBareRec(r)
	ctx := context.Background()

	m := item(taskFlush, day(2027, 3, 14)) // description "flush the tank", cost 40
	r.lister.set(m)
	_, err := rec.Reconcile(ctx, r.gid)
	require.NoError(t, err)
	require.NotEmpty(t, r.events()[0].Description)

	m.Description, m.Cost = "", 0
	r.lister.set(m)
	res, err := rec.Reconcile(ctx, r.gid)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Patched)
	assert.Empty(t, r.events()[0].Description, "the stale description must be cleared, not left in place")

	// And it is stable: the stored hash now matches what is on the calendar.
	res, err = rec.Reconcile(ctx, r.gid)
	require.NoError(t, err)
	assert.Equal(t, gcal.Result{Unchanged: 1}, res)
}

func TestXReconcilePatchRemovesReminderWhenCompleted(t *testing.T) {
	r := newRecEnv(t, nil)
	m := item(taskFlush, day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	require.Equal(t, []gcal.ReminderOverride{{Method: popupReminder, Minutes: 1440}}, r.events()[0].Reminders.Overrides)

	m.CompletedDate = day(2027, 3, 15)
	r.lister.set(m)
	assert.Equal(t, gcal.Result{Patched: 1}, r.sync(t))
	rem := r.events()[0].Reminders
	require.NotNil(t, rem)
	assert.False(t, rem.UseDefault)
	assert.Empty(t, rem.Overrides, "a completed task must not keep its popup reminder")
}

func TestXReconcilePatchRemovesReminderWhenSettingTurnedOff(t *testing.T) {
	r := newRecEnv(t, nil)
	r.lister.set(item(taskFlush, day(2027, 3, 14)))
	r.sync(t)
	require.Len(t, r.events()[0].Reminders.Overrides, 1)

	off := 0
	require.NoError(t, r.svc.UpdateSettings(context.Background(), r.gid, gcal.SettingsInput{ReminderMinutes: &off}))
	assert.Equal(t, gcal.Result{Patched: 1}, r.sync(t))
	assert.Empty(t, r.events()[0].Reminders.Overrides)
	assert.False(t, r.events()[0].Reminders.UseDefault, "no reminders at all, not Google's defaults")

	on := 60
	require.NoError(t, r.svc.UpdateSettings(context.Background(), r.gid, gcal.SettingsInput{ReminderMinutes: &on}))
	assert.Equal(t, gcal.Result{Patched: 1}, r.sync(t))
	assert.Equal(t, []gcal.ReminderOverride{{Method: popupReminder, Minutes: 60}}, r.events()[0].Reminders.Overrides)
}

// Owned events that are not the linked event for their source (left behind by an interrupted
// run, or a lost links table) are cleaned up, but only events carrying our marker.
func TestXReconcileRemovesDuplicateOwnedEvents(t *testing.T) {
	r := newRecEnv(t, nil)
	ctx := context.Background()
	foreign := r.fake.AddEvent(householdCal, gcal.Event{Summary: "🔧 Water heater: " + taskFlush})

	m := item(taskFlush, day(2027, 3, 14))
	r.lister.set(m)
	r.sync(t)
	var linked gcal.Event
	for _, e := range r.events() {
		if e.ID != foreign {
			linked = e
		}
	}
	require.NotEmpty(t, linked.ID)

	// Two extra owned copies of the same source: one sorts before the linked event, one after.
	before, after := linked, linked
	before.ID, after.ID = "aaa-dup", "zzz-dup"
	r.fake.AddEvent(householdCal, before)
	r.fake.AddEvent(householdCal, after)
	require.Len(t, r.events(), 4)

	res := r.sync(t)
	assert.Equal(t, 2, res.Deleted)
	assert.Equal(t, 1, res.Unchanged)
	ids := []string{}
	for _, e := range r.events() {
		ids = append(ids, e.ID)
	}
	assert.ElementsMatch(t, []string{foreign, linked.ID}, ids, "the linked event and the foreign one remain")
	links, err := r.svc.Store().ListLinks(ctx, r.gid)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, linked.ID, links[0].EventID)

	// Duplicates present when the source itself is deleted: nothing of ours remains.
	extra := linked
	extra.ID = "mmm-dup"
	r.fake.AddEvent(householdCal, extra)
	r.lister.set()
	res = r.sync(t)
	assert.Equal(t, 2, res.Deleted, "the linked event and the duplicate both go")
	ids = ids[:0]
	for _, e := range r.events() {
		ids = append(ids, e.ID)
	}
	assert.Equal(t, []string{foreign}, ids, "a foreign event with the same title is never touched")
}

func TestXReconcileDuplicateCleanupLeavesUnparseableAndUnmanagedAlone(t *testing.T) {
	r := newRecEnv(t, nil)
	// Our marker but no usable source id, and a source type no provider manages: not ours to
	// clean up even though they carry the marker.
	odd := r.fake.AddEvent(householdCal, gcal.Event{ExtendedProperties: &gcal.ExtendedProperties{Private: map[string]string{
		gcal.OwnerProperty: gcal.OwnerValue, gcal.IDProperty: "not-a-uuid", gcal.TypeProperty: gcal.SourceMaintenance,
	}}})
	m := item(taskFlush, day(2027, 3, 14))
	other := r.fake.AddEvent(householdCal, gcal.Event{ExtendedProperties: &gcal.ExtendedProperties{Private: map[string]string{
		gcal.OwnerProperty: gcal.OwnerValue, gcal.IDProperty: m.ID.String(), gcal.TypeProperty: "something_else",
	}}})
	r.lister.set(m)

	res := r.sync(t)
	assert.Equal(t, 1, res.Inserted)
	assert.Zero(t, res.Deleted)
	ids := map[string]bool{}
	for _, e := range r.events() {
		ids[e.ID] = true
	}
	assert.True(t, ids[odd])
	assert.True(t, ids[other])
}
