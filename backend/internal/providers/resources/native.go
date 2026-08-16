package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
)

type Native struct {
	store        domain.ReadRepositories
	conversation *conversationctx.Service
	location     *time.Location
}

func NewNative(data domain.ReadRepositories, conversation *conversationctx.Service, location *time.Location) *Native {
	if location == nil {
		location = time.UTC
	}
	return &Native{store: data, conversation: conversation, location: location}
}

func (n *Native) Schemes() []string {
	return []string{"expenses", "budget", "saving", "reminders", "timers", "notes", "journal", "voicememos", "conversation"}
}

func (n *Native) List(context.Context) ([]capability.ResourceDescriptor, error) {
	return []capability.ResourceDescriptor{
		{URI: "expenses://today", Name: "Today's expenses", Description: "Expense total and items for the current local day", MIMEType: "application/json"},
		{URI: "expenses://yesterday", Name: "Yesterday's expenses", Description: "Expense total and items for the previous local day", MIMEType: "application/json"},
		{URI: "expenses://week/current", Name: "Current week expenses", Description: "Expense total and items for the current local week", MIMEType: "application/json"},
		{URI: "expenses://month/current", Name: "Current month expenses", Description: "Expense total and items for the current local month", MIMEType: "application/json"},
		{URI: "budget://daily", Name: "Daily budget", Description: "Current daily spending limit", MIMEType: "application/json"},
		{URI: "budget://weekly", Name: "Weekly budget", Description: "Current weekly spending limit", MIMEType: "application/json"},
		{URI: "budget://monthly", Name: "Monthly budget", Description: "Current monthly spending limit", MIMEType: "application/json"},
		{URI: "saving://current", Name: "Current savings goal and progress", Description: "Current active savings target and derived budget headroom progress", MIMEType: "application/json"},
		{URI: "reminders://today", Name: "Today's reminders", Description: "Pending reminders due today", MIMEType: "application/json"},
		{URI: "reminders://upcoming", Name: "Upcoming reminders", Description: "Pending scheduled reminders", MIMEType: "application/json"},
		{URI: "timers://active", Name: "Active timers", Description: "Pending timers with remaining seconds", MIMEType: "application/json"},
		{URI: "notes://recent", Name: "Recent notes", Description: "Recently saved notes", MIMEType: "application/json"},
		{URI: "journal://today", Name: "Today's journal", Description: "Journal entries from the current local day", MIMEType: "application/json"},
		{URI: "journal://yesterday", Name: "Yesterday's journal", Description: "Journal entries from the previous local day", MIMEType: "application/json"},
		{URI: "journal://recent", Name: "Recent journal", Description: "Recent journal entries across past days", MIMEType: "application/json"},
		{URI: "voicememos://recent", Name: "Recent voice memos", Description: "Recently recorded voice memos with transcripts and durations", MIMEType: "application/json"},
		{URI: "voicememos://today", Name: "Today's voice memos", Description: "Voice memos recorded during the current local day", MIMEType: "application/json"},
		{URI: "conversation://recent", Name: "Recent conversation", Description: "Bounded hot/durable working conversation context", MIMEType: "application/json"},
	}, nil
}

