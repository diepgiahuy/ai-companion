#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
native = ROOT / "backend/internal/providers/tools/native.go"
text = native.read_text(encoding="utf-8")

repls = []
def replace(old, new, count=1):
    global text
    actual = text.count(old)
    if actual != count:
        raise SystemExit(f"native.go drift: expected {count} occurrences, found {actual}: {old[:100]!r}")
    text = text.replace(old, new)

replace("\tStore         domain.Repositories\n", "\tStore         domain.DurableRepositories\n")

replace('''\t\t\tif err = d.Store.CreateExpense(ctx, currentUser(ctx), r.Key, a.AmountVND, normalizeCategory(a.Category), strings.TrimSpace(a.Description), at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcategory := normalizeCategory(a.Category)\n\t\t\tdescription := strings.TrimSpace(a.Description)\n\t\t\trequest, err := durableMutationRequest(ctx, "expense.create", r.Key, map[string]any{"amount_vnd": a.AmountVND, "category": category, "description": description, "occurred_at": at.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err = d.Store.CreateExpenseMutation(ctx, request, currentUser(ctx), a.AmountVND, category, description, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err := d.Store.CreateExpenses(ctx, currentUser(ctx), r.Key, items); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\trequest, err := durableMutationRequest(ctx, "expense.log", r.Key, items)\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.CreateExpensesMutation(ctx, request, currentUser(ctx), items); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err = d.Store.UpdateExpense(ctx, currentUser(ctx), a.ID, a.Amount, normalizeCategory(a.Category), a.Description, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcategory := normalizeCategory(a.Category)\n\t\t\tdescription := strings.TrimSpace(a.Description)\n\t\t\trequest, err := durableMutationRequest(ctx, "expense.update", r.Key, map[string]any{"id": a.ID, "amount_vnd": a.Amount, "category": category, "description": description, "occurred_at": at.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err = d.Store.UpdateExpenseMutation(ctx, request, currentUser(ctx), a.ID, a.Amount, category, description, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\tdefine("expense", "expense.delete", "Xóa một khoản chi theo id", obj(map[string]any{"id": idField}, "id"), deleteID(func(ctx context.Context, u string, id int64) error { return d.Store.DeleteExpense(ctx, u, id) }, "expense")),\n''', '''\t\tdefine("expense", "expense.delete", "Xóa một khoản chi theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("expense.delete", "expense", func(ctx context.Context, request idempotency.Request, u string, id int64) error { return d.Store.DeleteExpenseMutation(ctx, request, u, id) })),\n''')

replace('''\t\t\tif err := d.Store.SetBudget(ctx, currentUser(ctx), a.Period, a.Limit); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tperiod := strings.ToLower(strings.TrimSpace(a.Period))\n\t\t\trequest, err := durableMutationRequest(ctx, "budget.set", r.Key, map[string]any{"period": period, "limit_vnd": a.Limit})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.SetBudgetMutation(ctx, request, currentUser(ctx), period, a.Limit); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\ta.Period = period\n''')

replace('''\t\t\tif err := d.Store.DeleteBudget(ctx, currentUser(ctx), a.Period); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tperiod := strings.ToLower(strings.TrimSpace(a.Period))\n\t\t\trequest, err := durableMutationRequest(ctx, "budget.delete", r.Key, map[string]any{"period": period})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.DeleteBudgetMutation(ctx, request, currentUser(ctx), period); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\ta.Period = period\n''')

replace('''\t\t\tif err := d.Store.CreateNote(ctx, currentUser(ctx), r.Key, a.Content); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcontent := strings.TrimSpace(a.Content)\n\t\t\trequest, err := durableMutationRequest(ctx, "note.create", r.Key, map[string]any{"content": content})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.CreateNoteMutation(ctx, request, currentUser(ctx), content); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err := d.Store.UpdateNote(ctx, currentUser(ctx), a.ID, a.Content); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcontent := strings.TrimSpace(a.Content)\n\t\t\trequest, err := durableMutationRequest(ctx, "note.update", r.Key, map[string]any{"id": a.ID, "content": content})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.UpdateNoteMutation(ctx, request, currentUser(ctx), a.ID, content); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\tdefine("note", "note.delete", "Xóa ghi chú", obj(map[string]any{"id": idField}, "id"), deleteID(func(ctx context.Context, u string, id int64) error { return d.Store.DeleteNote(ctx, u, id) }, "note")),\n''', '''\t\tdefine("note", "note.delete", "Xóa ghi chú", obj(map[string]any{"id": idField}, "id"), deleteMutationID("note.delete", "note", func(ctx context.Context, request idempotency.Request, u string, id int64) error { return d.Store.DeleteNoteMutation(ctx, request, u, id) })),\n''')

