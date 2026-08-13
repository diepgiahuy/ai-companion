#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
native = ROOT / "backend/internal/providers/tools/native.go"
text = native.read_text(encoding="utf-8")
old = '''\t\t\titems := make([]domain.ExpenseInput, 0, len(a.Items))
\t\t\tvar total int64
\t\t\tfor i, x := range a.Items {
\t\t\t\tat, err := parseTime(x.OccurredAt, fmt.Sprintf("items[%d].occurred_at", i))
\t\t\t\tif err != nil {
\t\t\t\t\treturn capability.Failure(err)
\t\t\t\t}
\t\t\t\titems = append(items, domain.ExpenseInput{AmountVND: x.AmountVND, Category: normalizeCategory(x.Category), Description: strings.TrimSpace(x.Description), OccurredAt: at})
\t\t\t\ttotal += x.AmountVND
\t\t\t}
\t\t\trequest, err := durableMutationRequest(ctx, "expense.log", r.Key, items)
'''
new = '''\t\t\titems := make([]domain.ExpenseInput, 0, len(a.Items))
\t\t\thashItems := make([]map[string]any, 0, len(a.Items))
\t\t\tvar total int64
\t\t\tfor i, x := range a.Items {
\t\t\t\tat, err := parseTime(x.OccurredAt, fmt.Sprintf("items[%d].occurred_at", i))
\t\t\t\tif err != nil {
\t\t\t\t\treturn capability.Failure(err)
\t\t\t\t}
\t\t\t\tcategory := normalizeCategory(x.Category)
\t\t\t\tdescription := strings.TrimSpace(x.Description)
\t\t\t\titems = append(items, domain.ExpenseInput{AmountVND: x.AmountVND, Category: category, Description: description, OccurredAt: at})
\t\t\t\thashItems = append(hashItems, map[string]any{"amount_vnd": x.AmountVND, "category": category, "description": description, "occurred_at": at.UTC().Format(time.RFC3339Nano)})
\t\t\t\ttotal += x.AmountVND
\t\t\t}
\t\t\trequest, err := durableMutationRequest(ctx, "expense.log", r.Key, hashItems)
'''
if text.count(old) != 1:
    raise SystemExit(f"expense.log canonicalization target drifted: {text.count(old)} matches")
native.write_text(text.replace(old, new), encoding="utf-8")

test = ROOT / "backend/internal/providers/tools/idempotency_native_test.go"
t = test.read_text(encoding="utf-8")
anchor = '''func TestDurableMutationRequiresAuthenticatedUserActor(t *testing.T) {'''
if t.count(anchor) != 1:
    raise SystemExit("idempotency native test anchor drifted")
case = r'''func TestExpenseLogCanonicalizesEquivalentTimeOffsets(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "expense-offset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	registry := capability.NewToolRegistry()
	if err := RegisterNative(registry, NativeDependencies{Store: data}); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})

	first := registry.Execute(ctx, "expense.log", capability.ToolRequest{Key: "expense-offset-key", Arguments: `{"items":[{"amount_vnd":50000,"category":"food","description":"lunch","occurred_at":"2030-01-01T15:00:00+07:00"}]}`})
	if !strings.Contains(first.Content, `"ok":true`) {
		t.Fatalf("first expense.log = %s", first.Content)
	}
	replayed := registry.Execute(ctx, "expense.log", capability.ToolRequest{Key: "expense-offset-key", Arguments: `{"items":[{"amount_vnd":50000,"category":"food","description":"lunch","occurred_at":"2030-01-01T08:00:00Z"}]}`})
	if !strings.Contains(replayed.Content, `"ok":true`) {
		t.Fatalf("equivalent-offset retry = %s", replayed.Content)
	}
	conflict := registry.Execute(ctx, "expense.log", capability.ToolRequest{Key: "expense-offset-key", Arguments: `{"items":[{"amount_vnd":60000,"category":"food","description":"lunch","occurred_at":"2030-01-01T08:00:00Z"}]}`})
	if !strings.Contains(conflict.Content, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("different-amount retry = %s", conflict.Content)
	}

	from := time.Date(2030, 1, 1, 7, 0, 0, 0, time.UTC)
	to := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	items, err := data.ListExpenses(ctx, "user-a", from, to, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AmountVND != 50000 {
		t.Fatalf("expenses = %+v; want one original 50000 VND row", items)
	}
}

'''
test.write_text(t.replace(anchor, case + anchor), encoding="utf-8")
print("expense.log canonical idempotency patch applied")
