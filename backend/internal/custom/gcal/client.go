package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// OwnerProperty / OwnerValue mark every event Homebox creates, so Homebox only ever
	// touches its own events on a shared calendar (PLAN §7 shared-calendar safety).
	OwnerProperty = "homebox_source"
	OwnerValue    = "homebox"
	// IDProperty carries the Homebox source ID (used to rebuild links from the calendar).
	IDProperty = "homebox_id"

	maxAttempts = 4
)

// Client is a minimal hand-written Google Calendar v3 REST client. HTTP must already carry
// OAuth credentials (an oauth2 client). It retries 429 and 5xx with exponential backoff.
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://www.googleapis.com/calendar/v3
	// Backoff is the first retry delay (doubling each attempt). Zero means 500ms.
	Backoff time.Duration
	// Sleep waits between retries; nil uses a context-aware timer. Tests override it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// APIError is a non-2xx response from Google.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("google calendar api: %d %s: %s", e.Status, e.Reason, e.Message)
}

// IsNotFound reports a 404 or 410 (event gone).
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusGone)
}

// IsAuth reports a 401: the credentials are no longer valid.
func IsAuth(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// Calendar is a calendarList entry.
type Calendar struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	AccessRole string `json:"accessRole"`
	Primary    bool   `json:"primary"`
}

// Writable reports whether the account may create events on the calendar.
func (c Calendar) Writable() bool { return c.AccessRole == "owner" || c.AccessRole == "writer" }

// EventDate is an all-day date (YYYY-MM-DD). For all-day events Google's end date is exclusive.
type EventDate struct {
	Date string `json:"date,omitempty"`
}

// ReminderOverride is one popup/email reminder.
type ReminderOverride struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}

// Reminders configures event reminders. UseDefault=false with no overrides means none.
type Reminders struct {
	UseDefault bool               `json:"useDefault"`
	Overrides  []ReminderOverride `json:"overrides,omitempty"`
}

// ExtendedProperties holds Homebox's private markers.
type ExtendedProperties struct {
	Private map[string]string `json:"private,omitempty"`
}

// Event is the subset of a Calendar event Homebox reads and writes.
type Event struct {
	ID                 string              `json:"id,omitempty"`
	Status             string              `json:"status,omitempty"`
	Summary            string              `json:"summary,omitempty"`
	Description        string              `json:"description,omitempty"`
	Start              *EventDate          `json:"start,omitempty"`
	End                *EventDate          `json:"end,omitempty"`
	Reminders          *Reminders          `json:"reminders,omitempty"`
	ExtendedProperties *ExtendedProperties `json:"extendedProperties,omitempty"`
}

// IsOurs reports whether the event carries Homebox's owner marker.
func (e Event) IsOurs() bool {
	return e.ExtendedProperties != nil && e.ExtendedProperties.Private[OwnerProperty] == OwnerValue
}

