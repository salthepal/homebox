package main

// Fork-only entry point for custom features (.fork/PLAN.md §4). Called once from run() in
// main.go — that call is the only backend seam. Everything custom is wired up here.

import (
	"context"
	"fmt"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-chi/chi/v5"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/graceful"
	"github.com/rs/zerolog/log"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	xmigrations "github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/authroles"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

// registerCustom runs custom migrations, opens the custom ent client on upstream's
// *sql.DB, mounts /api/v1/x and registers custom background tasks.
func registerCustom(a *app, cfg *config.Config, router *chi.Mux, chain *errchain.ErrChain, runner *graceful.Runner) error {
	xcfg, err := xconfig.Load()
	if err != nil {
		return fmt.Errorf("custom config: %w", err)
	}

	driver := strings.ToLower(cfg.Database.Driver)
	_, dialect, err := resolveDriver(driver)
	if err != nil {
		return err
	}

	db := a.db.Sql()
	applied, err := xmigrations.Run(context.Background(), db, driver)
	if err != nil {
		return err
	}
	log.Info().Ints64("applied", applied).Msg("custom migrations up to date")

	xc := xent.NewClient(xent.Driver(entsql.OpenDB(dialect, db)))
	deps := xapi.Deps{Ent: xc, Config: xcfg}
	registerCustomGcal(a, xcfg, xc, runner, &deps)
	mountCustomRoutes(a, router, chain, deps)

	if missing := xcfg.GcalMissing(); len(missing) > 0 {
		log.Info().Strs("missing", missing).Msg("google calendar sync disabled")
	}

	_ = runner // custom background tasks are registered here in later tasks
	return nil
}

// mountCustomRoutes mounts /api/v1/x with upstream's authenticated-user middleware.
func mountCustomRoutes(a *app, router chi.Router, chain *errchain.ErrChain, deps xapi.Deps) {
	userMW := []errchain.Middleware{
		a.mwAuthToken,
		a.mwTenant,
		a.mwRoles(RoleModeOr, authroles.RoleUser.String()),
	}
	router.Route(prefix+"/v1/x", func(r chi.Router) {
		xapi.Mount(r, chain, userMW, deps)
	})
}
