package main

// Google Calendar wiring for the fork (.fork/PLAN.md §7). Kept out of x_register.go so that
// file only gains one call.

import (
	"time"

	"github.com/hay-kot/httpkit/graceful"
	"github.com/rs/zerolog/log"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

// newGcalReconciler builds the reconciler over upstream maintenance entries and registers
// the ent hook that enqueues a debounced reconcile whenever one changes. The hook is
// registered even while Google isn't configured: Enqueue is then a no-op.
func newGcalReconciler(a *app, xcfg xconfig.Config, xc *xent.Client, opts gcal.Options, rec gcal.ReconcilerOptions) *gcal.Reconciler {
	svc := gcal.New(xc, xcfg, opts)
	r := gcal.NewReconciler(svc, rec, gcal.MaintenanceProvider{
		Source:  upstream.NewMaintenanceReader(a.db),
		BaseURL: xcfg.PublicBaseURL,
	})
	upstream.OnMaintenanceChange(a.db, r.Enqueue)
	return r
}

// registerCustomGcal wires Google Calendar sync: the reconciler (stored in deps for the
// "Sync now" route), the queue goroutine and the periodic full sync. Both background tasks
// stop with the runner's context on shutdown, and neither starts while Google isn't
// configured.
func registerCustomGcal(a *app, xcfg xconfig.Config, xc *xent.Client, runner *graceful.Runner, deps *xapi.Deps) {
	rec := newGcalReconciler(a, xcfg, xc, deps.Gcal, gcal.ReconcilerOptions{})
	deps.GcalReconciler = rec
	deps.Maintenance = upstream.NewMaintenanceReader(a.db) // also serves the ICS feed
	if !xcfg.GcalEnabled() {
		return
	}

	runner.AddFunc("x-gcal-queue", rec.Run)

	interval := xcfg.GcalSyncInterval
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	runner.AddPlugin(NewTask("x-gcal-sync", interval, rec.ReconcileAll))
	log.Info().Dur("interval", interval).Msg("google calendar sync enabled")
}
