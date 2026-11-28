// Package gcaltest is an in-memory fake of the Google endpoints Homebox uses (OAuth token,
// revoke, calendarList and events), served by httptest. Shared by gcal, api and (later)
// reconciler tests.
package gcaltest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
)

const (
	// Code is the authorization code the fake accepts.
	Code = "good-code"
	// RefreshToken is the refresh token the fake issues.
	RefreshToken = "fake-refresh-token"
)

// Fake is the fake Google server.
type Fake struct {
	Server *httptest.Server

	mu sync.Mutex
	// Calendars is the account's calendarList.
	Calendars []gcal.Calendar
	// Events holds events per calendar ID, keyed by event ID.
	Events map[string]map[string]gcal.Event
	// InvalidGrant makes the token endpoint reject refreshes with invalid_grant.
	InvalidGrant bool
	// NoRefreshToken makes the code exchange omit the refresh token.
	NoRefreshToken bool
	// Revoked is set when the revoke endpoint receives a token.
	Revoked string
	// FailNext makes the next N API (not token) requests return FailStatus.
	FailNext   int
	FailStatus int
	// Requests logs "METHOD path" for every API request, in order.
	Requests []string

	nextID int
}

// New starts a fake with a primary calendar and a shared writable calendar, plus a
// read-only one that must never be offered.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		Calendars: []gcal.Calendar{
			{ID: "owner@example.com", Summary: "Owner", AccessRole: "owner", Primary: true},
			{ID: "household@group.calendar.google.com", Summary: "Household", AccessRole: "writer"},
			{ID: "holidays@group.v.calendar.google.com", Summary: "Holidays", AccessRole: "reader"},
		},
		Events: map[string]map[string]gcal.Event{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", f.revoke)
	mux.HandleFunc("/calendar/", f.api)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	return f
}

// Options returns gcal.Options pointing at the fake.
func (f *Fake) Options() gcal.Options {
	return gcal.Options{
		AuthURL:   f.Server.URL + "/auth",
		TokenURL:  f.Server.URL + "/token",
		RevokeURL: f.Server.URL + "/revoke",
		APIBase:   f.Server.URL + "/calendar",
	}
}

// EventsOn returns a copy of the events currently on a calendar.
func (f *Fake) EventsOn(calendarID string) []gcal.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gcal.Event
	for _, e := range f.Events[calendarID] {
		out = append(out, e)
	}
	return out
}

// AddEvent puts an event on a calendar directly (e.g. a foreign event). Returns its ID.
func (f *Fake) AddEvent(calendarID string, ev gcal.Event) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ev.ID == "" {
		f.nextID++
		ev.ID = fmt.Sprintf("evt%d", f.nextID)
	}
	if f.Events[calendarID] == nil {
		f.Events[calendarID] = map[string]gcal.Event{}
	}
	f.Events[calendarID][ev.ID] = ev
	return ev.ID
}

// Set runs fn with the fake's lock held, for changing its knobs safely.
func (f *Fake) Set(fn func(*Fake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("code") != Code {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		resp := map[string]any{"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3600}
		if !f.NoRefreshToken {
			resp["refresh_token"] = RefreshToken
		}
		writeJSON(w, http.StatusOK, resp)
	case "refresh_token":
		if f.InvalidGrant || r.PostForm.Get("refresh_token") != RefreshToken {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3600})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *Fake) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	f.Revoked = r.PostForm.Get("token")
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func gerr(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": msg, "errors": []map[string]string{{"reason": reason}},
	}})
}

// api serves /calendar/users/me/calendarList and /calendar/calendars/{id}/events[/{eid}].
func (f *Fake) api(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.EscapedPath())

	if r.Header.Get("Authorization") != "Bearer fake-access" {
		gerr(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
		return
	}
	if f.FailNext > 0 {
		f.FailNext--
		status := f.FailStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		gerr(w, status, "backendError", "injected failure")
		return
	}

	path := strings.TrimPrefix(r.URL.EscapedPath(), "/calendar")
	if path == "/users/me/calendarList" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"items": f.Calendars})
		return
	}

	rest, ok := strings.CutPrefix(path, "/calendars/")
	if !ok {
		gerr(w, http.StatusNotFound, "notFound", "not found")
		return
	}
	parts := strings.Split(rest, "/")
	calID, err := url.PathUnescape(parts[0])
	if err != nil || len(parts) < 2 || parts[1] != "events" {
		gerr(w, http.StatusNotFound, "notFound", "not found")
		return
	}
	if f.Events[calID] == nil {
		f.Events[calID] = map[string]gcal.Event{}
	}
	evs := f.Events[calID]

	if len(parts) == 2 {
		switch r.Method {
		case http.MethodPost:
			var ev gcal.Event
			if json.NewDecoder(r.Body).Decode(&ev) != nil {
				gerr(w, http.StatusBadRequest, "invalid", "bad body")
				return
			}
			f.nextID++
			ev.ID = fmt.Sprintf("evt%d", f.nextID)
			evs[ev.ID] = ev
			writeJSON(w, http.StatusOK, ev)
		case http.MethodGet:
			filter := r.URL.Query().Get("privateExtendedProperty")
			var items []gcal.Event
			for _, ev := range evs {
				if filter != "" {
					k, v, _ := strings.Cut(filter, "=")
					if ev.ExtendedProperties == nil || ev.ExtendedProperties.Private[k] != v {
						continue
					}
				}
				items = append(items, ev)
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		default:
			gerr(w, http.StatusMethodNotAllowed, "invalid", "method")
		}
		return
	}

	eid, _ := url.PathUnescape(parts[2])
	cur, exists := evs[eid]
	if !exists {
		gerr(w, http.StatusNotFound, "notFound", "Not Found")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch gcal.Event
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			gerr(w, http.StatusBadRequest, "invalid", "bad body")
			return
		}
		if patch.Summary != "" {
			cur.Summary = patch.Summary
		}
		if patch.Description != "" {
			cur.Description = patch.Description
		}
		if patch.Start != nil {
			cur.Start = patch.Start
		}
		if patch.End != nil {
			cur.End = patch.End
		}
		if patch.Reminders != nil {
			cur.Reminders = patch.Reminders
		}
		if patch.ExtendedProperties != nil {
			cur.ExtendedProperties = patch.ExtendedProperties
		}
		evs[eid] = cur
		writeJSON(w, http.StatusOK, cur)
	case http.MethodDelete:
		delete(evs, eid)
		w.WriteHeader(http.StatusNoContent)
	default:
		gerr(w, http.StatusMethodNotAllowed, "invalid", "method")
	}
}
