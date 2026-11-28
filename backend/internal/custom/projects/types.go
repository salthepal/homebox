// Package projects is the repository (and later service) for the Projects feature: ordered
// multi-step upgrades linked to upstream items (.fork/PLAN.md §6).
//
// Every repository method takes the caller's group ID and filters by it, so one group can
// never read or modify another group's data. Upstream IDs (group, entity, maintenance entry)
// are plain UUIDs; this package never touches upstream tables (R4).
package projects

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a row doesn't exist or belongs to another group.
var ErrNotFound = errors.New("projects: not found")

// ErrInvalid is returned for input that fails validation (bad status, empty title, ...).
var ErrInvalid = errors.New("projects: invalid input")

// Project statuses.
const (
	StatusIdea       = "idea"
	StatusPlanning   = "planning"
	StatusInProgress = "in_progress"
	StatusOnHold     = "on_hold"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// Step statuses.
const (
	StepTodo    = "todo"
	StepDoing   = "doing"
	StepBlocked = "blocked"
	StepDone    = "done"
	StepSkipped = "skipped"
)

// Project is a project with its steps and linked item IDs loaded.
type Project struct {
	ID          uuid.UUID
	GroupID     uuid.UUID
	Name        string
	Description string
	Status      string
	Priority    int
	TargetDate  *time.Time
	Budget      *float64
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Steps       []Step
	EntityIDs   []uuid.UUID
}

// Step is one ordered step of a project.
type Step struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	Position           int
	Title              string
	Notes              string
	Status             string
	DueDate            *time.Time
	EstCost            *float64
	ActualCost         *float64
	URL                *string
	EntityID           *uuid.UUID
	LogAsMaintenance   bool
	MaintenanceEntryID *uuid.UUID
	CompletedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ProjectInput is the editable part of a project (create and full-replace update).
// Empty Status means "idea" on create and "keep current" on update.
type ProjectInput struct {
	Name        string
	Description string
	Status      string
	Priority    int
	TargetDate  *time.Time
	Budget      *float64
}

// StepInput is the editable part of a step (create and full-replace update).
// Empty Status means "todo" on create and "keep current" on update.
// Changing Status to done sets CompletedAt, and leaving done clears it.
type StepInput struct {
	Title            string
	Notes            string
	Status           string
	DueDate          *time.Time
	EstCost          *float64
	ActualCost       *float64
	URL              *string
	EntityID         *uuid.UUID
	LogAsMaintenance bool
}

// ListFilter narrows ListProjects. Empty Status means all statuses.
type ListFilter struct {
	Status string
}

// StepCompletion is applied by CompleteStep.
type StepCompletion struct {
	ActualCost         *float64
	LogAsMaintenance   bool
	MaintenanceEntryID *uuid.UUID
	At                 time.Time
}

func validProjectStatus(s string) bool {
	switch s {
	case StatusIdea, StatusPlanning, StatusInProgress, StatusOnHold, StatusDone, StatusCancelled:
		return true
	}
	return false
}

func validStepStatus(s string) bool {
	switch s {
	case StepTodo, StepDoing, StepBlocked, StepDone, StepSkipped:
		return true
	}
	return false
}
