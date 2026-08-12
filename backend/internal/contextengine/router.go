package contextengine

import (
	"context"
	"strings"

	"companion-server/internal/capability"
)

type Plan struct {
	Packs     []string
	Resources []capability.Resource
}

type Router struct{ resources *capability.ResourceRegistry }

func New(resources *capability.ResourceRegistry) *Router { return &Router{resources: resources} }

func (r *Router) Plan(ctx context.Context, query string) Plan {
	q := strings.ToLower(strings.TrimSpace(query))
	packs := map[string]bool{}
	var uris []string
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(q, w) {
				return true
			}
		}
		return false
	}
	// Destructive context controls use a narrow route and return immediately so
	// ambiguous words such as "lịch" in "lịch sử" cannot expose unrelated packs.
	if has("xóa lịch sử", "xoá lịch sử", "clear history", "delete conversation", "xóa hội thoại", "xoá hội thoại") {
		return Plan{Packs: []string{"context"}}
	}
	if has("tiêu", "chi ", "chi tiêu", "expense", "mua", "cafe", "cà phê", "ăn", "tiền") {
		packs["expense"] = true
		packs["budget"] = true
		switch {
		case has("hôm nay", "today"):
			uris = append(uris, "expenses://today", "budget://daily")
		case has("tháng", "month"):
			uris = append(uris, "expenses://month/current", "budget://monthly")
		default:
			uris = append(uris, "expenses://week/current", "budget://weekly")
		}
	}
	if has("budget", "ngân sách", "hạn mức", "goal chi") {
		packs["budget"] = true
		if has("hôm nay", "today", "daily", "mỗi ngày") {
			uris = append(uris, "budget://daily")
		} else if has("tháng", "month") {
			uris = append(uris, "budget://monthly")
		} else {
			uris = append(uris, "budget://weekly")
		}
	}
	if has("timer", "đếm ngược", "hẹn giờ") {
		packs["schedule"] = true
		uris = append(uris, "timers://active")
	}
	if has("nhắc", "reminder", "calendar", "schedule", "lịch hôm", "lịch sắp", "lịch tới", "lịch ngày", "lịch tuần", "cuộc hẹn", "appointment") {
		packs["schedule"] = true
		if has("hôm nay", "today") {
			uris = append(uris, "reminders://today")
		} else {
			uris = append(uris, "reminders://upcoming")
		}
	}
	if has("ghi chú", "note") {
		packs["note"] = true
		uris = append(uris, "notes://recent")
	}
	if has("nhật ký", "journal") {
		packs["journal"] = true
		uris = append(uris, "journal://today")
	}
	if has("ghi âm", "voice memo", "voice note", "recording") {
		packs["voice"] = true
	}
	if has("giá vàng", "xau", "gold", "market", "chứng khoán", "stock", "crypto", "bitcoin", "ethereum", "tỷ giá", "exchange rate", "usd/vnd") {
		packs["market"] = true
	}
	if has("nhớ", "ghi nhớ", "remember", "quên", "forget", "từng nói", "sở thích", "thích ", "ghét ", "mục tiêu", "personal memory") {
		packs["memory"] = true
	}
	// Unknown/general conversation keeps all native packs available for compatibility,
	// but avoids eagerly loading unrelated resources.
	if len(packs) == 0 {
		for _, p := range []string{"expense", "budget", "note", "journal", "schedule", "voice"} {
			packs[p] = true
		}
	}
	out := Plan{Packs: mapKeys(packs)}
	if r.resources != nil {
		seen := map[string]bool{}
		for _, uri := range uris {
			if seen[uri] {
				continue
			}
			seen[uri] = true
			if res, err := r.resources.Read(ctx, uri); err == nil {
				out.Resources = append(out.Resources, res)
			}
		}
	}
	return out
}
func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for _, k := range []string{"expense", "budget", "note", "journal", "schedule", "voice", "memory", "market", "context"} {
		if m[k] {
			out = append(out, k)
		}
	}
	return out
}
