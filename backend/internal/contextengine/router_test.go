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