// ListCalendars returns every calendarList entry (all pages).
func (c *Client) ListCalendars(ctx context.Context) ([]Calendar, error) {
	var out []Calendar
	token := ""
	for {
		q := url.Values{"minAccessRole": {"reader"}, "maxResults": {"250"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var page struct {
			Items         []Calendar `json:"items"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodGet, "/users/me/calendarList", q, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}

// ListWritableCalendars returns only owner/writer calendars, primary first, then by name.
func (c *Client) ListWritableCalendars(ctx context.Context) ([]Calendar, error) {
	all, err := c.ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	var out []Calendar
	for _, cal := range all {
		if cal.Writable() {
			out = append(out, cal)
		}
	}
	sortCalendars(out)
	return out, nil
}

// InsertEvent creates an event and returns it with its Google-assigned ID.
func (c *Client) InsertEvent(ctx context.Context, calendarID string, ev Event) (Event, error) {
	var out Event
	err := c.do(ctx, http.MethodPost, eventsPath(calendarID, ""), nil, ev, &out)
	return out, err
}

// eventPatch is the body of a PATCH. Google leaves omitted fields unchanged, so the fields
// Homebox must be able to clear (description, reminder overrides) are always sent, even when
// empty; Event's omitempty tags would silently turn "clear this" into "leave it alone".
type eventPatch struct {
	Summary            string              `json:"summary"`
	Description        string              `json:"description"`
	Start              *EventDate          `json:"start,omitempty"`
	End                *EventDate          `json:"end,omitempty"`
	Reminders          *remindersPatch     `json:"reminders,omitempty"`
	ExtendedProperties *ExtendedProperties `json:"extendedProperties,omitempty"`
}

type remindersPatch struct {
	UseDefault bool               `json:"useDefault"`
	Overrides  []ReminderOverride `json:"overrides"` // [] (not omitted) clears existing reminders
}

// PatchEvent updates an existing event to match ev. Summary and description are always
// written (an empty description clears it); reminders, when set, replace the event's
// reminders entirely, including clearing them when ev has none.
func (c *Client) PatchEvent(ctx context.Context, calendarID, eventID string, ev Event) (Event, error) {
	body := eventPatch{
		Summary: ev.Summary, Description: ev.Description,
		Start: ev.Start, End: ev.End, ExtendedProperties: ev.ExtendedProperties,
	}
	if ev.Reminders != nil {
		body.Reminders = &remindersPatch{UseDefault: ev.Reminders.UseDefault, Overrides: ev.Reminders.Overrides}
		if body.Reminders.Overrides == nil {
			body.Reminders.Overrides = []ReminderOverride{}
		}
	}
	var out Event
	err := c.do(ctx, http.MethodPatch, eventsPath(calendarID, eventID), nil, body, &out)
	return out, err
}

// DeleteEvent deletes an event. A missing event returns an error for which IsNotFound is true.
func (c *Client) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	return c.do(ctx, http.MethodDelete, eventsPath(calendarID, eventID), nil, nil, nil)
}

// ListEventsParams filters ListEvents.
type ListEventsParams struct {
	// PrivateExtendedProperty is a "key=value" filter, e.g. "homebox_source=homebox".
	PrivateExtendedProperty string
}

// ListEvents returns matching events (all pages).
func (c *Client) ListEvents(ctx context.Context, calendarID string, p ListEventsParams) ([]Event, error) {
	var out []Event
	token := ""
	for {
		q := url.Values{"maxResults": {"250"}, "showDeleted": {"false"}, "singleEvents": {"false"}}
		if p.PrivateExtendedProperty != "" {
			q.Set("privateExtendedProperty", p.PrivateExtendedProperty)
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		var page struct {
			Items         []Event `json:"items"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodGet, eventsPath(calendarID, ""), q, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}

func eventsPath(calendarID, eventID string) string {
	p := "/calendars/" + url.PathEscape(calendarID) + "/events"
	if eventID != "" {
		p += "/" + url.PathEscape(eventID)
	}
	return p
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	delay := c.Backoff
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			// Transport errors (including oauth2 token refresh failures) are not retried:
			// the caller classifies them (e.g. invalid_grant).
			return err
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if rerr != nil {
			return rerr
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			return json.Unmarshal(data, out)
		}

		lastErr = parseAPIError(resp.StatusCode, data)
		// A 5xx on a POST may mean Google created the event anyway, so retrying could
		// duplicate it: POSTs retry on 429 only.
		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode >= 500 && method != http.MethodPost)
		if !retryable || attempt == maxAttempts {
			return lastErr
		}
		wait := delay
		if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && ra >= 0 && ra <= 60 {
			wait = time.Duration(ra) * time.Second
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
		delay *= 2
	}
	return lastErr
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseAPIError(status int, body []byte) *APIError {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	ae := &APIError{Status: status}
	if json.Unmarshal(body, &e) == nil {
		ae.Message = e.Error.Message
		if len(e.Error.Errors) > 0 {
			ae.Reason = e.Error.Errors[0].Reason
		}
	}
	if ae.Message == "" {
		ae.Message = http.StatusText(status)
	}
	return ae
}
