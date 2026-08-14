// Command companion-verify-tier1-store validates the deterministic Tier-1
// scenario against the authoritative PostgreSQL store and writes evidence.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"companion-server/internal/pgstore"
)

type evidence struct {
	SchemaVersion        int              `json:"schema_version"`
	EvidenceClass        string           `json:"evidence_class"`
	Promotion            string           `json:"promotion"`
	Result               string           `json:"result"`
	AuthoritativeStore   string           `json:"authoritative_store"`
	SchemaRevision       string           `json:"schema_revision"`
	RuntimeRole          string           `json:"runtime_role"`
	SchemaCreateAllowed  bool             `json:"schema_create_allowed"`
	Mutation             mutationEvidence `json:"mutation"`
	RepresentativeParity map[string]any   `json:"representative_parity"`
}

type mutationEvidence struct {
	Table                 string `json:"table"`
	Count                 int    `json:"count"`
	AmountVND             int64  `json:"amount_vnd"`
	Category              string `json:"category"`
	Description           string `json:"description"`
	OccurredAt            string `json:"occurred_at"`
	UserID                string `json:"user_id"`
	IdempotencyKeyPresent bool   `json:"idempotency_key_present"`
}

type scheduledRow struct {
	Key, UserID, DeviceID, Kind, Title, Status string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "companion-verify-tier1-store:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("companion-verify-tier1-store", flag.ContinueOnError)
	postgresURL := flags.String("postgres", "", "authoritative PostgreSQL URL")
	output := flags.String("output", "", "evidence JSON output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*postgresURL) == "" || strings.TrimSpace(*output) == "" {
		return errors.New("--postgres and --output are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := pgstore.OpenStore(ctx, pgstore.PoolConfig{DSN: *postgresURL, MaxConns: 2})
	if err != nil {
		return err
	}
	defer data.Close()
	if err := data.VerifySchema(ctx); err != nil {
		return err
	}

	report, err := verify(ctx, data)
	if err != nil {
		return err
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(report)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Println("TOOL PARITY EVIDENCE PASS: PostgreSQL expense/budget/note/journal/reminder/timer/memory authoritative state verified")
	return nil
}

func verify(ctx context.Context, data *pgstore.Store) (evidence, error) {
	var runtimeRole string
	var schemaCreateAllowed bool
	if err := data.Pool().QueryRow(ctx, `SELECT current_user, has_schema_privilege(current_user, 'public', 'CREATE')`).Scan(&runtimeRole, &schemaCreateAllowed); err != nil {
		return evidence{}, err
	}
	if runtimeRole != "companion_app" || schemaCreateAllowed {
		return evidence{}, fmt.Errorf("unexpected PostgreSQL runtime role=%q schema_create=%t", runtimeRole, schemaCreateAllowed)
	}
	var expense mutationEvidence
	expense.Table = "expenses"
	err := data.Pool().QueryRow(ctx, `
		SELECT count(*)::int, COALESCE(max(amount_vnd),0), COALESCE(max(category),''),
			COALESCE(max(description),''), COALESCE(max(occurred_at)::text,''),
			COALESCE(max(user_id),''), bool_and(idempotency_key <> '')
		FROM expenses
	`).Scan(&expense.Count, &expense.AmountVND, &expense.Category, &expense.Description, &expense.OccurredAt, &expense.UserID, &expense.IdempotencyKeyPresent)
	if err != nil {
		return evidence{}, err
	}
	if expense.Count != 1 || expense.AmountVND != 50000 || expense.Category != "food" || expense.Description != "tier1 deterministic expense" || expense.UserID != "default" || !expense.IdempotencyKeyPresent {
		return evidence{}, fmt.Errorf("unexpected expense state: %+v", expense)
	}

	var budgetUser, budgetPeriod string
	var budgetLimit int64
	var budgetCount int
	if err := data.Pool().QueryRow(ctx, `SELECT count(*)::int, COALESCE(max(user_id),''), COALESCE(max(period),''), COALESCE(max(limit_vnd),0) FROM budgets`).Scan(&budgetCount, &budgetUser, &budgetPeriod, &budgetLimit); err != nil {
		return evidence{}, err
	}
	if budgetCount != 1 || budgetUser != "default" || budgetPeriod != "weekly" || budgetLimit != 1000000 {
		return evidence{}, fmt.Errorf("unexpected budget state: count=%d user=%q period=%q limit=%d", budgetCount, budgetUser, budgetPeriod, budgetLimit)
	}

	var noteCount int
	var noteUser, noteContent string
	var noteKey bool
	if err := data.Pool().QueryRow(ctx, `SELECT count(*)::int, COALESCE(max(user_id),''), COALESCE(max(content),''), bool_and(idempotency_key <> '') FROM notes`).Scan(&noteCount, &noteUser, &noteContent, &noteKey); err != nil {
		return evidence{}, err
	}
	if noteCount != 1 || noteUser != "default" || noteContent != "tier1 note" || !noteKey {
		return evidence{}, fmt.Errorf("unexpected note state: count=%d user=%q content=%q key=%t", noteCount, noteUser, noteContent, noteKey)
	}

	var journalCount int
	var journalUser, journalContent string
	var journalKey bool
	if err := data.Pool().QueryRow(ctx, `SELECT count(*)::int, COALESCE(max(user_id),''), COALESCE(max(content),''), bool_and(idempotency_key <> '') FROM journal_entries`).Scan(&journalCount, &journalUser, &journalContent, &journalKey); err != nil {
		return evidence{}, err
	}
	if journalCount != 1 || journalUser != "default" || journalContent != "tier1 journal" || !journalKey {
		return evidence{}, fmt.Errorf("unexpected journal state: count=%d user=%q content=%q key=%t", journalCount, journalUser, journalContent, journalKey)
	}

	rows, err := data.Pool().Query(ctx, `SELECT idempotency_key,user_id,device_id,kind,title,status FROM reminders ORDER BY kind`)
	if err != nil {
		return evidence{}, err
	}
	var scheduled []scheduledRow
	for rows.Next() {
		var row scheduledRow
		if err := rows.Scan(&row.Key, &row.UserID, &row.DeviceID, &row.Kind, &row.Title, &row.Status); err != nil {
			rows.Close()
			return evidence{}, err
		}
		scheduled = append(scheduled, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return evidence{}, err
	}
	rows.Close()
	if len(scheduled) != 2 {
		return evidence{}, fmt.Errorf("scheduled mutation count=%d, want reminder+timer", len(scheduled))
	}
	byKind := map[string]scheduledRow{}
	for _, row := range scheduled {
		byKind[row.Kind] = row
	}
	for kind, title := range map[string]string{"reminder": "tier1 reminder", "timer": "tier1 timer"} {
		row, ok := byKind[kind]
		if !ok || row.Key == "" || row.UserID != "default" || row.DeviceID != "software-device-tool" || row.Title != title || (row.Status != "pending" && row.Status != "active") {
			return evidence{}, fmt.Errorf("unexpected %s state: %+v", kind, row)
		}
	}

	memories, err := data.CurrentMemories(ctx, "default", time.Now().Add(time.Minute), 10)
	if err != nil {
		return evidence{}, err
	}
	if len(memories) != 1 || memories[0].Key != "preferred_language" || string(memories[0].Kind) != "semantic" || memories[0].Value != "Vietnamese" || memories[0].Source != "user_explicit" {
		return evidence{}, fmt.Errorf("unexpected memory state: %+v", memories)
	}

	return evidence{
		SchemaVersion: 2, EvidenceClass: "tier1_orchestration", Promotion: "orchestration_only",
		Result: "passed", AuthoritativeStore: "postgresql", SchemaRevision: pgstore.RequiredSchemaRevision,
		RuntimeRole: runtimeRole, SchemaCreateAllowed: schemaCreateAllowed,
		Mutation: expense,
		RepresentativeParity: map[string]any{
			"expense":  map[string]any{"count": 1, "amount_vnd": int64(50000), "idempotency_key_present": true},
			"budget":   map[string]any{"period": "weekly", "limit_vnd": int64(1000000)},
			"note":     map[string]any{"count": 1, "content": "tier1 note", "idempotency_key_present": true},
			"journal":  map[string]any{"count": 1, "content": "tier1 journal", "idempotency_key_present": true},
			"reminder": map[string]any{"count": 1, "title": "tier1 reminder", "device_id": "software-device-tool", "idempotency_key_present": true},
			"timer":    map[string]any{"count": 1, "title": "tier1 timer", "device_id": "software-device-tool", "idempotency_key_present": true},
			"memory":   map[string]any{"count": 1, "key": "preferred_language", "kind": "semantic", "value": "Vietnamese"},
		},
	}, nil
}
