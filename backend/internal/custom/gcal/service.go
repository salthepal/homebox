package gcal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	xconfig "github.com/sysadminsmedia/homebox/backend/internal/custom/config"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
)

// Google endpoints and scopes (PLAN §7).
const (
	defaultAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURL  = "https://oauth2.googleapis.com/token"
	defaultRevokeURL = "https://oauth2.googleapis.com/revoke"
	defaultAPIBase   = "https://www.googleapis.com/calendar/v3"

	ScopeEvents       = "https://www.googleapis.com/auth/calendar.events"
	ScopeCalendarList = "https://www.googleapis.com/auth/calendar.calendarlist.readonly"

	// CallbackPath is appended to the public base URL to form the OAuth redirect URI.
	CallbackPath = "/api/v1/x/gcal/callback"
	// MaxReminderMinutes is Google's limit for event reminders (4 weeks).
	MaxReminderMinutes = 40320
)

var (
	// ErrBroken means the stored Google credentials no longer work and the user must reconnect.
	ErrBroken = errors.New("gcal: connection broken, reconnect Google Calendar")
	// ErrCalendarNotSet means a calendar has not been chosen yet.
	ErrCalendarNotSet = errors.New("gcal: no calendar selected")
	// ErrInvalidSetting is returned for settings that fail validation.
	ErrInvalidSetting = errors.New("gcal: invalid setting")
	// ErrNotMember means the user in a signed OAuth state is no longer a member of the group
	// (or membership could not be checked), so the callback fails closed.
	ErrNotMember = errors.New("gcal: user is not a member of the group")
)

// NotConfiguredError is returned while required HBOX_X_* variables are missing.
type NotConfiguredError struct{ Missing []string }

func (e *NotConfiguredError) Error() string {
	return "google calendar is not configured: set " + strings.Join(e.Missing, ", ")
}

// Options override Google endpoints and timing. The zero value targets real Google; tests
// point the URLs at an httptest fake.
type Options struct {
	AuthURL    string
	TokenURL   string
	RevokeURL  string
	APIBase    string
	HTTPClient *http.Client // base client for token and API calls; nil = http.DefaultClient
	Now        func() time.Time
	Backoff    time.Duration
	Sleep      func(ctx context.Context, d time.Duration) error

	// IsMember reports whether the user belongs to the group. The OAuth callback bypasses
	// auth middleware, so it revalidates membership with this; nil fails closed.
	IsMember func(ctx context.Context, gid, uid uuid.UUID) (bool, error)
	// GroupsExist returns the subset of ids that are existing upstream groups (orphan sweep).
	GroupsExist func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error)
}

// Service is the Google Calendar integration: connect flow, settings, authorized clients.
type Service struct {
	cfg   xconfig.Config
	store *Store
	opt   Options
}

