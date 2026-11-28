package gcal

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xgcalconnection"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xgcallink"
)

// ErrNotConnected is returned when a group has no Google Calendar connection.
var ErrNotConnected = errors.New("gcal: not connected")

// Source types for links.
const (
	SourceMaintenance = "maintenance"
	SourceProjectStep = "project_step"
)

// Connection is a group's Google Calendar connection (without the token).
type Connection struct {
	GroupID             uuid.UUID
	ConnectedBy         uuid.UUID
	GoogleEmail         string
	CalendarID          string
	CalendarSummary     string
	IncludeCompleted    bool
	IncludeProjectSteps bool
	ReminderMinutes     int
	Broken              bool
	LastSyncAt          *time.Time
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time

	encToken []byte
}

// Link maps a Homebox source row to the Google event created for it.
type Link struct {
	ID          uuid.UUID
	GroupID     uuid.UUID
	SourceType  string
	SourceID    uuid.UUID
	EventID     string
	ContentHash string
	SyncedAt    time.Time
}

// SettingsUpdate patches connection options; nil fields are left unchanged.
type SettingsUpdate struct {
	CalendarID          *string
	CalendarSummary     *string
	IncludeCompleted    *bool
	IncludeProjectSteps *bool
	ReminderMinutes     *int
}

// Store persists connections and links in the custom tables. Every method is scoped by
// group ID.
type Store struct {
	ent *xent.Client
}

// NewStore wraps the custom ent client.
func NewStore(c *xent.Client) *Store { return &Store{ent: c} }

// ---- connection ----

// GetConnection returns the group's connection, or ErrNotConnected.
func (s *Store) GetConnection(ctx context.Context, gid uuid.UUID) (Connection, error) {
	row, err := s.ent.XGcalConnection.Get(ctx, gid)
	if xent.IsNotFound(err) {
		return Connection{}, ErrNotConnected
	}
	if err != nil {
		return Connection{}, err
	}
	return toConnection(row), nil
}

// SaveConnection creates the group's connection, or on reconnect replaces the token and
// account. A reconnect with the same account (a token refresh) keeps the chosen calendar,
// options and links. A reconnect with a DIFFERENT Google account clears the calendar choice
// and drops the group's links in the same transaction: the old events can't be removed
// (the old credentials are gone), so they stay on the old account's calendar, and keeping
// the links would make those sources look already synced and never reach the new calendar.
// It clears the broken flag and last error.
func (s *Store) SaveConnection(ctx context.Context, gid, uid uuid.UUID, email string, encToken []byte) error {
	tx, err := s.ent.Tx(ctx)
	if err != nil {
		return err
	}
	if err := saveConnectionTx(ctx, tx, gid, uid, email, encToken); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func saveConnectionTx(ctx context.Context, tx *xent.Tx, gid, uid uuid.UUID, email string, encToken []byte) error {
	existing, err := tx.XGcalConnection.Get(ctx, gid)
	switch {
	case xent.IsNotFound(err):
		return tx.XGcalConnection.Create().
			SetID(gid).SetConnectedBy(uid).SetGoogleEmail(email).
			SetEncRefreshToken(encToken).Exec(ctx)
	case err != nil:
		return err
	}
	u := existing.Update().
		SetConnectedBy(uid).SetGoogleEmail(email).
		SetEncRefreshToken(encToken).SetBroken(false).SetLastError("")
	if existing.GoogleEmail != "" && email != "" && existing.GoogleEmail != email {
		u.SetCalendarID("").SetCalendarSummary("")
		if _, err := tx.XGcalLink.Delete().Where(xgcallink.GroupID(gid)).Exec(ctx); err != nil {
			return err
		}
	}
	return u.Exec(ctx)
}

// OtherConnectionsWithEmail counts connections of other groups that use the same Google
// account. Google revokes a grant per account and client, so a token may only be revoked
// when this is zero.
func (s *Store) OtherConnectionsWithEmail(ctx context.Context, gid uuid.UUID, email string) (int, error) {
	if email == "" {
		return 0, nil
	}
	return s.ent.XGcalConnection.Query().
		Where(xgcalconnection.IDNEQ(gid), xgcalconnection.GoogleEmail(email)).Count(ctx)
}

// UpdateSettings patches the connection's calendar and options.
func (s *Store) UpdateSettings(ctx context.Context, gid uuid.UUID, in SettingsUpdate) error {
	u := s.ent.XGcalConnection.UpdateOneID(gid)
	if in.CalendarID != nil {
		u.SetCalendarID(*in.CalendarID)
	}
	if in.CalendarSummary != nil {
		u.SetCalendarSummary(*in.CalendarSummary)
	}
	if in.IncludeCompleted != nil {
		u.SetIncludeCompleted(*in.IncludeCompleted)
	}
	if in.IncludeProjectSteps != nil {
		u.SetIncludeProjectSteps(*in.IncludeProjectSteps)
	}
	if in.ReminderMinutes != nil {
		u.SetReminderMinutes(*in.ReminderMinutes)
	}
	err := u.Exec(ctx)
	if xent.IsNotFound(err) {
		return ErrNotConnected
	}
	return err
}

// MarkBroken flags the connection as needing a reconnect and records why.
func (s *Store) MarkBroken(ctx context.Context, gid uuid.UUID, msg string) error {
	err := s.ent.XGcalConnection.UpdateOneID(gid).SetBroken(true).SetLastError(msg).Exec(ctx)
	if xent.IsNotFound(err) {
		return ErrNotConnected
	}
	return err
}

// RecordSync stores the outcome of a sync attempt (empty errMsg = success).
func (s *Store) RecordSync(ctx context.Context, gid uuid.UUID, at time.Time, errMsg string) error {
	u := s.ent.XGcalConnection.UpdateOneID(gid).SetLastError(errMsg)
	if errMsg == "" {
		u.SetLastSyncAt(at.UTC())
	}
	err := u.Exec(ctx)
	if xent.IsNotFound(err) {
		return ErrNotConnected
	}
	return err
}

// DeleteConnection removes the connection and all of the group's links. It never touches
// Google.
func (s *Store) DeleteConnection(ctx context.Context, gid uuid.UUID) error {
	if _, err := s.ent.XGcalLink.Delete().Where(xgcallink.GroupID(gid)).Exec(ctx); err != nil {
		return err
	}
	_, err := s.ent.XGcalConnection.Delete().Where(xgcalconnection.ID(gid)).Exec(ctx)
	return err
}

// ListConnections returns every group's connection (for the periodic reconciler).
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.ent.XGcalConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Connection, len(rows))
	for i, r := range rows {
		out[i] = toConnection(r)
	}
	return out, nil
}

