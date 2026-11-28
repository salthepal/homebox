package gcal

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xproject"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/ent/xprojectstep"
)

// StepItem is one project step as the calendar needs it.
type StepItem struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	ProjectName string
	Title       string
	Notes       string
	URL         string
	Done        bool
	DueDate     time.Time
	EstCost     float64
	ActualCost  float64
}

// StepLister is the read side of project steps with due dates.
type StepLister interface {
	ListDueSteps(ctx context.Context, gid uuid.UUID) ([]StepItem, error)
}

// EntStepLister reads steps from the custom ent client (x_projects, x_project_steps).
type EntStepLister struct{ Ent *xent.Client }

// ListDueSteps returns the group's steps that have a due date, except skipped steps and
// steps of cancelled projects (nothing is going to happen for them).
func (l EntStepLister) ListDueSteps(ctx context.Context, gid uuid.UUID) ([]StepItem, error) {
	rows, err := l.Ent.XProjectStep.Query().
		Where(
			xprojectstep.DueDateNotNil(),
			xprojectstep.StatusNEQ(xprojectstep.StatusSkipped),
			xprojectstep.HasProjectWith(xproject.GroupID(gid), xproject.StatusNEQ(xproject.StatusCancelled)),
		).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]StepItem, 0, len(rows))
	for _, r := range rows {
		it := StepItem{
			ID: r.ID, ProjectID: r.ProjectID, Title: r.Title, Notes: r.Notes,
			Done: r.Status == xprojectstep.StatusDone, DueDate: *r.DueDate,
		}
		if r.Edges.Project != nil {
			it.ProjectName = r.Edges.Project.Name
		}
		if r.URL != nil {
			it.URL = *r.URL
		}
		if r.EstCost != nil {
			it.EstCost = *r.EstCost
		}
		if r.ActualCost != nil {
			it.ActualCost = *r.ActualCost
		}
		out = append(out, it)
	}
	return out, nil
}

// StepProvider turns project steps with a due date into desired calendar events (PLAN §7).
// It yields nothing while the connection's include_project_steps option is off, which makes
// the reconciler remove the events it created earlier.
type StepProvider struct {
	Source  StepLister
	BaseURL string // HBOX_X_PUBLIC_BASE_URL, for deep links
}

// SourceType implements SourceProvider.
func (StepProvider) SourceType() string { return SourceProjectStep }

// Desired implements SourceProvider.
func (p StepProvider) Desired(ctx context.Context, gid uuid.UUID, conn Connection) ([]Desired, error) {
	if !conn.IncludeProjectSteps {
		return nil, nil
	}
	items, err := p.Source.ListDueSteps(ctx, gid)
	if err != nil {
		return nil, err
	}
	out := make([]Desired, 0, len(items))
	for _, it := range items {
		out = append(out, StepDesired(it, conn, p.BaseURL))
	}
	return out, nil
}

// StepDesired maps a project step onto its calendar event: all-day on the due date, title
// "📋 project: step" (✅ once done, with no reminder), linking to /x/projects/{id}.
func StepDesired(s StepItem, conn Connection, baseURL string) Desired {
	icon := "📋"
	if s.Done {
		icon = "✅"
	}
	title := strings.TrimSpace(s.Title)
	if s.ProjectName != "" {
		title = s.ProjectName + ": " + title
	}

	var desc []string
	if n := strings.TrimSpace(s.Notes); n != "" {
		desc = append(desc, n)
	}
	switch {
	case s.Done && s.ActualCost > 0:
		desc = append(desc, "Cost: "+money(s.ActualCost))
	case !s.Done && s.EstCost > 0:
		desc = append(desc, "Estimated cost: "+money(s.EstCost))
	}
	if s.URL != "" {
		desc = append(desc, s.URL)
	}
	if baseURL != "" {
		desc = append(desc, strings.TrimRight(baseURL, "/")+"/x/projects/"+s.ProjectID.String())
	}

	rem := &Reminders{UseDefault: false}
	if !s.Done && conn.ReminderMinutes > 0 {
		rem.Overrides = []ReminderOverride{{Method: "popup", Minutes: conn.ReminderMinutes}}
	}

	start, end := allDay(s.DueDate)
	ev := Event{
		Summary:     icon + " " + title,
		Description: strings.Join(desc, "\n"),
		Start:       start,
		End:         end,
		Reminders:   rem,
		ExtendedProperties: &ExtendedProperties{Private: map[string]string{
			OwnerProperty: OwnerValue,
			IDProperty:    s.ID.String(),
			TypeProperty:  SourceProjectStep,
		}},
	}
	return Desired{SourceType: SourceProjectStep, SourceID: s.ID, Event: ev, Hash: EventHash(conn.CalendarID, ev)}
}
