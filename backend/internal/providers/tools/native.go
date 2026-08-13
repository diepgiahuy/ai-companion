package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
	"companion-server/internal/pipeline"
	"companion-server/internal/recording"
)

type ConversationControl interface {
	Append(context.Context, string, conversationctx.Scope, string, string) error
	Clear(context.Context, conversationctx.Scope) error
}

type VoicePrivacy interface {
	VoiceAudioAllowed(context.Context, string) bool
}

type NativeDependencies struct {
	Store         domain.DurableRepositories
	Conversation  ConversationControl
	Resources     *capability.ResourceRegistry
	VoicePrivacy  VoicePrivacy
	RecordingsDir string
	Now           func() time.Time
}

func RegisterNative(registry *capability.ToolRegistry, d NativeDependencies) error {
	if registry == nil || d.Store == nil {
		return fmt.Errorf("tool registry and store are required")
	}
	if d.RecordingsDir == "" {
		d.RecordingsDir = "data/recordings"
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	for _, t := range nativeTools(d) {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}

func nativeTools(d NativeDependencies) []capability.Tool {
	obj := func(p map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": p, "required": req, "additionalProperties": false}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	idField := map[string]any{"type": "integer", "minimum": 1}
	limitField := map[string]any{"type": "integer", "minimum": 1, "maximum": 20}
	periodField := map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}}
	statusField := map[string]any{"type": "string", "enum": []string{"active", "pending", "paused", "dispatching", "sent", "fired", "cancelled", "all"}}
	define := func(pack, name, desc string, params map[string]any, h func(context.Context, capability.ToolRequest) capability.ToolResult) capability.Tool {
		risk := "read"
		if strings.Contains(name, ".create") || strings.Contains(name, ".log") || strings.Contains(name, ".update") || strings.Contains(name, ".set") || strings.Contains(name, ".pause") || strings.Contains(name, ".resume") {
			risk = "write"
		}
		if strings.Contains(name, ".delete") || strings.Contains(name, ".clear") || strings.Contains(name, ".cancel") {
			risk = "destructive"
		}
		return capability.FunctionTool{ToolName: name, ToolDefinition: &capability.ToolDefinition{Name: name, Description: desc, Parameters: params, Pack: pack, Risk: risk}, Handler: h}
	}
	hidden := func(name string, h func(context.Context, capability.ToolRequest) capability.ToolResult) capability.Tool {
		return capability.FunctionTool{ToolName: name, Handler: h}
	}
	parseTime := func(v, label string) (time.Time, error) {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", label, err)
		}
		return t, nil
	}

	expenseItem := obj(map[string]any{"amount_vnd": map[string]any{"type": "integer", "minimum": 1, "maximum": 1_000_000_000}, "category": map[string]any{"type": "string", "enum": []string{"food", "transport", "health", "bills", "shopping", "other"}}, "description": str("Mô tả ngắn"), "occurred_at": str("RFC3339")}, "amount_vnd", "category", "description", "occurred_at")
	tools := []capability.Tool{
		hidden("expense.create", func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				AmountVND   int64  `json:"amount_vnd"`
				Category    string `json:"category"`
				Description string `json:"description"`
				OccurredAt  string `json:"occurred_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			at, err := parseTime(a.OccurredAt, "occurred_at")
			if err != nil {
				return capability.Failure(err)
			}
			category := normalizeCategory(a.Category)
			description := strings.TrimSpace(a.Description)
			request, err := durableMutationRequest(ctx, "expense.create", r.Key, map[string]any{"amount_vnd": a.AmountVND, "category": category, "description": description, "occurred_at": at.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			if err = d.Store.CreateExpenseMutation(ctx, request, currentUser(ctx), a.AmountVND, category, description, at); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "expense", "amount_vnd": a.AmountVND})
		}),
		define("expense", "expense.log", "Ghi một hoặc nhiều khoản chi", obj(map[string]any{"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": expenseItem}}, "items"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Items []struct {
					AmountVND   int64  `json:"amount_vnd"`
					Category    string `json:"category"`
					Description string `json:"description"`
					OccurredAt  string `json:"occurred_at"`
				} `json:"items"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			items := make([]domain.ExpenseInput, 0, len(a.Items))
			var total int64
			for i, x := range a.Items {
				at, err := parseTime(x.OccurredAt, fmt.Sprintf("items[%d].occurred_at", i))
				if err != nil {
					return capability.Failure(err)
				}
				items = append(items, domain.ExpenseInput{AmountVND: x.AmountVND, Category: normalizeCategory(x.Category), Description: strings.TrimSpace(x.Description), OccurredAt: at})
				total += x.AmountVND
			}
			request, err := durableMutationRequest(ctx, "expense.log", r.Key, items)
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.CreateExpensesMutation(ctx, request, currentUser(ctx), items); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "expenses", "count": len(items), "total_vnd": total})
		}),
		define("expense", "expense.query", "Tổng chi trong khoảng và budget còn lại", obj(map[string]any{"from": str("RFC3339"), "to": str("RFC3339"), "period": periodField}, "from", "to"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				From   string `json:"from"`
				To     string `json:"to"`
				Period string `json:"period"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			from, to, err := parseRange(a.From, a.To, 366*24*time.Hour)
			if err != nil {
				return capability.Failure(err)
			}
			if a.Period == "" {
				a.Period = "weekly"
			}
			u := currentUser(ctx)
			total, err := d.Store.ExpenseTotal(ctx, u, from, to)
			if err != nil {
				return capability.Failure(err)
			}
			limit, ok, err := d.Store.BudgetLimit(ctx, u, a.Period)
			if err != nil {
				return capability.Failure(err)
			}
			remaining := int64(0)
			if ok {
				remaining = limit - total
			}
			res := capability.Success(map[string]any{"period": a.Period, "from": from, "to": to, "total_vnd": total, "budget_set": ok, "budget_limit_vnd": limit, "remaining_vnd": remaining})
			res.Presentation = expenseCard(total, limit, remaining, a.Period)
			return res
		}),
		define("expense", "expense.summary", "Tính tổng chi trong khoảng", obj(map[string]any{"from": str("RFC3339"), "to": str("RFC3339")}, "from", "to"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			from, to, err := parseRange(a.From, a.To, 366*24*time.Hour)
			if err != nil {
				return capability.Failure(err)
			}
			total, err := d.Store.ExpenseTotal(ctx, currentUser(ctx), from, to)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"total_vnd": total, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)})
		}),
		define("expense", "expense.list", "Liệt kê khoản chi", obj(map[string]any{"from": str("RFC3339"), "to": str("RFC3339"), "category": str("optional"), "limit": limitField}, "from", "to"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				From     string `json:"from"`
				To       string `json:"to"`
				Category string `json:"category"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			from, to, err := parseRange(a.From, a.To, 366*24*time.Hour)
			if err != nil {
				return capability.Failure(err)
			}
			x, err := d.Store.ListExpenses(ctx, currentUser(ctx), from, to, strings.TrimSpace(a.Category), a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"expenses": x})
		}),
		define("expense", "expense.update", "Sửa một khoản chi theo id", obj(map[string]any{"id": idField, "amount_vnd": map[string]any{"type": "integer", "minimum": 1}, "category": str("category"), "description": str("description"), "occurred_at": str("RFC3339")}, "id", "amount_vnd", "category", "description", "occurred_at"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID          int64  `json:"id"`
				Amount      int64  `json:"amount_vnd"`
				Category    string `json:"category"`
				Description string `json:"description"`
				Occurred    string `json:"occurred_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			at, err := parseTime(a.Occurred, "occurred_at")
			if err != nil {
				return capability.Failure(err)
			}
			category := normalizeCategory(a.Category)
			description := strings.TrimSpace(a.Description)
			request, err := durableMutationRequest(ctx, "expense.update", r.Key, map[string]any{"id": a.ID, "amount_vnd": a.Amount, "category": category, "description": description, "occurred_at": at.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			if err = d.Store.UpdateExpenseMutation(ctx, request, currentUser(ctx), a.ID, a.Amount, category, description, at); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"updated": "expense", "id": a.ID})
		}),
		define("expense", "expense.delete", "Xóa một khoản chi theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("expense.delete", "expense", func(ctx context.Context, request idempotency.Request, u string, id int64) error {
			return d.Store.DeleteExpenseMutation(ctx, request, u, id)
		})),

		define("budget", "budget.get", "Đọc budget ngày/tuần/tháng", obj(map[string]any{"period": periodField}, "period"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Period string `json:"period"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			v, ok, err := d.Store.BudgetLimit(ctx, currentUser(ctx), a.Period)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"period": a.Period, "set": ok, "limit_vnd": v})
		}),
		define("budget", "budget.set", "Đặt hoặc cập nhật budget", obj(map[string]any{"period": periodField, "limit_vnd": map[string]any{"type": "integer", "minimum": 0}}, "period", "limit_vnd"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Period string `json:"period"`
				Limit  int64  `json:"limit_vnd"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			period := strings.ToLower(strings.TrimSpace(a.Period))
			request, err := durableMutationRequest(ctx, "budget.set", r.Key, map[string]any{"period": period, "limit_vnd": a.Limit})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.SetBudgetMutation(ctx, request, currentUser(ctx), period, a.Limit); err != nil {
				return capability.Failure(err)
			}
			a.Period = period
			return capability.Success(map[string]any{"period": a.Period, "limit_vnd": a.Limit})
		}),
		define("budget", "budget.delete", "Xóa budget", obj(map[string]any{"period": periodField}, "period"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Period string `json:"period"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			period := strings.ToLower(strings.TrimSpace(a.Period))
			request, err := durableMutationRequest(ctx, "budget.delete", r.Key, map[string]any{"period": period})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.DeleteBudgetMutation(ctx, request, currentUser(ctx), period); err != nil {
				return capability.Failure(err)
			}
			a.Period = period
			return capability.Success(map[string]any{"deleted": "budget", "period": a.Period})
		}),

		define("note", "note.create", "Lưu ghi chú", obj(map[string]any{"content": str("Nội dung")}, "content"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			content := strings.TrimSpace(a.Content)
			request, err := durableMutationRequest(ctx, "note.create", r.Key, map[string]any{"content": content})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.CreateNoteMutation(ctx, request, currentUser(ctx), content); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "note"})
		}),
		define("note", "note.list", "Liệt kê ghi chú", obj(map[string]any{"limit": limitField}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			x, err := d.Store.ListNotes(ctx, currentUser(ctx), a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"notes": x})
		}),
		define("note", "note.update", "Sửa ghi chú", obj(map[string]any{"id": idField, "content": str("Nội dung mới")}, "id", "content"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID      int64  `json:"id"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			content := strings.TrimSpace(a.Content)
			request, err := durableMutationRequest(ctx, "note.update", r.Key, map[string]any{"id": a.ID, "content": content})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.UpdateNoteMutation(ctx, request, currentUser(ctx), a.ID, content); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"updated": "note", "id": a.ID})
		}),
		define("note", "note.delete", "Xóa ghi chú", obj(map[string]any{"id": idField}, "id"), deleteMutationID("note.delete", "note", func(ctx context.Context, request idempotency.Request, u string, id int64) error {
			return d.Store.DeleteNoteMutation(ctx, request, u, id)
		})),

		define("journal", "journal.create", "Lưu nhật ký", obj(map[string]any{"content": str("Nội dung"), "occurred_at": str("RFC3339")}, "content", "occurred_at"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Content  string `json:"content"`
				Occurred string `json:"occurred_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			at, err := parseTime(a.Occurred, "occurred_at")
			if err != nil {
				return capability.Failure(err)
			}
			content := strings.TrimSpace(a.Content)
			request, err := durableMutationRequest(ctx, "journal.create", r.Key, map[string]any{"content": content, "occurred_at": at.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.CreateJournalMutation(ctx, request, currentUser(ctx), content, at); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "journal"})
		}),
		define("journal", "journal.list", "Liệt kê nhật ký", obj(map[string]any{"from": str("RFC3339"), "to": str("RFC3339"), "limit": limitField}, "from", "to"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				From  string `json:"from"`
				To    string `json:"to"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			from, to, err := parseRange(a.From, a.To, 366*24*time.Hour)
			if err != nil {
				return capability.Failure(err)
			}
			x, err := d.Store.ListJournal(ctx, currentUser(ctx), from, to, a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"journal": x})
		}),
		define("journal", "journal.update", "Sửa nhật ký", obj(map[string]any{"id": idField, "content": str("Nội dung"), "occurred_at": str("RFC3339")}, "id", "content", "occurred_at"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID       int64  `json:"id"`
				Content  string `json:"content"`
				Occurred string `json:"occurred_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			at, err := parseTime(a.Occurred, "occurred_at")
			if err != nil {
				return capability.Failure(err)
			}
			content := strings.TrimSpace(a.Content)
			request, err := durableMutationRequest(ctx, "journal.update", r.Key, map[string]any{"id": a.ID, "content": content, "occurred_at": at.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.UpdateJournalMutation(ctx, request, currentUser(ctx), a.ID, content, at); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"updated": "journal", "id": a.ID})
		}),
		define("journal", "journal.delete", "Xóa nhật ký", obj(map[string]any{"id": idField}, "id"), deleteMutationID("journal.delete", "journal", func(ctx context.Context, request idempotency.Request, u string, id int64) error {
			return d.Store.DeleteJournalMutation(ctx, request, u, id)
		})),

		define("schedule", "timer.create", "Tạo timer tương đối", obj(map[string]any{"title": str("Nội dung"), "delay_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 604800}}, "delay_seconds"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Title string `json:"title"`
				Delay int64  `json:"delay_seconds"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			if a.Delay < 1 || a.Delay > 604800 {
				return capability.Failure(fmt.Errorf("delay_seconds out of range"))
			}
			if strings.TrimSpace(a.Title) == "" {
				a.Title = "Hết giờ"
			}
			a.Title = strings.TrimSpace(a.Title)
			request, err := durableMutationRequest(ctx, "timer.create", r.Key, map[string]any{"title": a.Title, "delay_seconds": a.Delay})
			if err != nil {
				return capability.Failure(err)
			}
			fire := d.Now().Add(time.Duration(a.Delay) * time.Second)
			item, err := d.Store.CreateTimerMutation(ctx, request, currentUser(ctx), currentDevice(ctx), a.Title, fire)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "timer", "fire_at": item.FireAt.UTC().Format(time.RFC3339), "delay_seconds": a.Delay})
		}),
		define("schedule", "reminder.create", "Tạo lời nhắc tuyệt đối", obj(map[string]any{"title": str("Nội dung"), "fire_at": str("RFC3339")}, "title", "fire_at"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Title string `json:"title"`
				Fire  string `json:"fire_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			fire, err := parseTime(a.Fire, "fire_at")
			if err != nil {
				return capability.Failure(err)
			}
			title := strings.TrimSpace(a.Title)
			request, err := durableMutationRequest(ctx, "reminder.create", r.Key, map[string]any{"title": title, "fire_at": fire.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			item, err := d.Store.CreateReminderMutation(ctx, request, currentUser(ctx), currentDevice(ctx), title, fire)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "reminder", "fire_at": item.FireAt.UTC().Format(time.RFC3339)})
		}),
		define("schedule", "reminder.list", "Liệt kê lời nhắc", obj(map[string]any{"status": statusField, "limit": limitField}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			if a.Status == "" {
				a.Status = "active"
			}
			x, err := d.Store.ListReminders(ctx, currentUser(ctx), currentDevice(ctx), a.Status, a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"reminders": x})
		}),
		define("schedule", "timer.list", "Liệt kê timer", obj(map[string]any{"status": statusField, "limit": limitField}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			if a.Status == "" {
				a.Status = "active"
			}
			x, err := d.Store.ListTimers(ctx, currentUser(ctx), currentDevice(ctx), a.Status, a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"timers": x})
		}),
		define("schedule", "schedule.update", "Sửa timer/reminder theo id", obj(map[string]any{"id": idField, "title": str("Nội dung"), "fire_at": str("RFC3339")}, "id", "title", "fire_at"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID    int64  `json:"id"`
				Title string `json:"title"`
				Fire  string `json:"fire_at"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			fire, err := parseTime(a.Fire, "fire_at")
			if err != nil {
				return capability.Failure(err)
			}
			title := strings.TrimSpace(a.Title)
			request, err := durableMutationRequest(ctx, "schedule.update", r.Key, map[string]any{"id": a.ID, "title": title, "fire_at": fire.UTC().Format(time.RFC3339Nano)})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.UpdateScheduledMutation(ctx, request, currentUser(ctx), a.ID, title, fire); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"updated": "scheduled_item", "id": a.ID})
		}),
		define("schedule", "timer.pause", "Tạm dừng timer đang chạy theo id", obj(map[string]any{"id": idField}, "id"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			request, err := durableMutationRequest(ctx, "timer.pause", r.Key, map[string]any{"id": a.ID})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.PauseTimerMutation(ctx, request, currentUser(ctx), a.ID, d.Now()); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"paused": "timer", "id": a.ID})
		}),
		define("schedule", "timer.resume", "Tiếp tục timer đã tạm dừng theo id", obj(map[string]any{"id": idField}, "id"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			request, err := durableMutationRequest(ctx, "timer.resume", r.Key, map[string]any{"id": a.ID})
			if err != nil {
				return capability.Failure(err)
			}
			if err := d.Store.ResumeTimerMutation(ctx, request, currentUser(ctx), a.ID, d.Now()); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"resumed": "timer", "id": a.ID})
		}),
		define("schedule", "schedule.cancel", "Hủy timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("schedule.cancel", "scheduled_item", func(ctx context.Context, request idempotency.Request, u string, id int64) error {
			return d.Store.CancelScheduledMutation(ctx, request, u, id)
		})),
		define("schedule", "schedule.delete", "Xóa timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("schedule.delete", "scheduled_item", func(ctx context.Context, request idempotency.Request, u string, id int64) error {
			return d.Store.DeleteScheduledMutation(ctx, request, u, id)
		})),

		define("voice", "voice_memo.save", "Lưu audio lượt hiện tại thành WAV", obj(map[string]any{}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			turn, ok := pipeline.CurrentTurn(ctx)
			if !ok || len(turn.PCM16Mono) == 0 || turn.SampleRate <= 0 {
				return capability.Failure(fmt.Errorf("current turn audio unavailable"))
			}
			u := currentUser(ctx)
			if d.VoicePrivacy != nil && !d.VoicePrivacy.VoiceAudioAllowed(ctx, u) {
				return capability.Failure(fmt.Errorf("voice audio persistence disabled by user privacy policy"))
			}
			if x, ok, err := d.Store.VoiceMemoByKey(ctx, u, r.Key); err != nil {
				return capability.Failure(err)
			} else if ok {
				return capability.Success(map[string]any{"saved": "voice_memo", "id": x.ID, "duration_ms": x.DurationMS})
			}
			sum := sha256.Sum256([]byte(r.Key))
			path := filepath.Join(d.RecordingsDir, "memo-"+hex.EncodeToString(sum[:8])+".wav")
			if err := recording.WritePCM16MonoWAV(path, turn.PCM16Mono, turn.SampleRate); err != nil {
				return capability.Failure(err)
			}
			ms := int64(len(turn.PCM16Mono)/2) * 1000 / int64(turn.SampleRate)
			if err := d.Store.CreateVoiceMemo(ctx, u, r.Key, turn.DeviceID, path, turn.Transcript, ms); err != nil {
				_ = os.Remove(path)
				return capability.Failure(err)
			}
			x, _, err := d.Store.VoiceMemoByKey(ctx, u, r.Key)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"saved": "voice_memo", "id": x.ID, "duration_ms": ms})
		}),
		define("voice", "voice_memo.list", "Liệt kê voice memo", obj(map[string]any{"limit": limitField}), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			x, err := d.Store.ListVoiceMemos(ctx, currentUser(ctx), currentDevice(ctx), a.Limit)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"voice_memos": x})
		}),
		define("voice", "voice_memo.delete", "Xóa voice memo và file WAV", obj(map[string]any{"id": idField}, "id"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			u := currentUser(ctx)
			memo, ok, err := d.Store.VoiceMemoByID(ctx, u, a.ID)
			if err != nil {
				return capability.Failure(err)
			}
			if !ok {
				return capability.Failure(fmt.Errorf("voice memo not found"))
			}
			if err := os.Remove(memo.Path); err != nil && !os.IsNotExist(err) {
				return capability.Failure(fmt.Errorf("delete voice memo file: %w", err))
			}
			if err := d.Store.DeleteVoiceMemo(ctx, u, a.ID); err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"deleted": "voice_memo", "id": a.ID})
		}),
	}
	if d.Conversation != nil {
		tools = append(tools, define("context", "conversation.clear", "Xóa lịch sử hội thoại của thread hiện tại khi người dùng yêu cầu rõ ràng", obj(map[string]any{"confirm": map[string]any{"type": "boolean", "const": true}}, "confirm"), func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				Confirm bool `json:"confirm"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			if !a.Confirm {
				return capability.Failure(fmt.Errorf("explicit confirmation is required"))
			}
			scope := currentConversationScope(ctx)
			if err := d.Conversation.Clear(ctx, scope); err != nil {
				return capability.Failure(err)
			}
			// Qwen appends the current user turn before tools execute. Preserve that
			// explicit clear request so the post-tool assistant response is not left
			// as an orphan message after clearing earlier history.
			if turn, ok := pipeline.CurrentTurn(ctx); ok && strings.TrimSpace(turn.Transcript) != "" {
				key := "conversation-clear:" + scope.Key() + ":" + strings.TrimSpace(turn.SessionID) + ":" + strings.TrimSpace(turn.TurnID)
				if err := d.Conversation.Append(ctx, key, scope, "user", turn.Transcript); err != nil {
					return capability.Failure(err)
				}
			}
			return capability.Success(map[string]any{"cleared": "conversation", "thread_id": scope.ThreadID})
		}))
	}
	if d.Resources != nil {
		tools = append(tools, hidden("resource.read", func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			var a struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
				return capability.Failure(err)
			}
			x, err := d.Resources.Read(ctx, a.URI)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"resource": x})
		}), hidden("resource.list", func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
			x, err := d.Resources.List(ctx)
			if err != nil {
				return capability.Failure(err)
			}
			return capability.Success(map[string]any{"resources": x})
		}))
	}
	return tools
}

