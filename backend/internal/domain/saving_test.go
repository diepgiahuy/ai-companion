package domain

import (
	"testing"
	"time"
)

func TestCalculatePeriodBounds(t *testing.T) {
	ict, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		ict = time.FixedZone("ICT", 7*3600)
	}

	// 2026-08-16 21:00:00 ICT
	refTime := time.Date(2026, 8, 16, 21, 0, 0, 0, ict)

	// Monthly bounds in ICT
	startM, endM := CalculatePeriodBounds("monthly", refTime, ict)
	expectedStartM := time.Date(2026, 8, 1, 0, 0, 0, 0, ict).UTC()
	expectedEndM := time.Date(2026, 9, 1, 0, 0, 0, 0, ict).UTC()

	if !startM.Equal(expectedStartM) || !endM.Equal(expectedEndM) {
		t.Fatalf("monthly bounds mismatch: got [%v, %v), want [%v, %v)", startM, endM, expectedStartM, expectedEndM)
	}

	// Weekly bounds in ICT (Sunday Aug 16 -> Monday Aug 10 to Monday Aug 17)
	startW, endW := CalculatePeriodBounds("weekly", refTime, ict)
	expectedStartW := time.Date(2026, 8, 10, 0, 0, 0, 0, ict).UTC()
	expectedEndW := time.Date(2026, 8, 17, 0, 0, 0, 0, ict).UTC()
	if !startW.Equal(expectedStartW) || !endW.Equal(expectedEndW) {
		t.Fatalf("weekly bounds mismatch: got [%v, %v), want [%v, %v)", startW, endW, expectedStartW, expectedEndW)
	}
}

func TestCalculateSavingsProgress_NoGoalNoBudget(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	p := CalculateSavingsProgress(nil, "monthly", start, end, 500000, nil)

	if p.Basis != BasisSpendOnly {
		t.Fatalf("basis = %q, want %q", p.Basis, BasisSpendOnly)
	}
	if p.Status != StatusNoActiveGoal {
		t.Fatalf("status = %q, want %q", p.Status, StatusNoActiveGoal)
	}
	if p.SpentVND != 500000 {
		t.Fatalf("spent = %d, want 500000", p.SpentVND)
	}
	if p.BudgetVND != nil || p.BudgetRemainingVND != nil || p.HeadroomVsTargetVND != nil {
		t.Fatalf("expected nil pointers for missing budget & headroom")
	}
}

func TestCalculateSavingsProgress_GoalWithNoBudgetInsufficientData(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	goal := &SavingsGoal{
		UserID:    "user-1",
		Period:    "monthly",
		TargetVND: 3000000,
	}

	p := CalculateSavingsProgress(goal, "monthly", start, end, 1200000, nil)

	if p.Basis != BasisSpendOnly {
		t.Fatalf("basis = %q, want %q", p.Basis, BasisSpendOnly)
	}
	if p.Status != StatusInsufficientData {
		t.Fatalf("status = %q, want %q", p.Status, StatusInsufficientData)
	}
	if p.BudgetRemainingVND != nil || p.HeadroomVsTargetVND != nil {
		t.Fatalf("must not calculate savings progress without a budget baseline")
	}
}

func TestCalculateSavingsProgress_BudgetHeadroomCoversTarget(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	goal := &SavingsGoal{
		UserID:    "user-1",
		Period:    "monthly",
		TargetVND: 3000000,
	}
	budget := int64(10000000)
	spent := int64(6000000) // remaining = 4,000,000 >= 3,000,000

	p := CalculateSavingsProgress(goal, "monthly", start, end, spent, &budget)

	if p.Basis != BasisBudgetHeadroom {
		t.Fatalf("basis = %q, want %q", p.Basis, BasisBudgetHeadroom)
	}
	if p.Status != StatusBudgetHeadroomCoversTarget {
		t.Fatalf("status = %q, want %q", p.Status, StatusBudgetHeadroomCoversTarget)
	}
	if p.BudgetRemainingVND == nil || *p.BudgetRemainingVND != 4000000 {
		t.Fatalf("remaining = %v, want 4000000", p.BudgetRemainingVND)
	}
	if p.HeadroomVsTargetVND == nil || *p.HeadroomVsTargetVND != 1000000 {
		t.Fatalf("headroom vs target = %v, want 1000000", p.HeadroomVsTargetVND)
	}
}

func TestCalculateSavingsProgress_BudgetHeadroomBelowTarget(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	goal := &SavingsGoal{
		UserID:    "user-1",
		Period:    "monthly",
		TargetVND: 5000000,
	}
	budget := int64(10000000)
	spent := int64(7000000) // remaining = 3,000,000 < 5,000,000

	p := CalculateSavingsProgress(goal, "monthly", start, end, spent, &budget)

	if p.Basis != BasisBudgetHeadroom {
		t.Fatalf("basis = %q, want %q", p.Basis, BasisBudgetHeadroom)
	}
	if p.Status != StatusBudgetHeadroomBelowTarget {
		t.Fatalf("status = %q, want %q", p.Status, StatusBudgetHeadroomBelowTarget)
	}
	if p.HeadroomVsTargetVND == nil || *p.HeadroomVsTargetVND != -2000000 {
		t.Fatalf("headroom vs target = %v, want -2000000", p.HeadroomVsTargetVND)
	}
}

func TestCalculateSavingsProgress_OverBudget(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	goal := &SavingsGoal{
		UserID:    "user-1",
		Period:    "monthly",
		TargetVND: 2000000,
	}
	budget := int64(10000000)
	spent := int64(11500000) // remaining = -1,500,000

	p := CalculateSavingsProgress(goal, "monthly", start, end, spent, &budget)

	if p.Basis != BasisBudgetHeadroom {
		t.Fatalf("basis = %q, want %q", p.Basis, BasisBudgetHeadroom)
	}
	if p.Status != StatusOverBudget {
		t.Fatalf("status = %q, want %q", p.Status, StatusOverBudget)
	}
	if p.BudgetRemainingVND == nil || *p.BudgetRemainingVND != -1500000 {
		t.Fatalf("remaining = %v, want -1500000", p.BudgetRemainingVND)
	}
	if p.HeadroomVsTargetVND == nil || *p.HeadroomVsTargetVND != -3500000 {
		t.Fatalf("headroom vs target = %v, want -3500000", p.HeadroomVsTargetVND)
	}
}
