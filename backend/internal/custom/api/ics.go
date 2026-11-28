package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ics"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// Default feed rate limit: requests per client IP per window.
const (
	icsRateLimit  = 60
	icsRateWindow = time.Minute
)

// Feed bounds: at most icsMaxEvents events (newest first), completed entries only from the
// last icsCompletedDays days, scheduled ones up to icsScheduledYears years ahead.
const (
	icsMaxEvents      = 5000
	icsCompletedDays  = 365
	icsScheduledYears = 2
)

// feedReminderMinutes is the VALARM lead time written into the feed for scheduled work.
const feedReminderMinutes = 1440

// IcsFeedSource is the bounded read side of upstream maintenance entries used by the feed
// (upstream.MaintenanceReader implements it).
type IcsFeedSource interface {
	ListForFeed(ctx context.Context, gid uuid.UUID, completedSince, scheduledUntil time.Time, limit int) ([]upstream.MaintenanceItem, error)
}

// IcsGroups answers the group questions the feed needs (upstream.Groups implements it).
type IcsGroups interface {
	// IsOwner reports whether the user owns the group.
	IsOwner(ctx context.Context, gid, uid uuid.UUID) (bool, error)
	// GroupExists reports whether the group still exists.
	GroupExists(ctx context.Context, gid uuid.UUID) (bool, error)
}

// mountICS registers the read-only ICS feed (.fork/PLAN.md G4). Managing the token needs
// the normal auth stack, and creating/rotating/revoking it needs group ownership. Fetching
// the feed needs no Authorization header: calendar apps can't send a Bearer token, so it is
// authenticated by the unguessable token in the path, and a Cloudflare Access bypass for
// /api/v1/x/calendar/* is needed in front of it.
func mountICS(r chi.Router, chain *errchain.ErrChain, userMW []errchain.Middleware, d Deps) {
	limiter := d.IcsLimiter
	if limiter == nil {
		limiter = ics.NewLimiter(icsRateLimit, icsRateWindow, nil)
	}
	limit := d.IcsFeedLimit
	if limit <= 0 {
		limit = icsMaxEvents
	}
	h := &icsHandlers{
		store:   ics.NewStore(d.Ent),
		feed:    d.IcsFeed,
		groups:  d.IcsGroups,
		baseURL: strings.TrimRight(d.Config.PublicBaseURL, "/"),
		limiter: limiter,
		limit:   limit,
		now:     time.Now,
	}

	r.Get("/calendar/feed", chain.ToHandlerFunc(h.status, userMW...))
	r.Post("/calendar/feed", chain.ToHandlerFunc(h.issue, userMW...))
	r.Delete("/calendar/feed", chain.ToHandlerFunc(h.revoke, userMW...))
	r.Get("/calendar/{token}.ics", h.serve) // deliberately no userMW
}

type icsHandlers struct {
	store   *ics.Store
	feed    IcsFeedSource
	groups  IcsGroups
	baseURL string
	limiter *ics.Limiter
	limit   int
	now     func() time.Time
}

// noStore marks a response as sensitive: never cached, never sniffed, no referrer. It is set
// on every response of the feed route (including 404 and 429) and on the issue response,
// which contains the secret URL.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// requireOwner allows only owners of the caller's group. It fails closed: with no group
// adapter wired, nobody manages the feed.
func (h *icsHandlers) requireOwner(r *http.Request) error {
	auth := services.NewContext(r.Context())
	owner := false
	if h.groups != nil {
		var err error
		owner, err = h.groups.IsOwner(r.Context(), auth.GID, auth.UID)
		if err != nil {
			return err
		}
	}
	if !owner {
		return validate.NewRequestError(errors.New("only the group owner can manage the calendar feed"), http.StatusForbidden)
	}
	return nil
}

// status is readable by any member. It never reveals the token.
func (h *icsHandlers) status(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	st, err := h.store.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	noStore(w)
	return server.JSON(w, http.StatusOK, st)
}

// issue creates or rotates the group's feed token and returns the feed URL. This response
// is the only time the token is visible. The URL is absolute when HBOX_X_PUBLIC_BASE_URL is
// set and a root-relative path otherwise (the UI resolves it against the page origin).
func (h *icsHandlers) issue(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireOwner(r); err != nil {
		return err
	}
	auth := services.NewContext(r.Context())
	token, err := h.store.Issue(r.Context(), auth.GID, auth.UID)
	if err != nil {
		return err
	}
	st, err := h.store.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	noStore(w)
	return server.JSON(w, http.StatusCreated, map[string]any{
		"url":        h.baseURL + "/api/v1/x/calendar/" + token + ".ics",
		"exists":     st.Exists,
		"created_at": st.CreatedAt,
		"rotated_at": st.RotatedAt,
	})
}

func (h *icsHandlers) revoke(w http.ResponseWriter, r *http.Request) error {
	if err := h.requireOwner(r); err != nil {
		return err
	}
	auth := services.NewContext(r.Context())
	if err := h.store.Revoke(r.Context(), auth.GID); err != nil {
		return err
	}
	noStore(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// serve renders the feed. Every failure to authenticate (unknown, malformed, revoked or
// orphaned token) is the same bare 404.
func (h *icsHandlers) serve(w http.ResponseWriter, r *http.Request) {
	noStore(w) // before anything else, so 404/429/500 carry it too

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
	if !ok || h.groups == nil || h.feed == nil {
		http.NotFound(w, r)
		return
	}

	// A token whose group was deleted stops working immediately (the row can't cascade from
	// upstream tables, R4); remove it as we go.
	exists, err := h.groups.GroupExists(r.Context(), gid)
	if err != nil {
		log.Err(err).Msg("ics feed: group lookup failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !exists {
		if _, err := h.store.DeleteForGroups(r.Context(), []uuid.UUID{gid}); err != nil {
			log.Err(err).Msg("ics feed: removing orphaned token failed")
		}
		http.NotFound(w, r)
		return
	}

	now := h.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	items, err := h.feed.ListForFeed(r.Context(), gid,
		today.AddDate(0, 0, -icsCompletedDays), today.AddDate(icsScheduledYears, 0, 0), h.limit)
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

	// The ETag covers the content only: DTSTAMP changes every request, so it is hashed with a
	// fixed stamp. Unchanged feeds answer If-None-Match with 304 and no body.
	sum := sha256.Sum256(ics.Render(ics.CalendarName, events, time.Unix(0, 0)))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="homebox.ics"`)
	if _, err := w.Write(ics.Render(ics.CalendarName, events, now)); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.Debug().Err(err).Msg("ics feed: write failed")
	}
}

// etagMatches implements the weak comparison of If-None-Match (RFC 9110 §13.1.2).
func etagMatches(header, etag string) bool {
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == etag {
			return true
		}
	}
	return false
}