func (n *Native) Read(ctx context.Context, uri *url.URL) (capability.Resource, error) {
	if n.store == nil {
		return capability.Resource{}, fmt.Errorf("native resource store is unavailable")
	}
	userID, deviceID, threadID := "", "", "default"
	if turn, ok := pipeline.CurrentTurn(ctx); ok {
		userID, deviceID, threadID = turn.UserID, turn.DeviceID, turn.ThreadID
		if strings.TrimSpace(userID) == "" {
			userID = deviceID
		}
		if strings.TrimSpace(threadID) == "" {
			threadID = "default"
		}
	}
	now := time.Now().In(resolveLocation(ctx, n.location))
	limit := queryLimit(uri, 10)
	search := strings.TrimSpace(uri.Query().Get("search"))
	var value any
	var err error

	switch strings.ToLower(uri.Scheme) {
	case "expenses":
		var from, to time.Time
		switch resourceKey(uri) {
		case "today":
			from, to = dayRange(now)
		case "yesterday":
			from, to = dayRange(now.AddDate(0, 0, -1))
		case "week/current":
			from, to = weekRange(now)
		case "month/current":
			from, to = monthRange(now)
		default:
			return capability.Resource{}, fmt.Errorf("unsupported expense resource %q", uri.String())
		}
		total, totalErr := n.store.ExpenseTotal(ctx, userID, from, to)
		if totalErr != nil {
			return capability.Resource{}, totalErr
		}
		items, listErr := n.store.ListExpenses(ctx, userID, from, to, "", limit)
		if listErr != nil {
			return capability.Resource{}, listErr
		}
		value = map[string]any{"from": from, "to": to, "total_vnd": total, "expenses": items}

	case "budget":
		period := resourceKey(uri)
		if period != "daily" && period != "weekly" && period != "monthly" {
			return capability.Resource{}, fmt.Errorf("unsupported budget resource %q", uri.String())
		}
		amount, found, lookupErr := n.store.BudgetLimit(ctx, userID, period)
		if lookupErr != nil {
			return capability.Resource{}, lookupErr
		}
		value = map[string]any{"period": period, "set": found, "limit_vnd": amount}

	case "saving":
		period := "monthly"
		if p := resourceKey(uri); p != "" && p != "current" {
			period = p
		}
		loc := resolveLocation(ctx, n.location)
		start, end := domain.CalculatePeriodBounds(period, now, loc)
		spent, totalErr := n.store.ExpenseTotal(ctx, userID, start, end)
		if totalErr != nil {
			return capability.Resource{}, totalErr
		}
		bLimit, bSet, bErr := n.store.BudgetLimit(ctx, userID, period)
		if bErr != nil {
			return capability.Resource{}, bErr
		}
		var bPtr *int64
		if bSet {
			bPtr = &bLimit
		}
		goal, gSet, gErr := n.store.GetSavingsGoal(ctx, userID, period)
		if gErr != nil {
			return capability.Resource{}, gErr
		}
		var gPtr *domain.SavingsGoal
		if gSet {
			gPtr = &goal
		}
		value = domain.CalculateSavingsProgress(gPtr, period, start, end, spent, bPtr)

	case "reminders":
		items, listErr := n.store.ListReminders(ctx, userID, deviceID, "active", 100)
		if listErr != nil {
			return capability.Resource{}, listErr
		}
		filtered := make([]domain.ScheduledItem, 0, len(items))
		switch resourceKey(uri) {
		case "today":
			from, to := dayRange(now)
			for _, item := range items {
				if item.Kind != "timer" && !item.FireAt.Before(from) && item.FireAt.Before(to) {
					filtered = append(filtered, item)
				}
			}
		case "upcoming":
			for _, item := range items {
				if item.Kind != "timer" && item.FireAt.After(now) {
					filtered = append(filtered, item)
				}
			}
		default:
			return capability.Resource{}, fmt.Errorf("unsupported reminder resource %q", uri.String())
		}
		value = map[string]any{"reminders": trimReminders(filtered, limit)}

	case "timers":
		if resourceKey(uri) != "active" {
			return capability.Resource{}, fmt.Errorf("unsupported timer resource %q", uri.String())
		}
		items, listErr := n.store.ListTimers(ctx, userID, deviceID, "active", limit)
		if listErr != nil {
			return capability.Resource{}, listErr
		}
		type activeTimer struct {
			domain.ScheduledItem
			RemainingSeconds int64 `json:"remaining_seconds"`
		}
		active := make([]activeTimer, 0, len(items))
		for _, item := range items {
			remaining := item.PausedRemainingSeconds
			if item.Status != "paused" {
				remaining = int64(time.Until(item.FireAt).Seconds())
			}
			if remaining < 0 {
				remaining = 0
			}
			active = append(active, activeTimer{ScheduledItem: item, RemainingSeconds: remaining})
		}
		value = map[string]any{"timers": active}

	case "notes":
		if resourceKey(uri) != "recent" {
			return capability.Resource{}, fmt.Errorf("unsupported note resource %q", uri.String())
		}
		if search != "" {
			value, err = n.store.QueryNotes(ctx, userID, domain.NoteQuery{Search: search, Limit: limit})
		} else {
			value, err = n.store.ListNotes(ctx, userID, limit)
		}

	case "journal":
		switch resourceKey(uri) {
		case "today":
			from, to := dayRange(now)
			value, err = n.store.ListJournal(ctx, userID, from, to, limit)
		case "yesterday":
			from, to := dayRange(now.AddDate(0, 0, -1))
			value, err = n.store.ListJournal(ctx, userID, from, to, limit)
		case "recent":
			from := now.AddDate(0, 0, -30)
			to := now.AddDate(0, 0, 1)
			value, err = n.store.ListJournal(ctx, userID, from, to, limit)
		default:
			return capability.Resource{}, fmt.Errorf("unsupported journal resource %q", uri.String())
		}

	case "voicememos":
		switch resourceKey(uri) {
		case "today":
			from, to := dayRange(now)
			value, err = n.store.QueryVoiceMemos(ctx, userID, domain.VoiceMemoQuery{
				DeviceID: deviceID,
				From:     from,
				To:       to,
				Search:   search,
				Limit:    limit,
			})
		case "recent":
			if search != "" {
				value, err = n.store.QueryVoiceMemos(ctx, userID, domain.VoiceMemoQuery{
					DeviceID: deviceID,
					Search:   search,
					Limit:    limit,
				})
			} else {
				value, err = n.store.ListVoiceMemos(ctx, userID, deviceID, limit)
			}
		default:
			return capability.Resource{}, fmt.Errorf("unsupported voicememos resource %q", uri.String())
		}

	case "conversation":
		if resourceKey(uri) != "recent" {
			return capability.Resource{}, fmt.Errorf("unsupported conversation resource %q", uri.String())
		}
		if n.conversation == nil {
			return capability.Resource{}, fmt.Errorf("conversation resource provider is unavailable")
		}
		value, err = n.conversation.Recent(ctx, conversationctx.Scope{UserID: userID, ThreadID: threadID}, limit)

	default:
		return capability.Resource{}, fmt.Errorf("unsupported resource scheme %q", uri.Scheme)
	}
	if err != nil {
		return capability.Resource{}, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return capability.Resource{}, err
	}
	return capability.Resource{URI: uri.String(), MIMEType: "application/json", Text: string(payload)}, nil
}