func deleteMutationID(operation, label string, fn func(context.Context, idempotency.Request, string, int64) error) func(context.Context, capability.ToolRequest) capability.ToolResult {
	return func(ctx context.Context, r capability.ToolRequest) capability.ToolResult {
		var a struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(r.Arguments), &a); err != nil {
			return capability.Failure(err)
		}
		request, err := durableMutationRequest(ctx, operation, r.Key, map[string]any{"id": a.ID})
		if err != nil {
			return capability.Failure(err)
		}
		if err := fn(ctx, request, currentUser(ctx), a.ID); err != nil {
			return capability.Failure(err)
		}
		return capability.Success(map[string]any{"deleted": label, "id": a.ID})
	}
}
func currentUser(ctx context.Context) string {
	if t, ok := pipeline.CurrentTurn(ctx); ok {
		if strings.TrimSpace(t.UserID) != "" {
			return strings.TrimSpace(t.UserID)
		}
		return strings.TrimSpace(t.DeviceID)
	}
	return ""
}
func currentDevice(ctx context.Context) string {
	if t, ok := pipeline.CurrentTurn(ctx); ok {
		return strings.TrimSpace(t.DeviceID)
	}
	return ""
}
func currentConversationScope(ctx context.Context) conversationctx.Scope {
	if t, ok := pipeline.CurrentTurn(ctx); ok {
		userID := strings.TrimSpace(t.UserID)
		if userID == "" {
			userID = strings.TrimSpace(t.DeviceID)
		}
		threadID := strings.TrimSpace(t.ThreadID)
		if threadID == "" {
			threadID = "default"
		}
		return conversationctx.Scope{UserID: userID, ThreadID: threadID}
	}
	return conversationctx.Scope{UserID: "default", ThreadID: "default"}
}
func parseRange(a, b string, max time.Duration) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, a)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be RFC3339: %w", err)
	}
	to, err := time.Parse(time.RFC3339, b)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be RFC3339: %w", err)
	}
	if !to.After(from) || to.Sub(from) > max {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid range")
	}
	return from, to, nil
}
func normalizeCategory(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "food", "transport", "health", "bills", "shopping", "other":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "other"
	}
}
func expenseCard(total, limit, remaining int64, period string) *capability.Presentation {
	pct := 0
	if limit > 0 {
		pct = int(total * 100 / limit)
		if pct > 100 {
			pct = 100
		}
	}
	return &capability.Presentation{Kind: "expense_summary", Title: "Chi tiêu " + period, Primary: fmt.Sprintf("%d VND", total), Secondary: fmt.Sprintf("Còn %d / %d VND", remaining, limit), Progress: pct}
}
