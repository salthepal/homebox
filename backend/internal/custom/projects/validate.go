package projects

import (
	"fmt"
	"math"
	"strings"
)

// Limits shared by every caller (they live here, not in the API layer, so the service and
// any future adapter get the same answers).
const (
	// maxTextLen is the longest project name / step title. The columns are VARCHAR(255)
	// (characters on Postgres), but ent's MaxLen validator compares len(), i.e. bytes, and
	// rejects with a plain validation error (a 500 for us). We apply ent's stricter byte rule
	// up front so the user always gets a 400. A name of 255 ASCII characters or about 127
	// two-byte characters is accepted.
	maxTextLen = 255
	// maxMoney is the exclusive upper bound: NUMERIC(14,2) holds 12 integer digits.
	maxMoney = 1e12
)

// checkText validates an already-trimmed required text field against maxTextLen bytes.
func checkText(field, v string) error {
	if v == "" {
		return fmt.Errorf("%w: %s required", ErrInvalid, field)
	}
	if len(v) > maxTextLen {
		return fmt.Errorf("%w: %s must be at most %d bytes", ErrInvalid, field, maxTextLen)
	}
	return nil
}

// ValidateMoney checks an optional amount: finite, not negative, below 1e12 and with at most
// two decimal places. It rejects instead of rounding so SQLite (float) and Postgres
// (NUMERIC(14,2)) always store identical values.
func ValidateMoney(field string, v *float64) error {
	if v == nil {
		return nil
	}
	x := *v
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return fmt.Errorf("%w: %s must be a finite number", ErrInvalid, field)
	}
	if x < 0 {
		return fmt.Errorf("%w: %s must not be negative", ErrInvalid, field)
	}
	if x >= maxMoney {
		return fmt.Errorf("%w: %s must be less than %.0f", ErrInvalid, field, maxMoney)
	}
	// Scale to cents and allow only floating-point noise (a few ulps, never less than 1e-9),
	// so 80.55 and 0.1+0.2 pass while 80.555 does not.
	s := x * 100
	if math.Abs(s-math.Round(s)) > math.Max(1e-9, s*1e-15) {
		return fmt.Errorf("%w: %s must have at most 2 decimal places", ErrInvalid, field)
	}
	return nil
}

// money normalises a validated amount to exactly two decimals before it is stored.
func money(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := math.Round(*v*100) / 100
	return &r
}

func validateProjectInput(in ProjectInput) error {
	if err := checkText("name", strings.TrimSpace(in.Name)); err != nil {
		return err
	}
	if in.Priority < 0 || in.Priority > 3 {
		return fmt.Errorf("%w: priority 0-3", ErrInvalid)
	}
	if in.Status != "" && !validProjectStatus(in.Status) {
		return fmt.Errorf("%w: status %q", ErrInvalid, in.Status)
	}
	return ValidateMoney("budget", in.Budget)
}

func validateStep(in StepInput) error {
	if err := checkText("title", strings.TrimSpace(in.Title)); err != nil {
		return err
	}
	if in.Status != "" && !validStepStatus(in.Status) {
		return fmt.Errorf("%w: step status %q", ErrInvalid, in.Status)
	}
	if err := ValidateMoney("est_cost", in.EstCost); err != nil {
		return err
	}
	return ValidateMoney("actual_cost", in.ActualCost)
}