// New builds the service. It is safe to build when Google isn't configured: every call that
// needs Google then returns *NotConfiguredError.
func New(c *xent.Client, cfg xconfig.Config, opt Options) *Service {
	if opt.AuthURL == "" {
		opt.AuthURL = defaultAuthURL
	}
	if opt.TokenURL == "" {
		opt.TokenURL = defaultTokenURL
	}
	if opt.RevokeURL == "" {
		opt.RevokeURL = defaultRevokeURL
	}
	if opt.APIBase == "" {
		opt.APIBase = defaultAPIBase
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Service{cfg: cfg, store: NewStore(c), opt: opt}
}

// Store exposes the connection/link store (used by the reconciler).
func (s *Service) Store() *Store { return s.store }

// RedirectURI is the OAuth redirect URI to register in the Google OAuth client.
func (s *Service) RedirectURI() string { return s.cfg.PublicBaseURL + CallbackPath }

// Status is what the settings page shows.
type Status struct {
	Enabled             bool       `json:"enabled"`
	Missing             []string   `json:"missing"`
	RedirectURI         string     `json:"redirect_uri"`
	Connected           bool       `json:"connected"`
	Broken              bool       `json:"broken"`
	GoogleEmail         string     `json:"google_email"`
	CalendarID          string     `json:"calendar_id"`
	CalendarSummary     string     `json:"calendar_summary"`
	IncludeCompleted    bool       `json:"include_completed"`
	IncludeProjectSteps bool       `json:"include_project_steps"`
	ReminderMinutes     int        `json:"reminder_minutes"`
	LastSyncAt          *time.Time `json:"last_sync_at"`
	LastError           string     `json:"last_error"`
}

// Status reports configuration and connection state for a group.
func (s *Service) Status(ctx context.Context, gid uuid.UUID) (Status, error) {
	st := Status{
		Enabled: s.cfg.GcalEnabled(), Missing: s.cfg.GcalMissing(),
		IncludeCompleted: true, IncludeProjectSteps: true, ReminderMinutes: 1440,
	}
	if st.Missing == nil {
		st.Missing = []string{}
	}
	if s.cfg.PublicBaseURL != "" {
		st.RedirectURI = s.RedirectURI()
	}
	conn, err := s.store.GetConnection(ctx, gid)
	if errors.Is(err, ErrNotConnected) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Connected = true
	st.Broken = conn.Broken
	st.GoogleEmail = conn.GoogleEmail
	st.CalendarID = conn.CalendarID
	st.CalendarSummary = conn.CalendarSummary
	st.IncludeCompleted = conn.IncludeCompleted
	st.IncludeProjectSteps = conn.IncludeProjectSteps
	st.ReminderMinutes = conn.ReminderMinutes
	st.LastSyncAt = conn.LastSyncAt
	st.LastError = conn.LastError
	return st, nil
}

func (s *Service) requireConfigured() error {
	if m := s.cfg.GcalMissing(); len(m) > 0 {
		return &NotConfiguredError{Missing: m}
	}
	return nil
}

func (s *Service) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.GoogleClientID,
		ClientSecret: s.cfg.GoogleClientSecret,
		RedirectURL:  s.RedirectURI(),
		Scopes:       []string{ScopeEvents, ScopeCalendarList},
		Endpoint:     oauth2.Endpoint{AuthURL: s.opt.AuthURL, TokenURL: s.opt.TokenURL},
	}
}

// withHTTP puts the configured base client in ctx so oauth2 uses it for token calls.
func (s *Service) withHTTP(ctx context.Context) context.Context {
	if s.opt.HTTPClient != nil {
		return context.WithValue(ctx, oauth2.HTTPClient, s.opt.HTTPClient)
	}
	return ctx
}

// ConnectURL returns the Google consent URL for the acting user and group. The state is
// signed and expires after StateTTL.
func (s *Service) ConnectURL(uid, gid uuid.UUID) (string, error) {
	if err := s.requireConfigured(); err != nil {
		return "", err
	}
	state, err := SignState(s.cfg.SecretKey, uid, gid, s.opt.Now())
	if err != nil {
		return "", err
	}
	return s.oauthConfig().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), nil
}

// HandleCallback completes the OAuth flow: it verifies the state, exchanges the code and
// stores the encrypted refresh token. It returns the group the connection belongs to.
func (s *Service) HandleCallback(ctx context.Context, code, state string) (uuid.UUID, error) {
	if err := s.requireConfigured(); err != nil {
		return uuid.Nil, err
	}
	uid, gid, err := VerifyState(s.cfg.SecretKey, state, s.opt.Now())
	if err != nil {
		return uuid.Nil, err
	}
	if s.opt.IsMember == nil {
		return gid, ErrNotMember
	}
	ok, err := s.opt.IsMember(ctx, gid, uid)
	if err != nil {
		return gid, fmt.Errorf("%w: %w", ErrNotMember, err)
	}
	if !ok {
		return gid, ErrNotMember
	}
	if code == "" {
		return gid, errors.New("gcal: missing authorization code")
	}
	ctx = s.withHTTP(ctx)
	tok, err := s.oauthConfig().Exchange(ctx, code)
	if err != nil {
		return gid, fmt.Errorf("gcal: code exchange failed: %w", err)
	}
	if tok.RefreshToken == "" {
		return gid, errors.New("gcal: Google returned no refresh token (remove Homebox from your Google account's connected apps and try again)")
	}

	// The primary calendar's ID is the account's email address.
	cal := &Client{HTTP: oauth2.NewClient(ctx, s.oauthConfig().TokenSource(ctx, tok)), BaseURL: s.opt.APIBase,
		Backoff: s.opt.Backoff, Sleep: s.opt.Sleep}
	cals, err := cal.ListCalendars(ctx)
	if err != nil {
		return gid, fmt.Errorf("gcal: reading calendars: %w", err)
	}
	email := ""
	for _, c := range cals {
		if c.Primary {
			email = c.ID
			break
		}
	}

	enc, err := Encrypt(s.cfg.SecretKey, []byte(tok.RefreshToken))
	if err != nil {
		return gid, err
	}
	return gid, s.store.SaveConnection(ctx, gid, uid, email, enc)
}

