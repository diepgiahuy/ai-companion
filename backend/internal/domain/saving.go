package domain

import (
	"fmt"
	"time"
)

const (
	BasisBudgetHeadroom = "budget_headroom"
	BasisSpendOnly      = "spend_only"

	StatusBudgetHeadroomCoversTarget = "budget_headroom_covers_target"
	StatusBudgetHeadroomBelowTarget  = "budget_headroom_below_target"
	StatusOverBudget                 = "over_budget"
	StatusInsufficientData           = "insufficient_data"
	StatusNoActiveGoal               = "no_active_goal"

	MinSavingsTargetVND int64 = 1
	MaxSavingsTargetVND int64 = 1_000_000_000_000 // 1 Trillion VND upper boundary
)

// ValidateSavingsTarget ensures the target is within finite Product-v1 bounds
// and prevents integer arithmetic overflow during budget/headroom calculations.
func ValidateSavingsTarget(targetVND int64) error {
	if targetVND < MinSavingsTargetVND {
		return fmt.Errorf("savings target must be at least %d VND", MinSavingsTargetVND)
	}
	if targetVND > MaxSavingsTargetVND {
		return fmt.Errorf("savings target exceeds maximum allowable %d VND", MaxSavingsTargetVND)
	}
	return nil
}

// CalculatePeriodBounds returns the [start, end) interval for a standard cadence relative to now.
func CalculatePeriodBounds(period string, now time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	t := now.In(loc)
	switch period {
	case "weekly":
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday -> 7
		}
		startDay := t.AddDate(0, 0, -(weekday - 1))
		start := time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, loc)
		end := start.AddDate(0, 0, 7)
		return start.UTC(), end.UTC()
	case "daily":
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		end := start.AddDate(0, 0, 1)
		return start.UTC(), end.UTC()
	default: // "monthly"
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0)
		return start.UTC(), end.UTC()
	}
}

// CalculateSavingsProgress derives truthful savings goal progress dynamically at read time.
// It never invents external income, bank account balance, or unrecorded cash savings.
func CalculateSavingsProgress(goal *SavingsGoal, period string, start, end time.Time, spentVND int64, budgetVND *int64) SavingsProgress {
	p := SavingsProgress{
		Goal:        goal,
		Period:      period,
		PeriodStart: start,
		PeriodEnd:   end,
		SpentVND:    spentVND,
		BudgetVND:   budgetVND,
	}

	if budgetVND == nil {
		p.Basis = BasisSpendOnly
		if goal == nil {
			p.Status = StatusNoActiveGoal
		} else {
			p.Status = StatusInsufficientData
		}
		return p
	}

	p.Basis = BasisBudgetHeadroom
	budget := *budgetVND
	rem := budget - spentVND
	p.BudgetRemainingVND = &rem

	if goal == nil {
		if rem < 0 {
			p.Status = StatusOverBudget
		} else {
			p.Status = StatusNoActiveGoal
		}
		return p
	}

	headroom := rem - goal.TargetVND
	p.HeadroomVsTargetVND = &headroom

	if rem < 0 {
		p.Status = StatusOverBudget
	} else if rem >= goal.TargetVND {
		p.Status = StatusBudgetHeadroomCoversTarget
	} else {
		p.Status = StatusBudgetHeadroomBelowTarget
	}

	return p
}
