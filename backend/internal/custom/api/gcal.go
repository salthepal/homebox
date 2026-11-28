package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// mountGcal registers the Google Calendar routes. Everything is behind userMW except the
// OAuth callback: it is a browser redirect from Google with no Bearer token, so it is
// authenticated by the signed, expiring state value instead.
func mountGcal(r chi.Router, chain *errchain.ErrChain, userMW []errchain.Middleware, d Deps) {
	svc := gcal.New(d.Ent, d.Config, d.Gcal)
	h := &gcalHandlers{svc: svc, baseURL: d.Config.PublicBaseURL, rec: d.GcalReconciler}

	r.Get("/gcal/status", chain.ToHandlerFunc(h.status, userMW...))
	r.Post("/gcal/connect", chain.ToHandlerFunc(h.connect, userMW...))
	r.Get("/gcal/calendars", chain.ToHandlerFunc(h.calendars, userMW...))
	r.Put("/gcal/settings", chain.ToHandlerFunc(h.settings, userMW...))
	r.Post("/gcal/disconnect", chain.ToHandlerFunc(h.disconnect, userMW...))
	r.Post("/gcal/sync", chain.ToHandlerFunc(h.sync, userMW...))
	r.Get("/gcal/callback", h.callback) // deliberately no userMW
}

type gcalHandlers struct {
	svc     *gcal.Service
	baseURL string
	rec     *gcal.Reconciler
}

// syncResponse is returned by "Sync now": what changed, plus the refreshed status (last sync
// time and error) for the settings page.
type syncResponse struct {
	Result gcal.Result `json:"result"`
	Status gcal.Status `json:"status"`
}

func (h *gcalHandlers) sync(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	if h.rec == nil {
		return validate.NewRequestError(errors.New("google calendar sync is not available"), http.StatusConflict)
	}
	res, err := h.rec.Reconcile(r.Context(), auth.GID)
	if err != nil {
		return gcalError(err)
	}
	st, err := h.svc.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	return server.JSON(w, http.StatusOK, syncResponse{Result: res, Status: st})
}

func (h *gcalHandlers) status(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	st, err := h.svc.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	return server.JSON(w, http.StatusOK, st)
}

func (h *gcalHandlers) connect(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	u, err := h.svc.ConnectURL(auth.UID, auth.GID)
	if err != nil {
		return gcalError(err)
	}
	return server.JSON(w, http.StatusOK, map[string]string{"url": u})
}

func (h *gcalHandlers) calendars(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	cals, err := h.svc.Calendars(r.Context(), auth.GID)
	if err != nil {
		return gcalError(err)
	}
	if cals == nil {
		cals = []gcal.Calendar{}
	}
	return server.JSON(w, http.StatusOK, map[string]any{"calendars": cals})
}

func (h *gcalHandlers) settings(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	var in gcal.SettingsInput
	if err := server.Decode(r, &in); err != nil {
		return validate.NewRequestError(errors.New("invalid request body"), http.StatusBadRequest)
	}
	if err := h.svc.UpdateSettings(r.Context(), auth.GID, in); err != nil {
		return gcalError(err)
	}
	st, err := h.svc.Status(r.Context(), auth.GID)
	if err != nil {
		return err
	}
	return server.JSON(w, http.StatusOK, st)
}

func (h *gcalHandlers) disconnect(w http.ResponseWriter, r *http.Request) error {
	auth := services.NewContext(r.Context())
	var in struct {
		RemoveEvents bool `json:"remove_events"`
	}
	if r.ContentLength != 0 {
		if err := server.Decode(r, &in); err != nil && !errors.Is(err, io.EOF) {
			return validate.NewRequestError(errors.New("invalid request body"), http.StatusBadRequest)
		}
	}
	res, err := h.svc.Disconnect(r.Context(), auth.GID, in.RemoveEvents)
	if err != nil {
		return gcalError(err)
	}
	return server.JSON(w, http.StatusOK, res)
}

// callback finishes the OAuth flow and sends the browser back to /x/calendar. It never
// returns JSON errors: the user is mid-redirect, so failures are reported as a short code
// in the query string.
func (h *gcalHandlers) callback(w http.ResponseWriter, r *http.Request) {
	if h.baseURL == "" {
		http.Error(w, "google calendar is not configured", http.StatusNotFound)
		return
	}
	back := func(key, val string) {
		http.Redirect(w, r, h.baseURL+"/x/calendar?"+url.Values{key: {val}}.Encode(), http.StatusFound)
	}

	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back("error", "denied") // user declined at Google
		return
	}
	if _, err := h.svc.HandleCallback(r.Context(), q.Get("code"), q.Get("state")); err != nil {
		log.Err(err).Msg("google calendar callback failed")
		switch {
		case errors.Is(err, gcal.ErrInvalidState):
			back("error", "state")
		default:
			back("error", "failed")
		}
		return
	}
	back("connected", "1")
}

// gcalError maps service errors onto HTTP statuses.
func gcalError(err error) error {
	var nc *gcal.NotConfiguredError
	switch {
	case errors.As(err, &nc):
		return validate.NewRequestError(err, http.StatusConflict)
	case errors.Is(err, gcal.ErrNotConnected):
		return validate.NewRequestError(errors.New("google calendar is not connected"), http.StatusNotFound)
	case errors.Is(err, gcal.ErrBroken):
		return validate.NewRequestError(errors.New("google connection is broken, reconnect Google Calendar"), http.StatusConflict)
	case errors.Is(err, gcal.ErrInvalidSetting):
		return validate.NewRequestError(err, http.StatusBadRequest)
	case errors.Is(err, gcal.ErrCalendarNotSet):
		return validate.NewRequestError(errors.New("choose a calendar first"), http.StatusConflict)
	}
	var ae *gcal.APIError
	if errors.As(err, &ae) {
		return validate.NewRequestError(errors.New("google calendar request failed"), http.StatusBadGateway)
	}
	return err
}