// Client returns an authorized Calendar client for the group and its connection. It returns
// ErrNotConnected or ErrBroken when no usable credentials exist. Callers should pass any
// error from client calls through ClassifyError so a dead token marks the connection broken.
func (s *Service) Client(ctx context.Context, gid uuid.UUID) (*Client, Connection, error) {
	if err := s.requireConfigured(); err != nil {
		return nil, Connection{}, err
	}
	conn, err := s.store.GetConnection(ctx, gid)
	if err != nil {
		return nil, Connection{}, err
	}
	if conn.Broken {
		return nil, conn, ErrBroken
	}
	rt, err := Decrypt(s.cfg.SecretKey, conn.encToken)
	if err != nil {
		return nil, conn, err
	}
	hctx := s.withHTTP(ctx)
	ts := s.oauthConfig().TokenSource(hctx, &oauth2.Token{RefreshToken: string(rt)})
	return &Client{
		HTTP: oauth2.NewClient(hctx, ts), BaseURL: s.opt.APIBase,
		Backoff: s.opt.Backoff, Sleep: s.opt.Sleep,
	}, conn, nil
}

// ClassifyError inspects an error from a Google call. A revoked or expired grant
// (invalid_grant) or a 401 marks the connection broken and returns ErrBroken (wrapping the
// cause); any other error is returned unchanged.
func (s *Service) ClassifyError(ctx context.Context, gid uuid.UUID, err error) error {
	if err == nil {
		return nil
	}
	var re *oauth2.RetrieveError
	dead := IsAuth(err)
	if errors.As(err, &re) {
		dead = dead || re.ErrorCode == "invalid_grant" || re.Response != nil && re.Response.StatusCode == http.StatusUnauthorized
	}
	if !dead {
		return err
	}
	_ = s.store.MarkBroken(ctx, gid, "Google rejected the saved credentials; reconnect Google Calendar")
	return fmt.Errorf("%w: %w", ErrBroken, err)
}

// Calendars lists the calendars the connected account can write to.
func (s *Service) Calendars(ctx context.Context, gid uuid.UUID) ([]Calendar, error) {
	c, _, err := s.Client(ctx, gid)
	if err != nil {
		return nil, err
	}
	cals, err := c.ListWritableCalendars(ctx)
	if err != nil {
		return nil, s.ClassifyError(ctx, gid, err)
	}
	return cals, nil
}

// SettingsInput patches the calendar choice and options; nil fields are unchanged.
type SettingsInput struct {
	CalendarID          *string `json:"calendar_id"`
	IncludeCompleted    *bool   `json:"include_completed"`
	IncludeProjectSteps *bool   `json:"include_project_steps"`
	ReminderMinutes     *int    `json:"reminder_minutes"`
}

