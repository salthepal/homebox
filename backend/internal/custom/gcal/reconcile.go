package gcal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

// Enabled reports whether the Google settings are all configured.
func (s *Service) Enabled() bool { return s.cfg.GcalEnabled() }

// MaintenanceLister is the read side of upstream maintenance entries
// (upstream.MaintenanceReader implements it).
type MaintenanceLister interface {
	ListForGroup(ctx context.Context, gid uuid.UUID) ([]upstream.MaintenanceItem, error)
}

// MaintenanceProvider turns a group's maintenance entries into desired calendar events.
type MaintenanceProvider struct {
	Source  MaintenanceLister
	BaseURL string // HBOX_X_PUBLIC_BASE_URL, for deep links
}

// SourceType implements SourceProvider.
func (MaintenanceProvider) SourceType() string { return SourceMaintenance }

// Desired implements SourceProvider.
func (p MaintenanceProvider) Desired(ctx context.Context, gid uuid.UUID, conn Connection) ([]Desired, error) {
	items, err := p.Source.ListForGroup(ctx, gid)
	if err != nil {
		return nil, err
	}
	out := make([]Desired, 0, len(items))
	for _, it := range items {
		if d, ok := MaintenanceDesired(it, conn, p.BaseURL); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// Result counts what one reconcile did.
type Result struct {
	Inserted  int `json:"inserted"`
	Patched   int `json:"patched"`
	Deleted   int `json:"deleted"`
	Relinked  int `json:"relinked"`
	Unchanged int `json:"unchanged"`
}

// ReconcilerOptions tunes the reconciler. The zero value is production behavior.
type ReconcilerOptions struct {
	// Debounce is how long a group's changes are collected before it is reconciled
	// (default 10s).
	Debounce time.Duration
	// Now overrides the clock used for sync timestamps.
	Now func() time.Time
}

// Reconciler keeps each group's Google calendar in line with Homebox (PLAN §7). It is safe
// for concurrent use; reconciles of one group never overlap.
type Reconciler struct {
	svc       *Service
	providers []SourceProvider
	opt       ReconcilerOptions

	queue chan uuid.UUID
	locks sync.Map // uuid.UUID -> *sync.Mutex
}

// NewReconciler builds a reconciler over the given source providers.
func NewReconciler(svc *Service, opt ReconcilerOptions, providers ...SourceProvider) *Reconciler {
	if opt.Debounce <= 0 {
		opt.Debounce = 10 * time.Second
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Reconciler{svc: svc, providers: providers, opt: opt, queue: make(chan uuid.UUID, 256)}
}

func (r *Reconciler) lock(gid uuid.UUID) *sync.Mutex {
	m, _ := r.locks.LoadOrStore(gid, &sync.Mutex{})
	mu, _ := m.(*sync.Mutex)
	return mu
}

type linkKey struct {
	typ string
	id  uuid.UUID
}

// Reconcile brings the group's calendar in line with its sources.
//
// Safety on the shared calendar (hard requirement): events are only patched or deleted when
// their ID is in x_gcal_links AND the event, as listed from the calendar, carries the
// Homebox owner marker. Foreign events are never touched, and the calendar is never cleared.
//
// It returns ErrNotConnected (no connection), *NotConfiguredError (Google not set up),
// ErrBroken (token dead, connection flagged), or ErrCalendarNotSet. The outcome is recorded
// on the connection (last sync / last error) for the settings page.
func (r *Reconciler) Reconcile(ctx context.Context, gid uuid.UUID) (res Result, err error) {
	mu := r.lock(gid)
	mu.Lock()
	defer mu.Unlock()

	client, conn, err := r.svc.Client(ctx, gid)
	if err != nil {
		return res, err
	}
	if conn.CalendarID == "" {
		return res, ErrCalendarNotSet
	}
	defer func() {
		msg := ""
		if err != nil {
			msg = err.Error()
			if errors.Is(err, ErrBroken) {
				msg = "Google rejected the saved credentials; reconnect Google Calendar"
			}
		}
		// Not recorded for a connection removed mid-run; nothing to update then.
		if rerr := r.svc.store.RecordSync(context.WithoutCancel(ctx), gid, r.opt.Now(), msg); rerr != nil && !errors.Is(rerr, ErrNotConnected) {
			log.Err(rerr).Str("group", gid.String()).Msg("recording google calendar sync result failed")
		}
	}()

	err = r.run(ctx, gid, client, conn, &res)
	return res, err
}

func (r *Reconciler) run(ctx context.Context, gid uuid.UUID, c *Client, conn Connection, res *Result) error {
	now := r.opt.Now()

	// 1. Desired set, from every provider.
	desired := map[linkKey]Desired{}
	managed := map[string]struct{}{}
	for _, p := range r.providers {
		managed[p.SourceType()] = struct{}{}
		ds, err := p.Desired(ctx, gid, conn)
		if err != nil {
			return fmt.Errorf("reading %s sources: %w", p.SourceType(), err)
		}
		for _, d := range ds {
			desired[linkKey{d.SourceType, d.SourceID}] = d
		}
	}

	// 2. Current links, 3. what is on the calendar that is ours.
	linkList, err := r.svc.store.ListLinks(ctx, gid)
	if err != nil {
		return err
	}
	links := make(map[linkKey]Link, len(linkList))
	for _, l := range linkList {
		links[linkKey{l.SourceType, l.SourceID}] = l
	}

	remoteList, err := c.ListEvents(ctx, conn.CalendarID, ListEventsParams{
		PrivateExtendedProperty: OwnerProperty + "=" + OwnerValue,
	})
	if err != nil {
		return r.svc.ClassifyError(ctx, gid, err)
	}
	remote := make(map[string]Event, len(remoteList))
	byKey := map[linkKey]Event{}
	sort.Slice(remoteList, func(i, j int) bool { return remoteList[i].ID < remoteList[j].ID })
	for _, ev := range remoteList {
		if !ev.IsOurs() {
			continue // the server filter should guarantee this; never trust it alone
		}
		remote[ev.ID] = ev
		if k, ok := keyOf(ev); ok {
			if _, dup := byKey[k]; !dup {
				byKey[k] = ev
			}
		}
	}

	// 4. Rebuild links from the calendar: an owned event of a managed type with no usable
	// link is re-linked by its homebox_id instead of being duplicated. Hash "" forces one
	// patch so the event content is checked against the source.
	for k, ev := range byKey {
		if _, managedType := managed[k.typ]; !managedType {
			continue
		}
		l, linked := links[k]
		if linked {
			if _, present := remote[l.EventID]; present {
				continue
			}
		}
		if err := r.svc.store.UpsertLink(ctx, gid, k.typ, k.id, ev.ID, "", now); err != nil {
			return err
		}
		links[k] = Link{GroupID: gid, SourceType: k.typ, SourceID: k.id, EventID: ev.ID}
		res.Relinked++
	}

	var firstErr error
	// soft records a per-event failure so one bad entry doesn't block the rest. Credential
	// failures and cancellation stop the run.
	soft := func(err error) error {
		err = r.svc.ClassifyError(ctx, gid, err)
		if errors.Is(err, ErrBroken) || ctx.Err() != nil {
			return err
		}
		if firstErr == nil {
			firstErr = err
		}
		return nil
	}

	// 5. Inserts and patches, in a stable order.
	keys := make([]linkKey, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].typ != keys[j].typ {
			return keys[i].typ < keys[j].typ
		}
		return keys[i].id.String() < keys[j].id.String()
	})
	for _, k := range keys {
		d := desired[k]
		l, linked := links[k]
		_, present := remote[l.EventID]

		insert := func() error {
			created, err := c.InsertEvent(ctx, conn.CalendarID, d.Event)
			if err != nil {
				return err
			}
			res.Inserted++
			return r.svc.store.UpsertLink(ctx, gid, k.typ, k.id, created.ID, d.Hash, now)
		}

		var err error
		switch {
		case !linked || !present:
			err = insert()
		case l.ContentHash == d.Hash:
			res.Unchanged++
		default:
			if _, perr := c.PatchEvent(ctx, conn.CalendarID, l.EventID, d.Event); perr == nil {
				res.Patched++
				err = r.svc.store.UpsertLink(ctx, gid, k.typ, k.id, l.EventID, d.Hash, now)
			} else if IsNotFound(perr) {
				err = insert() // deleted on the calendar since we listed: put it back
			} else {
				err = perr
			}
		}
		if err != nil {
			if stop := soft(err); stop != nil {
				return stop
			}
		}
	}

	// 6. Links whose source is gone (or no longer wanted): delete the event, but only an
	// event that is on the calendar and carries our marker.
	for k, l := range links {
		if _, managedType := managed[k.typ]; !managedType {
			continue
		}
		if _, wanted := desired[k]; wanted {
			continue
		}
		if _, ours := remote[l.EventID]; ours {
			if err := c.DeleteEvent(ctx, conn.CalendarID, l.EventID); err != nil && !IsNotFound(err) {
				if stop := soft(err); stop != nil {
					return stop
				}
				continue // keep the link so the next run retries
			}
			res.Deleted++
		}
		if err := r.svc.store.DeleteLink(ctx, gid, k.typ, k.id); err != nil {
			return err
		}
	}
	return firstErr
}

// keyOf reads the source a Homebox event was made from out of its private properties.
func keyOf(ev Event) (linkKey, bool) {
	if ev.ExtendedProperties == nil {
		return linkKey{}, false
	}
	p := ev.ExtendedProperties.Private
	id, err := uuid.Parse(p[IDProperty])
	if err != nil || p[TypeProperty] == "" {
		return linkKey{}, false
	}
	return linkKey{p[TypeProperty], id}, true
}

// ReconcileAll reconciles every group that has a usable connection with a calendar chosen.
// Failures are logged and recorded per group; one group failing doesn't stop the others.
func (r *Reconciler) ReconcileAll(ctx context.Context) {
	if r == nil || !r.svc.Enabled() {
		return
	}
	conns, err := r.svc.store.ListConnections(ctx)
	if err != nil {
		log.Err(err).Msg("listing google calendar connections failed")
		return
	}
	for _, c := range conns {
		if c.Broken || c.CalendarID == "" {
			continue
		}
		r.runOne(ctx, c.GroupID)
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *Reconciler) runOne(ctx context.Context, gid uuid.UUID) {
	res, err := r.Reconcile(ctx, gid)
	switch {
	case err == nil:
		if res.Inserted+res.Patched+res.Deleted+res.Relinked > 0 {
			log.Info().Str("group", gid.String()).Interface("result", res).Msg("google calendar synced")
		}
	case errors.Is(err, ErrNotConnected), errors.Is(err, ErrCalendarNotSet):
		// nothing to do
	default:
		log.Warn().Err(err).Str("group", gid.String()).Msg("google calendar sync failed")
	}
}

// Enqueue asks for a debounced reconcile of the group. It is safe for concurrent use, never
// blocks, and does nothing when Google Calendar isn't configured. If the queue is full the
// request is dropped; the periodic ReconcileAll picks the change up.
func (r *Reconciler) Enqueue(gid uuid.UUID) {
	if r == nil || !r.svc.Enabled() {
		return
	}
	select {
	case r.queue <- gid:
	default:
		log.Warn().Str("group", gid.String()).Msg("google calendar sync queue full; relying on the periodic sync")
	}
}

// Run is the single goroutine that drains the queue: a group is reconciled once Debounce has
// passed since its last request. It returns when ctx is cancelled (register it with the
// graceful runner so it stops on shutdown).
func (r *Reconciler) Run(ctx context.Context) error {
	pending := map[uuid.UUID]time.Time{}
	for {
		var next time.Time
		for _, due := range pending {
			if next.IsZero() || due.Before(next) {
				next = due
			}
		}
		var timer *time.Timer
		var fire <-chan time.Time
		if !next.IsZero() {
			timer = time.NewTimer(max(0, time.Until(next)))
			fire = timer.C
		}

		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case gid := <-r.queue:
			pending[gid] = time.Now().Add(r.opt.Debounce)
		case <-fire:
			now := time.Now()
			for gid, due := range pending {
				if !due.After(now) {
					delete(pending, gid)
					r.runOne(ctx, gid)
				}
			}
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
