// Package api holds the HTTP handlers for the fork's custom features, mounted under
// /api/v1/x by backend/app/api/x_register.go. Handlers live outside upstream's swag scan
// directories so they never touch the committed swagger output (.fork/PLAN.md R7).
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/projects"
)

// Deps are the dependencies shared by custom handlers.
type Deps struct {
	Ent    *xent.Client
	Config xconfig.Config
	// Gcal overrides Google endpoints (tests point these at a fake); zero = real Google.
	Gcal gcal.Options

	Projects *projects.Service // Feature 1 (P2)
	// GcalReconciler serves "Sync now" (POST /gcal/sync); nil means sync isn't wired.
	GcalReconciler *gcal.Reconciler

	// IcsFeed reads upstream maintenance entries (bounded) for the ICS feed.
	IcsFeed IcsFeedSource
	// IcsGroups checks group ownership and existence for the ICS feed.
	IcsGroups IcsGroups
	// IcsLimiter overrides the feed's per-IP rate limiter (tests); nil = default.
	IcsLimiter *ics.Limiter
	// IcsFeedLimit overrides the feed's event cap (tests); 0 = default (5000).
	IcsFeedLimit int
}

// Mount registers the custom routes on r. userMW is upstream's authenticated-user
// middleware stack (auth token, tenant, user role).
func Mount(r chi.Router, chain *errchain.ErrChain, userMW []errchain.Middleware, d Deps) {
	r.Get("/ping", chain.ToHandlerFunc(handlePing(d), userMW...))
	mountGcal(r, chain, userMW, d)
	mountProjects(r, chain, userMW, d)
	mountICS(r, chain, userMW, d)
}

// PingResponse proves the custom stack end to end: routing, auth, tenant and custom DB.
type PingResponse struct {
	OK      bool      `json:"ok"`
	GroupID uuid.UUID `json:"groupId"`
	Schema  string    `json:"schema"`
}

func handlePing(d Deps) errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		auth := services.NewContext(r.Context())
		meta, err := d.Ent.XMeta.Get(r.Context(), "schema")
		if err != nil {
			return err
		}
		return server.JSON(w, http.StatusOK, PingResponse{OK: true, GroupID: auth.GID, Schema: meta.Value})
	}
}
