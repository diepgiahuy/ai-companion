package contextengine

import (
	"context"
	"testing"
)

func TestRouterSelectsSmallDomainPacks(t *testing.T) {
	r := New(nil)
	p := r.Plan(context.Background(), "Tuần này tiêu hết bao nhiêu rồi?")
	if len(p.Packs) != 2 || p.Packs[0] != "expense" || p.Packs[1] != "budget" {
		t.Fatalf("packs=%v", p.Packs)
	}
}
func TestRouterFallsBackToAllPacksForGeneralConversation(t *testing.T) {
	r := New(nil)
	p := r.Plan(context.Background(), "Hôm nay bạn khỏe không?")
	if len(p.Packs) != 7 {
		t.Fatalf("packs=%v", p.Packs)
	}
}

func TestRouterExposesDestructiveContextPackOnlyForExplicitHistoryClear(t *testing.T) {
	r := New(nil)
	p := r.Plan(context.Background(), "Xóa lịch sử hội thoại này")
	if len(p.Packs) != 1 || p.Packs[0] != "context" {
		t.Fatalf("packs=%v", p.Packs)
	}
}

func TestRoutesMarketAndMemoryWithoutExposingEverything(t *testing.T) {
	r := New(nil)
	p := r.Plan(context.Background(), "giá vàng XAU hiện tại")
	if len(p.Packs) != 1 || p.Packs[0] != "market" {
		t.Fatalf("market packs=%v", p.Packs)
	}
	p = r.Plan(context.Background(), "nhớ là tôi thích trà")
	found := false
	for _, x := range p.Packs {
		if x == "memory" {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory packs=%v", p.Packs)
	}
}

func TestRouterDeterministicOrdering(t *testing.T) {
	r := New(nil)
	queries := []string{
		"Hôm nay tôi tiêu bao nhiêu?",
		"Set timer for 10 minutes and remind me to check oven",
		"Dat muc tieu tiet kiem 5tr va check budget",
		"Xin chào bạn khỏe không?",
	}

	for _, q := range queries {
		first := r.Plan(context.Background(), q)
		for i := 0; i < 5; i++ {
			again := r.Plan(context.Background(), q)
			if len(first.Packs) != len(again.Packs) {
				t.Fatalf("nondeterministic pack count for %q: %v vs %v", q, first.Packs, again.Packs)
			}
			for j := range first.Packs {
				if first.Packs[j] != again.Packs[j] {
					t.Fatalf("nondeterministic pack order for %q at %d: %s != %s", q, j, first.Packs[j], again.Packs[j])
				}
			}
		}
	}
}

func TestRouterDestructiveVsInformationalDistinction(t *testing.T) {
	r := New(nil)

	// "lịch sử chi tiêu" contains "lịch sử" but NOT "xóa lịch sử", so it must route to expense/budget, NEVER context
	p := r.Plan(context.Background(), "Xem lại lịch sử chi tiêu tháng này")
	for _, pack := range p.Packs {
		if pack == "context" {
			t.Fatalf("informational expense history must not route to destructive context pack: %v", p.Packs)
		}
	}
	hasExpense := false
	for _, pack := range p.Packs {
		if pack == "expense" {
			hasExpense = true
		}
	}
	if !hasExpense {
		t.Fatalf("expected expense pack for 'lịch sử chi tiêu', got %v", p.Packs)
	}

	// Destructive commands MUST route exclusively to context
	destructiveQueries := []string{
		"xóa lịch sử hội thoại",
		"xoá lịch sử",
		"clear history",
		"delete conversation",
		"xóa sạch toàn bộ lịch sử",
		"clear all conversation",
	}
	for _, dq := range destructiveQueries {
		dp := r.Plan(context.Background(), dq)
		if len(dp.Packs) != 1 || dp.Packs[0] != "context" {
			t.Fatalf("destructive query %q routed to unexpected packs: %v", dq, dp.Packs)
		}
	}
}

func TestRouterMultilingualRouting(t *testing.T) {
	r := New(nil)

	tests := []struct {
		name      string
		query     string
		mustPacks []string
	}{
		{
			name:      "VN saving goal",
			query:     "Mục tiêu tiết kiệm tháng này là 10 triệu",
			mustPacks: []string{"saving", "budget"},
		},
		{
			name:      "EN timer creation",
			query:     "Set a timer for 15 minutes",
			mustPacks: []string{"schedule"},
		},
		{
			name:      "Mixed budget check",
			query:     "Check budget va expense hom nay",
			mustPacks: []string{"expense", "budget"},
		},
		{
			name:      "VN voice memo",
			query:     "Ghi âm cuộc họp này",
			mustPacks: []string{"voice"},
		},
		{
			name:      "EN market inquiry",
			query:     "What is the gold price and bitcoin rate today?",
			mustPacks: []string{"market"},
		},
		{
			name:      "VN note finding",
			query:     "Tìm ghi chú về hợp đồng",
			mustPacks: []string{"note"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := r.Plan(context.Background(), tt.query)
			have := map[string]bool{}
			for _, p := range plan.Packs {
				have[p] = true
			}
			for _, must := range tt.mustPacks {
				if !have[must] {
					t.Fatalf("query %q: missing expected pack %q in %v", tt.query, must, plan.Packs)
				}
			}
		})
	}
}