replace('''\t\t\tif err := d.Store.CreateJournal(ctx, currentUser(ctx), r.Key, a.Content, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcontent := strings.TrimSpace(a.Content)\n\t\t\trequest, err := durableMutationRequest(ctx, "journal.create", r.Key, map[string]any{"content": content, "occurred_at": at.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.CreateJournalMutation(ctx, request, currentUser(ctx), content, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err := d.Store.UpdateJournal(ctx, currentUser(ctx), a.ID, a.Content, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\tcontent := strings.TrimSpace(a.Content)\n\t\t\trequest, err := durableMutationRequest(ctx, "journal.update", r.Key, map[string]any{"id": a.ID, "content": content, "occurred_at": at.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.UpdateJournalMutation(ctx, request, currentUser(ctx), a.ID, content, at); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\tdefine("journal", "journal.delete", "Xóa nhật ký", obj(map[string]any{"id": idField}, "id"), deleteID(func(ctx context.Context, u string, id int64) error { return d.Store.DeleteJournal(ctx, u, id) }, "journal")),\n''', '''\t\tdefine("journal", "journal.delete", "Xóa nhật ký", obj(map[string]any{"id": idField}, "id"), deleteMutationID("journal.delete", "journal", func(ctx context.Context, request idempotency.Request, u string, id int64) error { return d.Store.DeleteJournalMutation(ctx, request, u, id) })),\n''')

replace('''\t\t\tfire := d.Now().Add(time.Duration(a.Delay) * time.Second)\n\t\t\tif err := d.Store.CreateTimerForDevice(ctx, currentUser(ctx), r.Key, currentDevice(ctx), a.Title, fire); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\treturn capability.Success(map[string]any{"saved": "timer", "fire_at": fire.UTC().Format(time.RFC3339), "delay_seconds": a.Delay})\n''', '''\t\t\ta.Title = strings.TrimSpace(a.Title)\n\t\t\trequest, err := durableMutationRequest(ctx, "timer.create", r.Key, map[string]any{"title": a.Title, "delay_seconds": a.Delay})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tfire := d.Now().Add(time.Duration(a.Delay) * time.Second)\n\t\t\titem, err := d.Store.CreateTimerMutation(ctx, request, currentUser(ctx), currentDevice(ctx), a.Title, fire)\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\treturn capability.Success(map[string]any{"saved": "timer", "fire_at": item.FireAt.UTC().Format(time.RFC3339), "delay_seconds": a.Delay})\n''')

replace('''\t\t\tif err := d.Store.CreateReminderForDevice(ctx, currentUser(ctx), r.Key, currentDevice(ctx), a.Title, fire); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\treturn capability.Success(map[string]any{"saved": "reminder", "fire_at": fire.UTC().Format(time.RFC3339)})\n''', '''\t\t\ttitle := strings.TrimSpace(a.Title)\n\t\t\trequest, err := durableMutationRequest(ctx, "reminder.create", r.Key, map[string]any{"title": title, "fire_at": fire.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\titem, err := d.Store.CreateReminderMutation(ctx, request, currentUser(ctx), currentDevice(ctx), title, fire)\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\treturn capability.Success(map[string]any{"saved": "reminder", "fire_at": item.FireAt.UTC().Format(time.RFC3339)})\n''')

replace('''\t\t\tif err := d.Store.UpdateScheduledItem(ctx, currentUser(ctx), a.ID, a.Title, fire); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\ttitle := strings.TrimSpace(a.Title)\n\t\t\trequest, err := durableMutationRequest(ctx, "schedule.update", r.Key, map[string]any{"id": a.ID, "title": title, "fire_at": fire.UTC().Format(time.RFC3339Nano)})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.UpdateScheduledMutation(ctx, request, currentUser(ctx), a.ID, title, fire); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err := d.Store.PauseTimer(ctx, currentUser(ctx), a.ID, d.Now()); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\trequest, err := durableMutationRequest(ctx, "timer.pause", r.Key, map[string]any{"id": a.ID})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.PauseTimerMutation(ctx, request, currentUser(ctx), a.ID, d.Now()); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\t\tif err := d.Store.ResumeTimer(ctx, currentUser(ctx), a.ID, d.Now()); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''', '''\t\t\trequest, err := durableMutationRequest(ctx, "timer.resume", r.Key, map[string]any{"id": a.ID})\n\t\t\tif err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n\t\t\tif err := d.Store.ResumeTimerMutation(ctx, request, currentUser(ctx), a.ID, d.Now()); err != nil {\n\t\t\t\treturn capability.Failure(err)\n\t\t\t}\n''')