// UpdateSettings validates and stores settings. A new calendar must be one the account can
// write to; its display name is stored with it.
func (s *Service) UpdateSettings(ctx context.Context, gid uuid.UUID, in SettingsInput) error {
	if in.ReminderMinutes != nil && (*in.ReminderMinutes < 0 || *in.ReminderMinutes > MaxReminderMinutes) {
		return fmt.Errorf("%w: reminder_minutes must be 0-%d", ErrInvalidSetting, MaxReminderMinutes)
	}
	upd := SettingsUpdate{
		IncludeCompleted: in.IncludeCompleted, IncludeProjectSteps: in.IncludeProjectSteps,
		ReminderMinutes: in.ReminderMinutes,
	}
	if in.CalendarID != nil {
		id := strings.TrimSpace(*in.CalendarID)
		if id == "" {
			return fmt.Errorf("%w: calendar_id is empty", ErrInvalidSetting)
		}
		cals, err := s.Calendars(ctx, gid)
		if err != nil {
			return err
		}
		found := false
		for _, c := range cals {
			if c.ID == id {
				upd.CalendarID, upd.CalendarSummary = &c.ID, &c.Summary
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: calendar %q is not a calendar this account can write to", ErrInvalidSetting, id)
		}
		if err := s.switchCalendar(ctx, gid, id); err != nil {
			return err
		}
	} else if _, err := s.store.GetConnection(ctx, gid); err != nil {
		return err
	}
	return s.store.UpdateSettings(ctx, gid, upd)
}

// switchCalendar prepares for moving the group to calendar id. If a different calendar was
// chosen before, our events are removed from it (best effort: skipped when the connection
// is broken or Google isn't configured) and the group's links are dropped so the next sync
// starts clean. A removal error aborts the switch with nothing changed.
func (s *Service) switchCalendar(ctx context.Context, gid uuid.UUID, id string) error {
	conn, err := s.store.GetConnection(ctx, gid)
	if err != nil {
		return err
	}
	if conn.CalendarID == "" || conn.CalendarID == id {
		return nil
	}
	if !conn.Broken && s.requireConfigured() == nil {
		if _, err := s.removeOurEvents(ctx, gid, conn); err != nil {
			return err
		}
	}
	_, err = s.store.DeleteLinks(ctx, gid)
	return err
}

// SweepOrphans deletes connections (and their links) whose upstream group no longer exists,
// after a best-effort token revoke. Google events are never touched: with the group gone
// only the stored credentials and links remain to clean up. It returns how many were swept.
func (s *Service) SweepOrphans(ctx context.Context) (int, error) {
	if s.opt.GroupsExist == nil {
		return 0, nil
	}
	conns, err := s.store.ListConnections(ctx)
	if err != nil || len(conns) == 0 {
		return 0, err
	}
	ids := make([]uuid.UUID, len(conns))
	for i, c := range conns {
		ids[i] = c.GroupID
	}
	alive, err := s.opt.GroupsExist(ctx, ids)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range conns {
		if _, ok := alive[c.GroupID]; ok {
			continue
		}
		s.revoke(ctx, c)
		if err := s.store.DeleteConnection(ctx, c.GroupID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DisconnectResult reports what Disconnect did.
type DisconnectResult struct {
	// RemovedEvents is how many Homebox events were deleted from the calendar.
	RemovedEvents int `json:"removed_events"`
	// RemoveSkipped is true if removal was requested but couldn't run (broken connection or
	// no calendar chosen), so events may remain on the calendar.
	RemoveSkipped bool `json:"remove_skipped"`
}

// Disconnect removes the group's connection and links. With removeEvents it first deletes
// the events Homebox created: only events that are in x_gcal_links AND carry the Homebox
// owner marker. It never deletes or clears the calendar itself. If removal fails midway the
// connection is kept so the user can retry.
func (s *Service) Disconnect(ctx context.Context, gid uuid.UUID, removeEvents bool) (DisconnectResult, error) {
	var res DisconnectResult
	conn, err := s.store.GetConnection(ctx, gid)
	if err != nil {
		return res, err
	}

	if removeEvents {
		if conn.Broken || conn.CalendarID == "" || s.requireConfigured() != nil {
			res.RemoveSkipped = true
		} else {
			n, err := s.removeOurEvents(ctx, gid, conn)
			res.RemovedEvents = n
			if err != nil {
				return res, err
			}
		}
	}

	s.revoke(ctx, conn)
	if err := s.store.DeleteConnection(ctx, gid); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Service) removeOurEvents(ctx context.Context, gid uuid.UUID, conn Connection) (int, error) {
	c, _, err := s.Client(ctx, gid)
	if err != nil {
		return 0, err
	}
	links, err := s.store.ListLinks(ctx, gid)
	if err != nil {
		return 0, err
	}
	linked := make(map[string]struct{}, len(links))
	for _, l := range links {
		linked[l.EventID] = struct{}{}
	}
	events, err := c.ListEvents(ctx, conn.CalendarID, ListEventsParams{
		PrivateExtendedProperty: OwnerProperty + "=" + OwnerValue,
	})
	if err != nil {
		return 0, s.ClassifyError(ctx, gid, err)
	}
	removed := 0
	for _, ev := range events {
		if _, ok := linked[ev.ID]; !ok || !ev.IsOurs() {
			continue // not provably ours: leave it alone
		}
		if err := c.DeleteEvent(ctx, conn.CalendarID, ev.ID); err != nil && !IsNotFound(err) {
			return removed, s.ClassifyError(ctx, gid, err)
		}
		removed++
	}
	return removed, nil
}

// revoke asks Google to revoke the refresh token. Best effort: failures are ignored, since
// the local credentials are deleted regardless.
func (s *Service) revoke(ctx context.Context, conn Connection) {
	if s.requireConfigured() != nil {
		return
	}
	rt, err := Decrypt(s.cfg.SecretKey, conn.encToken)
	if err != nil {
		return
	}
	hc := s.opt.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.opt.RevokeURL,
		strings.NewReader(url.Values{"token": {string(rt)}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}
