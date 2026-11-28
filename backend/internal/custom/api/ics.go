package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
)

// Default feed rate limit: requests per client IP per window.
const (
	icsRateLimit  = 60
	icsRateWindow = time.Minute
)

// feedReminderMinutes is the VALARM lead time written into the feed for scheduled work.
const feedReminderMinutes = 1440

// mountICS registers the read-only ICS feed (.fork/PLAN.md G4). Managing the token needs
// the normal auth stack. Fetching the feed does not: calendar apps can't send a Bearer
// token, so it is authenticated by the unguessable token in the path, and a Cloudflare
// Access bypass for /api/v1/x/calendar/* is needed in front of it.
func mountICS(r chi.Router, chain *errchain.ErrChain, userMW []errchain.Middleware, d Deps) {
	limiter := d.IcsLimiter
	if limiter == nil {
		limiter = ics.NewLimiter(icsRateLimit, icsRateWindow, nil)
	}
	h := &icsHandlers{
		store:   ics.NewStore(d.Ent),
		maint:   d.Maintenance,
		baseURL: strings.TrimRight(d.Config.PublicBaseURL, "/"),
		limiter: limiter,
		now:     time.Now,
	}

	r.Get("/calendar/feed", chain.ToHandlerFunc(h.status, userMW...))
	r.Post("/calendar/feed", chain.ToHandlerFunc(h.issue, userMW...))
	r.Delete("/calendar/feed", chain.ToHandlerFunc(h.revoke, userMW...))
	r.Get("/calendar/{token}.ics", h.serve) // deliberately no userMW
}

type icsHandlers struct {
	store   *ics.Store
	maint   gcal.MaintenanceLister
	baseURL string
	limiter *ics.Limiter
	now     func() time.Time
}

func (h *icsHandlers) status(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	st, err := h.store.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	return server.JSON(w, http.StatusOK, st)
}

// issue creates or rotates the group's feed token and returns the feed URL. This response
// is the only time the token is visible. The URL is absolute when HBOX_X_PUBLIC_BASE_URL is
// set and a root-relative path otherwise (the UI resolves it against the page origin).
func (h *icsHandlers) issue(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	token, err := h.store.Issue(r.Context(), auth.GID, auth.UID)
	if err != nil {
		return err
	}
	st, err := h.store.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	return server.JSON(w, http.StatusCreated, map[string]any{
		"url":        h.baseURL + "/api/v1/x/calendar/" + token + ".ics",
		"exists":     st.Exists,
		"created_at": st.CreatedAt,
		"rotated_at": st.RotatedAt,
	})
}

func (h *icsHandlers) revoke(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	if err := h.store.Revoke(r.Context(), auth.GID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// serve renders the feed. Every failure to authenticate (unknown, malformed or revoked
// token) is the same bare 404.
func (h *icsHandlers) serve(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.Allow(ics.ClientIP(r)) {
		w.Header().Set("Retry-After", strconv.Itoa(int(icsRateWindow.Seconds())))
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	gid, ok, err := h.store.Lookup(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		log.Err(err).Msg("ics feed lookup failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	if h.maint == nil {
		http.NotFound(w, r)
		return
	}
	items, err := h.maint.ListForGroup(r.Context(), gid)
	if err != nil {
		log.Err(err).Msg("ics feed: listing maintenance failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	conn := gcal.Connection{IncludeCompleted: true, ReminderMinutes: feedReminderMinutes}
	events := make([]ics.Event, 0, len(items))
	for _, it := range items {
		d, ok := gcal.MaintenanceDesired(it, conn, h.baseURL)
		if !ok {
			continue
		}
		if ev, ok := ics.FromDesired(d); ok {
			events = append(events, ev)
		}
	}

	body := ics.Render(ics.CalendarName, events, h.now())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="homebox.ics"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(body); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.Debug().Err(err).Msg("ics feed: write failed")
	}
}