replace('''\t\tdefine("schedule", "schedule.cancel", "Hủy timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteID(func(ctx context.Context, u string, id int64) error { return d.Store.CancelScheduledItem(ctx, u, id) }, "scheduled_item")),\n\t\tdefine("schedule", "schedule.delete", "Xóa timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteID(func(ctx context.Context, u string, id int64) error { return d.Store.DeleteScheduledItem(ctx, u, id) }, "scheduled_item")),\n''', '''\t\tdefine("schedule", "schedule.cancel", "Hủy timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("schedule.cancel", "scheduled_item", func(ctx context.Context, request idempotency.Request, u string, id int64) error { return d.Store.CancelScheduledMutation(ctx, request, u, id) })),\n\t\tdefine("schedule", "schedule.delete", "Xóa timer/reminder theo id", obj(map[string]any{"id": idField}, "id"), deleteMutationID("schedule.delete", "scheduled_item", func(ctx context.Context, request idempotency.Request, u string, id int64) error { return d.Store.DeleteScheduledMutation(ctx, request, u, id) })),\n''')

replace('''func deleteID(fn func(context.Context, string, int64) error, label string) func(context.Context, capability.ToolRequest) capability.ToolResult {\n''', '''func deleteMutationID(operation, label string, fn func(context.Context, idempotency.Request, string, int64) error) func(context.Context, capability.ToolRequest) capability.ToolResult {\n''')
replace('''\t\tif err := fn(ctx, currentUser(ctx), a.ID); err != nil {\n\t\t\treturn capability.Failure(err)\n\t\t}\n''', '''\t\trequest, err := durableMutationRequest(ctx, operation, r.Key, map[string]any{"id": a.ID})\n\t\tif err != nil {\n\t\t\treturn capability.Failure(err)\n\t\t}\n\t\tif err := fn(ctx, request, currentUser(ctx), a.ID); err != nil {\n\t\t\treturn capability.Failure(err)\n\t\t}\n''', count=1)

# Add the idempotency import only once.
replace('''\t"companion-server/internal/domain"\n\t"companion-server/internal/pipeline"\n''', '''\t"companion-server/internal/domain"\n\t"companion-server/internal/idempotency"\n\t"companion-server/internal/pipeline"\n''')
native.write_text(text, encoding="utf-8")

# Ledger table is additive during this slice. Final #27 hard-cut moves this to
# Store.Open and removes the lazy compatibility call.
run = ROOT / "backend/internal/store/idempotency_run.go"
rtext = run.read_text(encoding="utf-8")
old = '''func (s *Store) runIdempotentMutation(ctx context.Context, request idempotency.Request, mutate func(*sql.Tx) (any, error)) (idempotentOutcome, error) {\n\trequest.Actor = strings.TrimSpace(request.Actor)'''
new = '''func (s *Store) runIdempotentMutation(ctx context.Context, request idempotency.Request, mutate func(*sql.Tx) (any, error)) (idempotentOutcome, error) {\n\tif err := s.migrateIdempotency(); err != nil {\n\t\treturn idempotentOutcome{}, err\n\t}\n\trequest.Actor = strings.TrimSpace(request.Actor)'''
if rtext.count(old) != 1:
    raise SystemExit("idempotency_run.go drifted")
run.write_text(rtext.replace(old, new), encoding="utf-8")

# Public durable mutations require authenticated UserID. Keep DeviceID for target.
test = ROOT / "backend/internal/providers/tools/native_test.go"
ttext = test.read_text(encoding="utf-8")
old = 'pipeline.TurnContext{DeviceID: "device-a"}'
new = 'pipeline.TurnContext{UserID: "user-a", DeviceID: "device-a"}'
if ttext.count(old) != 1:
    raise SystemExit("native_test context drifted")
ttext = ttext.replace(old, new)
ttext = ttext.replace('data.ListTimers(ctx, "device-a", "device-a", "pending", 10)', 'data.ListTimers(ctx, "user-a", "device-a", "pending", 10)')
test.write_text(ttext, encoding="utf-8")

print("issue27 native durable mutation patch applied")