// ---- links ----

// ListLinks returns all of the group's links.
func (s *Store) ListLinks(ctx context.Context, gid uuid.UUID) ([]Link, error) {
	rows, err := s.ent.XGcalLink.Query().Where(xgcallink.GroupID(gid)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Link, len(rows))
	for i, r := range rows {
		out[i] = toLink(r)
	}
	return out, nil
}

// GetLink returns the link for a source, or (zero, false).
func (s *Store) GetLink(ctx context.Context, gid uuid.UUID, sourceType string, sourceID uuid.UUID) (Link, bool, error) {
	row, err := s.ent.XGcalLink.Query().Where(
		xgcallink.GroupID(gid), xgcallink.SourceTypeEQ(xgcallink.SourceType(sourceType)), xgcallink.SourceID(sourceID),
	).Only(ctx)
	if xent.IsNotFound(err) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, err
	}
	return toLink(row), true, nil
}

// UpsertLink creates the link for a source or updates its event ID and hash.
func (s *Store) UpsertLink(ctx context.Context, gid uuid.UUID, sourceType string, sourceID uuid.UUID, eventID, hash string, at time.Time) error {
	row, err := s.ent.XGcalLink.Query().Where(
		xgcallink.GroupID(gid), xgcallink.SourceTypeEQ(xgcallink.SourceType(sourceType)), xgcallink.SourceID(sourceID),
	).Only(ctx)
	switch {
	case xent.IsNotFound(err):
		return s.ent.XGcalLink.Create().
			SetGroupID(gid).SetSourceType(xgcallink.SourceType(sourceType)).SetSourceID(sourceID).
			SetEventID(eventID).SetContentHash(hash).SetSyncedAt(at.UTC()).Exec(ctx)
	case err != nil:
		return err
	}
	return row.Update().SetEventID(eventID).SetContentHash(hash).SetSyncedAt(at.UTC()).Exec(ctx)
}

// DeleteLink removes one link (no error if absent).
func (s *Store) DeleteLink(ctx context.Context, gid uuid.UUID, sourceType string, sourceID uuid.UUID) error {
	_, err := s.ent.XGcalLink.Delete().Where(
		xgcallink.GroupID(gid), xgcallink.SourceTypeEQ(xgcallink.SourceType(sourceType)), xgcallink.SourceID(sourceID),
	).Exec(ctx)
	return err
}

// DeleteLinks removes all of the group's links and returns how many there were.
func (s *Store) DeleteLinks(ctx context.Context, gid uuid.UUID) (int, error) {
	return s.ent.XGcalLink.Delete().Where(xgcallink.GroupID(gid)).Exec(ctx)
}

func toConnection(r *xent.XGcalConnection) Connection {
	return Connection{
		GroupID: r.ID, ConnectedBy: r.ConnectedBy, GoogleEmail: r.GoogleEmail,
		CalendarID: r.CalendarID, CalendarSummary: r.CalendarSummary,
		IncludeCompleted: r.IncludeCompleted, IncludeProjectSteps: r.IncludeProjectSteps,
		ReminderMinutes: r.ReminderMinutes, Broken: r.Broken, LastSyncAt: r.LastSyncAt,
		LastError: r.LastError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		encToken: r.EncRefreshToken,
	}
}

func toLink(r *xent.XGcalLink) Link {
	return Link{
		ID: r.ID, GroupID: r.GroupID, SourceType: string(r.SourceType), SourceID: r.SourceID,
		EventID: r.EventID, ContentHash: r.ContentHash, SyncedAt: r.SyncedAt,
	}
}

func sortCalendars(c []Calendar) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Primary != c[j].Primary {
			return c[i].Primary
		}
		return c[i].Summary < c[j].Summary
	})
}