func resourceKey(uri *url.URL) string {
	return strings.Trim(strings.TrimSpace(uri.Host+uri.Path), "/")
}

func queryLimit(uri *url.URL, fallback int) int {
	value := uri.Query().Get("limit")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > 20 {
		return 20
	}
	return parsed
}

func dayRange(now time.Time) (time.Time, time.Time) {
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return from, from.AddDate(0, 0, 1)
}

func weekRange(now time.Time) (time.Time, time.Time) {
	dayStart, _ := dayRange(now)
	offset := (int(dayStart.Weekday()) + 6) % 7 // Monday = 0
	from := dayStart.AddDate(0, 0, -offset)
	return from, from.AddDate(0, 0, 7)
}

func monthRange(now time.Time) (time.Time, time.Time) {
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return from, from.AddDate(0, 1, 0)
}

func trimReminders(items []domain.ScheduledItem, limit int) []domain.ScheduledItem {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func resolveLocation(ctx context.Context, defaultLoc *time.Location) *time.Location {
	if turn, ok := pipeline.CurrentTurn(ctx); ok && strings.TrimSpace(turn.Timezone) != "" {
		if loc, err := time.LoadLocation(strings.TrimSpace(turn.Timezone)); err == nil {
			return loc
		}
	}
	if defaultLoc != nil {
		return defaultLoc
	}
	return time.UTC
}

