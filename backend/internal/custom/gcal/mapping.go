package gcal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/upstream"
)

// TypeProperty is a private extended property naming the kind of source an event was made
// from (SourceMaintenance, SourceProjectStep). With IDProperty it lets links be rebuilt from
// the calendar when x_gcal_links is lost.
const TypeProperty = "homebox_type"

// Desired is one event Homebox wants on the calendar, derived from one source row.
type Desired struct {
	SourceType string
	SourceID   uuid.UUID
	Event      Event
	// Hash fingerprints Event and the target calendar. A link whose stored hash differs is
	// out of date and gets patched.
	Hash string
}

// SourceProvider produces the desired events of one source type for a group. G2 provides
// MaintenanceProvider; G3 adds project steps.
type SourceProvider interface {
	// SourceType is the link source_type this provider owns (SourceMaintenance, ...).
	SourceType() string
	// Desired returns the full desired set for the group under the connection's options.
	Desired(ctx context.Context, gid uuid.UUID, conn Connection) ([]Desired, error)
}

// allDay builds the start/end of an all-day event on the given day. Google's all-day end
// date is exclusive, hence the next day.
func allDay(t time.Time) (start, end *EventDate) {
	d := t.UTC().Truncate(24 * time.Hour)
	return &EventDate{Date: d.Format("2006-01-02")}, &EventDate{Date: d.AddDate(0, 0, 1).Format("2006-01-02")}
}

func money(v float64) string { return fmt.Sprintf("$%.2f", v) }

// MaintenanceDesired maps a maintenance entry onto its calendar event (PLAN §7).
//
//   - completed (has a completed date): all-day on that date, title "✅ item: task", no
//     reminders; skipped unless conn.IncludeCompleted. A completed entry that also has a
//     schedule shows on the completion date.
//   - scheduled and not completed: all-day on the scheduled date, title "🔧 item: task",
//     one popup reminder conn.ReminderMinutes before (none if 0).
//   - neither date: no event (ok == false).
//
// baseURL (HBOX_X_PUBLIC_BASE_URL) is used for the deep link to the item's maintenance tab.
func MaintenanceDesired(m upstream.MaintenanceItem, conn Connection, baseURL string) (Desired, bool) {
	var (
		when      time.Time
		completed bool
	)
	switch {
	case m.CompletedDate != nil:
		if !conn.IncludeCompleted {
			return Desired{}, false
		}
		when, completed = *m.CompletedDate, true
	case m.ScheduledDate != nil:
		when = *m.ScheduledDate
	default:
		return Desired{}, false
	}

	icon := "🔧"
	if completed {
		icon = "✅"
	}
	title := strings.TrimSpace(m.Name)
	if m.EntityName != "" {
		title = m.EntityName + ": " + title
	}

	var desc []string
	if d := strings.TrimSpace(m.Description); d != "" {
		desc = append(desc, d)
	}
	if m.Cost > 0 {
		label := "Estimated cost"
		if completed {
			label = "Cost"
		}
		desc = append(desc, label+": "+money(m.Cost))
	}
	if baseURL != "" {
		desc = append(desc, strings.TrimRight(baseURL, "/")+"/item/"+m.EntityID.String()+"/maintenance")
	}

	rem := &Reminders{UseDefault: false}
	if !completed && conn.ReminderMinutes > 0 {
		rem.Overrides = []ReminderOverride{{Method: "popup", Minutes: conn.ReminderMinutes}}
	}

	start, end := allDay(when)
	ev := Event{
		Summary:     icon + " " + title,
		Description: strings.Join(desc, "\n"),
		Start:       start,
		End:         end,
		Reminders:   rem,
		ExtendedProperties: &ExtendedProperties{Private: map[string]string{
			OwnerProperty: OwnerValue,
			IDProperty:    m.ID.String(),
			TypeProperty:  SourceMaintenance,
		}},
	}
	return Desired{
		SourceType: SourceMaintenance, SourceID: m.ID, Event: ev,
		Hash: EventHash(conn.CalendarID, ev),
	}, true
}

// EventHash fingerprints what Homebox writes for an event plus the calendar it lives on, so
// a changed calendar choice also counts as a change.
func EventHash(calendarID string, ev Event) string {
	b, _ := json.Marshal(struct {
		Calendar string
		Event    Event
	}{calendarID, ev})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
