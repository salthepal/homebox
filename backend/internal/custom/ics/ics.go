// Package ics renders the read-only calendar feed (.fork/PLAN.md G4): a hand-written
// RFC 5545 writer (no new dependencies, R6) plus the per-group revocable feed token.
package ics

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sysadminsmedia/homebox/backend/internal/custom/gcal"
)

// maxLine is the RFC 5545 §3.1 limit on a content line, in octets, excluding CRLF.
const maxLine = 75

// CalendarName is the X-WR-CALNAME shown by calendar clients.
const CalendarName = "Homebox Maintenance"

// Event is one all-day VEVENT.
type Event struct {
	UID         string
	Start       time.Time // first day (date only)
	End         time.Time // exclusive end day (date only)
	Summary     string
	Description string
	// ReminderMinutes adds a display VALARM that many minutes before the event; 0 = none.
	ReminderMinutes int
}

// FromDesired converts a calendar event Homebox would write to Google into a feed event.
// ok is false when the event isn't an all-day event with parseable dates.
func FromDesired(d gcal.Desired) (Event, bool) {
	ev := d.Event
	if ev.Start == nil || ev.End == nil {
		return Event{}, false
	}
	start, err := time.Parse("2006-01-02", ev.Start.Date)
	if err != nil {
		return Event{}, false
	}
	end, err := time.Parse("2006-01-02", ev.End.Date)
	if err != nil || !end.After(start) {
		return Event{}, false
	}
	out := Event{
		UID:         d.SourceType + "-" + d.SourceID.String() + "@homebox",
		Start:       start,
		End:         end,
		Summary:     ev.Summary,
		Description: ev.Description,
	}
	if ev.Reminders != nil {
		for _, o := range ev.Reminders.Overrides {
			if o.Minutes > 0 {
				out.ReminderMinutes = o.Minutes
				break
			}
		}
	}
	return out, true
}

// Render writes a complete VCALENDAR. now is the DTSTAMP of every event. Output order is
// deterministic (start date, then UID) so clients and tests see stable feeds.
func Render(name string, events []Event, now time.Time) []byte {
	sorted := append([]Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Start.Equal(sorted[j].Start) {
			return sorted[i].Start.Before(sorted[j].Start)
		}
		return sorted[i].UID < sorted[j].UID
	})

	var b bytes.Buffer
	line := func(s string) { writeFolded(&b, s) }

	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Homebox//Fork Calendar Feed//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + EscapeText(name))
	line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	line("X-PUBLISHED-TTL:PT1H")
	stamp := now.UTC().Format("20060102T150405Z")
	for _, e := range sorted {
		line("BEGIN:VEVENT")
		line("UID:" + EscapeText(e.UID))
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + e.Start.Format("20060102"))
		line("DTEND;VALUE=DATE:" + e.End.Format("20060102"))
		line("SUMMARY:" + EscapeText(e.Summary))
		if e.Description != "" {
			line("DESCRIPTION:" + EscapeText(e.Description))
		}
		line("TRANSP:TRANSPARENT")
		if e.ReminderMinutes > 0 {
			line("BEGIN:VALARM")
			line("ACTION:DISPLAY")
			line("DESCRIPTION:" + EscapeText(e.Summary))
			line("TRIGGER:-PT" + strconv.Itoa(e.ReminderMinutes) + "M")
			line("END:VALARM")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.Bytes()
}

// EscapeText escapes a TEXT value (RFC 5545 §3.3.11): backslash, semicolon, comma and
// newlines. Carriage returns are dropped (CRLF/CR become a single \n).
func EscapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		default:
			if r < 0x20 && r != '\t' {
				continue // other control characters aren't allowed in a content line
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeFolded writes s as CRLF-terminated content lines of at most maxLine octets,
// continuing with a leading space (RFC 5545 §3.1). It never splits a UTF-8 sequence.
func writeFolded(b *bytes.Buffer, s string) {
	limit := maxLine
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 { // can't happen with limit >= 4, but never loop forever
			cut = limit
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = maxLine - 1 // the leading space counts toward the limit
	}
	b.WriteString(s)
	b.WriteString("\r\n")
}
