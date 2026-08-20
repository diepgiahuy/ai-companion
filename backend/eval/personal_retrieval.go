package eval

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/contextengine"
	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
	"companion-server/internal/memory"
)

const PersonalRetrievalReportSchemaVersion = "companion.personal_retrieval.report.v1"

type PersonalRetrievalScenario struct {
	ID                string   `json:"id"`
	Language          string   `json:"language"`
	Category          string   `json:"category"`
	Input             string   `json:"input"`
	ExpectedDomains   []string `json:"expected_domains"`
	TurnTime          string   `json:"turn_time,omitempty"`
	Owner             string   `json:"owner,omitempty"`
	ForbiddenTools    []string `json:"forbidden_tools,omitempty"`
	ExpectEmpty       bool     `json:"expect_empty,omitempty"`
	ExpectedKeyTerms  []string `json:"expected_key_terms,omitempty"`
	ForbiddenKeyTerms []string `json:"forbidden_key_terms,omitempty"`
}

type PersonalRetrievalCaseResult struct {
	CaseID           string   `json:"case_id"`
	Language         string   `json:"language"`
	Category         string   `json:"category"`
	ExpectedDomains  []string `json:"expected_domains"`
	ActualDomains    []string `json:"actual_domains"`
	OwnerScopePassed bool     `json:"owner_scope_passed"`
	Passed           bool     `json:"passed"`
	Reason           string   `json:"reason,omitempty"`
	FailureType      string   `json:"failure_type"` // none, retrieval, routing_model, temporal_interpretation, missing_domain_capability
}

type CategoryStats struct {
	Total  int     `json:"total"`
	Passed int     `json:"passed"`
	Rate   float64 `json:"rate"`
}

type PersonalRetrievalReport struct {
	SchemaVersion     string                        `json:"schema_version"`
	EvidenceClass     string                        `json:"evidence_class"`
	GeneratedAt       time.Time                     `json:"generated_at"`
	CorpusSHA256      string                        `json:"corpus_sha256"`
	TotalCases        int                           `json:"total_cases"`
	PassedCases       int                           `json:"passed_cases"`
	FailedCases       int                           `json:"failed_cases"`
	PassRate          float64                       `json:"pass_rate"`
	DomainBreakdown   map[string]CategoryStats      `json:"domain_breakdown"`
	LanguageBreakdown map[string]CategoryStats      `json:"language_breakdown"`
	CategoryBreakdown map[string]CategoryStats      `json:"category_breakdown"`
	Results           []PersonalRetrievalCaseResult `json:"results"`
}

type RetrievalDependencies struct {
	Store         domain.DurableRepositories
	Memory        *memory.Service
	Registry      *capability.ToolRegistry
	Router        *contextengine.Router
	Resources     *capability.ResourceRegistry
	RecordingsDir string
	Now           func() time.Time
}

func makeMutationReq(actor, op, key, payload string) idempotency.Request {
	h := sha256.Sum256([]byte(payload))
	return idempotency.Request{
		Actor:       actor,
		Operation:   op,
		Key:         key,
		RequestHash: hex.EncodeToString(h[:]),
	}
}

func LoadPersonalRetrievalCorpus(r io.Reader) ([]PersonalRetrievalScenario, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("read retrieval corpus: %w", err)
	}
	sum := sha256.Sum256(data)
	scenarios := make([]PersonalRetrievalScenario, 0)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var s PersonalRetrievalScenario
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "", fmt.Errorf("decode retrieval corpus line %d: %w", line, err)
		}
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			s.ID = fmt.Sprintf("retrieval-%04d", line)
		}
		if _, ok := seen[s.ID]; ok {
			return nil, "", fmt.Errorf("line %d: duplicate scenario id %q", line, s.ID)
		}
		seen[s.ID] = struct{}{}
		if s.Owner == "" {
			s.Owner = "alice"
		}
		scenarios = append(scenarios, s)
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("scan retrieval corpus: %w", err)
	}
	if len(scenarios) == 0 {
		return nil, "", fmt.Errorf("retrieval corpus is empty")
	}
	return scenarios, hex.EncodeToString(sum[:]), nil
}

func SeedRetrievalFixture(ctx context.Context, deps RetrievalDependencies) error {
	baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	if deps.RecordingsDir == "" {
		deps.RecordingsDir = "data/recordings"
	}
	_ = os.MkdirAll(deps.RecordingsDir, 0o700)

	// Alice: Expenses
	todayExp := baseTime.Add(-90 * time.Minute) // 08:30
	yestExp := baseTime.Add(-20 * time.Hour)    // yesterday 14:00
	monthExp := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	billsExp := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	req1 := makeMutationReq("alice", "expense.create", "seed-exp-1", "exp1")
	_ = deps.Store.CreateExpenseMutation(ctx, req1, "alice", 45000, "food", "Bún bò sáng", todayExp)
	req2 := makeMutationReq("alice", "expense.create", "seed-exp-2", "exp2")
	_ = deps.Store.CreateExpenseMutation(ctx, req2, "alice", 35000, "food", "Cà phê muối", yestExp)
	req3 := makeMutationReq("alice", "expense.create", "seed-exp-3", "exp3")
	_ = deps.Store.CreateExpenseMutation(ctx, req3, "alice", 250000, "transport", "Grab đi sân bay", monthExp)
	req4 := makeMutationReq("alice", "expense.create", "seed-exp-4", "exp4")
	_ = deps.Store.CreateExpenseMutation(ctx, req4, "alice", 1200000, "bills", "Tiền điện tháng 7", billsExp)

	// Alice: Budgets
	_ = deps.Store.SetBudget(ctx, "alice", "daily", 200000)
	_ = deps.Store.SetBudget(ctx, "alice", "weekly", 1500000)
	_ = deps.Store.SetBudget(ctx, "alice", "monthly", 6000000)

	// Alice: Savings Goal
	_ = deps.Store.SetSavingsGoal(ctx, "alice", "monthly", 5000000, "Monthly goal", baseTime)

	// Alice: Notes
	reqN1 := makeMutationReq("alice", "note.create", "seed-note-1", "note1")
	_ = deps.Store.CreateNoteMutation(ctx, reqN1, "alice", "Wifi office: CompanionLab_5G / pass: SecureCompanion2026")
	reqN2 := makeMutationReq("alice", "note.create", "seed-note-2", "note2")
	_ = deps.Store.CreateNoteMutation(ctx, reqN2, "alice", "Project Alpha launch checklist: review PRs, verify HIL, deploy companiond")
	reqN3 := makeMutationReq("alice", "note.create", "seed-note-3", "note3")
	_ = deps.Store.CreateNoteMutation(ctx, reqN3, "alice", "Shopping list: coffee beans, oat milk, HDMI cable")

	// Alice: Journal
	todayJour := baseTime.Add(-3 * time.Hour)
	yestJour := baseTime.Add(-13 * time.Hour)
	reqJ1 := makeMutationReq("alice", "journal.create", "seed-j-1", "jour1")
	_ = deps.Store.CreateJournalMutation(ctx, reqJ1, "alice", "Hôm nay bắt đầu sprint mới, cảm thấy tràn đầy năng lượng và tập trung.", todayJour)
	reqJ2 := makeMutationReq("alice", "journal.create", "seed-j-2", "jour2")
	_ = deps.Store.CreateJournalMutation(ctx, reqJ2, "alice", "Đã giải quyết xong bug memory leak và merge PR thành công.", yestJour)

	// Alice: Schedule (Reminders & Timers)
	_ = deps.Store.CreateReminderForDevice(ctx, "alice", "seed-rem-1", "dev-a", "Họp standup team IoT", baseTime.Add(30*time.Minute))
	_ = deps.Store.CreateReminderForDevice(ctx, "alice", "seed-rem-2", "dev-a", "Call bác sĩ nha khoa tái khám", baseTime.Add(23*time.Hour))
	_ = deps.Store.CreateTimerForDevice(ctx, "alice", "seed-tim-1", "dev-a", "Luộc trứng", baseTime.Add(5*time.Minute))

	// Alice: Voice Memos
	fileA := filepath.Join(deps.RecordingsDir, "memo-alpha.wav")
	fileB := filepath.Join(deps.RecordingsDir, "memo-groceries.wav")
	_ = os.WriteFile(fileA, []byte("alpha audio"), 0o600)
	_ = os.WriteFile(fileB, []byte("groceries audio"), 0o600)
	_ = deps.Store.CreateVoiceMemo(ctx, "alice", "seed-vm-1", "dev-a", fileA, "Brainstorm ý tưởng voice user interface cho Companion v2", 12500)
	_ = deps.Store.CreateVoiceMemo(ctx, "alice", "seed-vm-2", "dev-a", fileB, "Nhớ mua thêm rau xà lách và cà chua bi ở siêu thị", 4500)

	// Alice: Memory
	if deps.Memory != nil {
		reqM1Old := makeMutationReq("alice", "memory.remember", "seed-mem-1-old", "mem1old")
		_, _ = deps.Memory.RememberMutation(ctx, reqM1Old, "alice", "user_coffee_preference", memory.Semantic, "Thích uống latte ngọt", "user_explicit", 1, baseTime.Add(-240*time.Hour))

		reqM1 := makeMutationReq("alice", "memory.remember", "seed-mem-1", "mem1")
		_, _ = deps.Memory.RememberMutation(ctx, reqM1, "alice", "user_coffee_preference", memory.Semantic, "Thích uống Americano không đường, ghét cà phê sữa quá ngọt", "user_explicit", 1, baseTime.Add(-48*time.Hour))

		reqM2 := makeMutationReq("alice", "memory.remember", "seed-mem-2", "mem2")
		_, _ = deps.Memory.RememberMutation(ctx, reqM2, "alice", "mother_birthday", memory.Episodic, "Ngày sinh nhật của mẹ là 20 tháng 10", "user_explicit", 1, baseTime.Add(-72*time.Hour))

		reqM3 := makeMutationReq("alice", "memory.remember", "seed-mem-3", "mem3")
		_, _ = deps.Memory.RememberMutation(ctx, reqM3, "alice", "allergy_seafood", memory.Semantic, "Bị dị ứng tôm cua nặng, không ăn được hải sản có vỏ", "user_explicit", 1, baseTime.Add(-96*time.Hour))
	}

	// Bob: Seed isolated confidential data
	reqB1 := makeMutationReq("bob", "expense.create", "bob-exp-1", "bobexp1")
	_ = deps.Store.CreateExpenseMutation(ctx, reqB1, "bob", 5000000, "shopping", "Bob confidential expense: gaming monitor", todayExp)
	_ = deps.Store.SetBudget(ctx, "bob", "monthly", 20000000)
	reqB2 := makeMutationReq("bob", "note.create", "bob-note-1", "bobnote1")
	_ = deps.Store.CreateNoteMutation(ctx, reqB2, "bob", "Bob private confidential note: secret pass 99999")
	reqB3 := makeMutationReq("bob", "journal.create", "bob-j-1", "bobjour1")
	_ = deps.Store.CreateJournalMutation(ctx, reqB3, "bob", "Bob journal: private diary entry", todayJour)
	_ = deps.Store.CreateReminderForDevice(ctx, "bob", "bob-rem-1", "dev-b", "Bob private meeting with investor", baseTime.Add(2*time.Hour))
	if deps.Memory != nil {
		reqBM := makeMutationReq("bob", "memory.remember", "bob-mem-1", "bobmem1")
		_, _ = deps.Memory.RememberMutation(ctx, reqBM, "bob", "bob_secret", memory.Semantic, "Bob secret identity and account code 007", "user_explicit", 1, baseTime)
	}

	// Seed deletion / superseded artifacts
	// Deleted note:
	reqDelNote := makeMutationReq("alice", "note.create", "del-note-1", "delnote")
	_ = deps.Store.CreateNoteMutation(ctx, reqDelNote, "alice", "deleted_temp_note_content")
	noteItems, _ := deps.Store.QueryNotes(ctx, "alice", domain.NoteQuery{Search: "deleted_temp_note_content"})
	if len(noteItems) > 0 {
		_ = deps.Store.DeleteNote(ctx, "alice", noteItems[0].ID)
	}

	// Deleted expense:
	reqDelExp := makeMutationReq("alice", "expense.create", "del-exp-1", "delexp")
	_ = deps.Store.CreateExpenseMutation(ctx, reqDelExp, "alice", 999000, "food", "deleted_bill_999", todayExp)
	rangeFrom := baseTime.AddDate(0, 0, -30)
	rangeTo := baseTime.AddDate(0, 0, 30)
	expItems, _ := deps.Store.ListExpenses(ctx, "alice", rangeFrom, rangeTo, "", 50)
	for _, it := range expItems {
		if strings.Contains(it.Description, "deleted_bill_999") {
			_ = deps.Store.DeleteExpense(ctx, "alice", it.ID)
		}
	}

	// Cancelled reminder:
	_ = deps.Store.CreateReminderForDevice(ctx, "alice", "del-rem-1", "dev-a", "canceled_meeting_999", baseTime.Add(time.Hour))
	remItems, _ := deps.Store.ListReminders(ctx, "alice", "dev-a", "all", 10)
	for _, r := range remItems {
		if strings.Contains(r.Title, "canceled_meeting_999") {
			_ = deps.Store.CancelScheduledItem(ctx, "alice", r.ID)
		}
	}

	// Deleted voice memo:
	fileDel := filepath.Join(deps.RecordingsDir, "memo-del.wav")
	_ = os.WriteFile(fileDel, []byte("del audio"), 0o600)
	_ = deps.Store.CreateVoiceMemo(ctx, "alice", "del-vm-1", "dev-a", fileDel, "deleted_voice_memo_audio", 1000)
	vmItems, _ := deps.Store.QueryVoiceMemos(ctx, "alice", domain.VoiceMemoQuery{Search: "deleted_voice_memo_audio"})
	if len(vmItems) > 0 {
		_ = deps.Store.DeleteVoiceMemo(ctx, "alice", vmItems[0].ID)
		_ = os.Remove(fileDel)
	}

	// Forgotten memory:
	if deps.Memory != nil {
		reqForget := makeMutationReq("alice", "memory.remember", "del-mem-1", "delmem")
		_, _ = deps.Memory.RememberMutation(ctx, reqForget, "alice", "credit_card_pin", memory.Semantic, "forgotten_pin_8888", "user_explicit", 1, baseTime.Add(-100*time.Hour))
		reqForgetKey := makeMutationReq("alice", "memory.forget", "forget-pin-1", "forgetpin")
		_ = deps.Memory.ForgetMutation(ctx, reqForgetKey, "alice", "credit_card_pin")
	}

	return nil
}

func RunPersonalRetrievalEvaluation(ctx context.Context, scenarios []PersonalRetrievalScenario, deps RetrievalDependencies) (PersonalRetrievalReport, error) {
	if len(scenarios) == 0 {
		return PersonalRetrievalReport{}, fmt.Errorf("no scenarios to evaluate")
	}
	if err := SeedRetrievalFixture(ctx, deps); err != nil {
		return PersonalRetrievalReport{}, fmt.Errorf("seed fixture: %w", err)
	}

	report := PersonalRetrievalReport{
		SchemaVersion:     PersonalRetrievalReportSchemaVersion,
		EvidenceClass:     "deterministic_store_measured",
		GeneratedAt:       time.Now().UTC(),
		TotalCases:        len(scenarios),
		DomainBreakdown:   make(map[string]CategoryStats),
		LanguageBreakdown: make(map[string]CategoryStats),
		CategoryBreakdown: make(map[string]CategoryStats),
		Results:           make([]PersonalRetrievalCaseResult, 0, len(scenarios)),
	}

	for _, s := range scenarios {
		res := evaluateScenario(ctx, s, deps)
		report.Results = append(report.Results, res)
		if res.Passed {
			report.PassedCases++
		} else {
			report.FailedCases++
		}

		// Update breakdowns
		updateStats(report.LanguageBreakdown, s.Language, res.Passed)
		updateStats(report.CategoryBreakdown, s.Category, res.Passed)
		for _, d := range s.ExpectedDomains {
			updateStats(report.DomainBreakdown, d, res.Passed)
		}
	}

	if report.TotalCases > 0 {
		report.PassRate = float64(report.PassedCases) / float64(report.TotalCases)
	}

	return report, nil
}

func evaluateScenario(ctx context.Context, s PersonalRetrievalScenario, deps RetrievalDependencies) PersonalRetrievalCaseResult {
	res := PersonalRetrievalCaseResult{
		CaseID:           s.ID,
		Language:         s.Language,
		Category:         s.Category,
		ExpectedDomains:  s.ExpectedDomains,
		OwnerScopePassed: true,
		FailureType:      "none",
	}

	turnTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	if s.TurnTime != "" {
		if t, err := time.Parse(time.RFC3339, s.TurnTime); err == nil {
			turnTime = t.UTC()
		}
	}

	owner := s.Owner
	if owner == "" {
		owner = "alice"
	}

	// 1. Context Engine Routing Plan
	var plan contextengine.Plan
	if deps.Router != nil {
		plan = deps.Router.Plan(ctx, s.Input)
		res.ActualDomains = plan.Packs
	}

	// Check domain coverage
	if deps.Router != nil && len(s.ExpectedDomains) > 0 {
		actualMap := make(map[string]bool)
		for _, d := range plan.Packs {
			actualMap[d] = true
		}
		for _, exp := range s.ExpectedDomains {
			if !actualMap[exp] {
				res.Passed = false
				res.FailureType = "routing_model"
				res.Reason = fmt.Sprintf("router missed expected domain %q; planned: %v", exp, plan.Packs)
				return res
			}
		}
	}

	// 2. Perform Explicit Store/Resource Retrieval under the turn context
	retrievedText := strings.Builder{}

	// Execute retrieval for relevant domains
	for _, domainName := range s.ExpectedDomains {
		switch domainName {
		case "expense":
			from := time.Date(turnTime.Year(), turnTime.Month(), 1, 0, 0, 0, 0, time.UTC)
			to := from.AddDate(0, 1, 0)
			if strings.Contains(strings.ToLower(s.Input), "hôm nay") || strings.Contains(strings.ToLower(s.Input), "today") {
				from = time.Date(turnTime.Year(), turnTime.Month(), turnTime.Day(), 0, 0, 0, 0, time.UTC)
				to = from.Add(24 * time.Hour)
			} else if strings.Contains(strings.ToLower(s.Input), "hôm qua") || strings.Contains(strings.ToLower(s.Input), "yesterday") {
				to = time.Date(turnTime.Year(), turnTime.Month(), turnTime.Day(), 0, 0, 0, 0, time.UTC)
				from = to.Add(-24 * time.Hour)
			} else if strings.Contains(strings.ToLower(s.Input), "tháng") || strings.Contains(strings.ToLower(s.Input), "month") {
				from = time.Date(turnTime.Year(), turnTime.Month(), 1, 0, 0, 0, 0, time.UTC)
				to = from.AddDate(0, 1, 0)
			} else if strings.Contains(strings.ToLower(s.Input), "tuần") || strings.Contains(strings.ToLower(s.Input), "week") {
				weekday := int(turnTime.Weekday())
				if weekday == 0 {
					weekday = 7
				}
				from = time.Date(turnTime.Year(), turnTime.Month(), turnTime.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
				to = from.AddDate(0, 0, 7)
			} else if strings.Contains(strings.ToLower(s.Input), "3 ngày") || strings.Contains(strings.ToLower(s.Input), "past 3 days") {
				to = turnTime
				from = to.AddDate(0, 0, -3)
			} else if strings.Contains(strings.ToLower(s.Input), "gần đây") || strings.Contains(strings.ToLower(s.Input), "recent") {
				from = turnTime.AddDate(0, 0, -90)
				to = turnTime.Add(24 * time.Hour)
			}
			tot, _ := deps.Store.ExpenseTotal(ctx, owner, from, to)
			retrievedText.WriteString(fmt.Sprintf(" ExpenseTotal: %d ", tot))
			items, _ := deps.Store.ListExpenses(ctx, owner, from, to, "", 50)
			for _, item := range items {
				retrievedText.WriteString(fmt.Sprintf(" [Expense: %d %s %s %s] ", item.AmountVND, item.Category, item.Description, item.OccurredAt.Format(time.RFC3339)))
			}

		case "budget":
			for _, p := range []string{"daily", "weekly", "monthly"} {
				if limit, found, _ := deps.Store.BudgetLimit(ctx, owner, p); found {
					retrievedText.WriteString(fmt.Sprintf(" [Budget: %s %d] ", p, limit))
				}
			}

		case "saving":
			if g, found, _ := deps.Store.GetSavingsGoal(ctx, owner, "monthly"); found {
				retrievedText.WriteString(fmt.Sprintf(" [SavingsGoal: %s %d] target_vnd ", g.Period, g.TargetVND))
				limit, _, _ := deps.Store.BudgetLimit(ctx, owner, "monthly")
				from := time.Date(turnTime.Year(), turnTime.Month(), 1, 0, 0, 0, 0, time.UTC)
				to := from.AddDate(0, 1, 0)
				spent, _ := deps.Store.ExpenseTotal(ctx, owner, from, to)
				headroom := limit - spent
				retrievedText.WriteString(fmt.Sprintf(" headroom_vnd: %d ", headroom))
			}

		case "note":
			query := domain.NoteQuery{Limit: 50}
			lower := strings.ToLower(s.Input)
			if strings.Contains(lower, "wifi") {
				query.Search = "Wifi"
			} else if strings.Contains(lower, "alpha") {
				query.Search = "Alpha"
			} else if strings.Contains(lower, "shopping") || strings.Contains(lower, "mua sắm") {
				query.Search = "Shopping"
			} else if strings.Contains(lower, "lượng tử") || strings.Contains(lower, "quantum") {
				query.Search = "lượng tử"
			} else if strings.Contains(lower, "tạm thời") || strings.Contains(lower, "vừa bị xóa") {
				query.Search = "deleted_temp"
			}
			notes, _ := deps.Store.QueryNotes(ctx, owner, query)
			for _, n := range notes {
				retrievedText.WriteString(fmt.Sprintf(" [Note: %s] ", n.Content))
			}

		case "journal":
			var from, to time.Time
			if strings.Contains(strings.ToLower(s.Input), "hôm nay") || strings.Contains(strings.ToLower(s.Input), "today") {
				from = time.Date(turnTime.Year(), turnTime.Month(), turnTime.Day(), 0, 0, 0, 0, time.UTC)
				to = from.Add(24 * time.Hour)
			} else if strings.Contains(strings.ToLower(s.Input), "hôm qua") || strings.Contains(strings.ToLower(s.Input), "yesterday") {
				to = time.Date(turnTime.Year(), turnTime.Month(), turnTime.Day(), 0, 0, 0, 0, time.UTC)
				from = to.Add(-24 * time.Hour)
			}
			entries, _ := deps.Store.ListJournal(ctx, owner, from, to, 50)
			for _, j := range entries {
				retrievedText.WriteString(fmt.Sprintf(" [Journal: %s %s] ", j.Content, j.OccurredAt.Format(time.RFC3339)))
			}

		case "schedule":
			reminders, _ := deps.Store.ListReminders(ctx, owner, "", "active", 50)
			for _, r := range reminders {
				retrievedText.WriteString(fmt.Sprintf(" [Reminder: %s %s] ", r.Title, r.FireAt.Format(time.RFC3339)))
			}
			timers, _ := deps.Store.ListTimers(ctx, owner, "", "active", 50)
			for _, t := range timers {
				retrievedText.WriteString(fmt.Sprintf(" [Timer: %s %ds] ", t.Title, t.PausedRemainingSeconds))
			}

		case "voice":
			query := domain.VoiceMemoQuery{Limit: 50}
			lower := strings.ToLower(s.Input)
			if strings.Contains(lower, "siêu thị") || strings.Contains(lower, "groceries") {
				query.Search = "siêu thị"
			} else if strings.Contains(lower, "brainstorm") || strings.Contains(lower, "ý tưởng") {
				query.Search = "Brainstorm"
			} else if strings.Contains(lower, "blockchain") || strings.Contains(lower, "satellite") {
				query.Search = "blockchain"
			} else if strings.Contains(lower, "bị xóa") || strings.Contains(lower, "deleted") {
				query.Search = "deleted_voice_memo"
			}
			memos, _ := deps.Store.QueryVoiceMemos(ctx, owner, query)
			for _, m := range memos {
				retrievedText.WriteString(fmt.Sprintf(" [VoiceMemo: %s] ", m.Transcript))
			}

		case "memory":
			if deps.Memory != nil {
				mems, _ := deps.Memory.Recall(ctx, owner, s.Input, 5)
				for _, m := range mems {
					retrievedText.WriteString(fmt.Sprintf(" [Memory: %s=%s] ", m.Item.Key, m.Item.Value))
				}
			}
		}
	}

	content := retrievedText.String()

	// Check Owner Isolation: ensure other owner's data never leaked into content
	otherOwner := "bob"
	if owner == "bob" {
		otherOwner = "alice"
	}
	if otherOwner == "bob" && (strings.Contains(content, "gaming monitor") || strings.Contains(content, "Bob confidential") || strings.Contains(content, "secret pass 99999")) {
		res.OwnerScopePassed = false
		res.Passed = false
		res.FailureType = "retrieval"
		res.Reason = "owner isolation breach: Bob's data leaked into Alice's retrieval"
		return res
	}
	if otherOwner == "alice" && (strings.Contains(content, "SecureCompanion2026") || strings.Contains(content, "Grab đi sân bay")) {
		res.OwnerScopePassed = false
		res.Passed = false
		res.FailureType = "retrieval"
		res.Reason = "owner isolation breach: Alice's data leaked into Bob's retrieval"
		return res
	}

	// Check ExpectEmpty:
	if s.ExpectEmpty {
		for _, exp := range s.ExpectedDomains {
			if exp == "note" && strings.Contains(content, "[Note:") {
				res.Passed = false
				res.FailureType = "retrieval"
				res.Reason = "expected empty result but received note items"
				return res
			}
			if exp == "voice" && strings.Contains(content, "[VoiceMemo:") {
				res.Passed = false
				res.FailureType = "retrieval"
				res.Reason = "expected empty result but received voice memo items"
				return res
			}
			if exp == "schedule" && (strings.Contains(content, "canceled_meeting_999")) {
				res.Passed = false
				res.FailureType = "retrieval"
				res.Reason = "expected empty result but received cancelled schedule items"
				return res
			}
		}
	}

	// Check ForbiddenKeyTerms:
	for _, forbidden := range s.ForbiddenKeyTerms {
		if strings.Contains(content, forbidden) {
			res.Passed = false
			res.FailureType = "retrieval"
			res.Reason = fmt.Sprintf("retrieved content contains forbidden term %q", forbidden)
			return res
		}
	}

	// Check ExpectedKeyTerms:
	for _, expected := range s.ExpectedKeyTerms {
		if !strings.Contains(content, expected) {
			res.Passed = false
			res.FailureType = "retrieval"
			res.Reason = fmt.Sprintf("retrieved content missing expected key term %q", expected)
			return res
		}
	}

	res.Passed = true
	res.FailureType = "none"
	return res
}

func updateStats(m map[string]CategoryStats, key string, passed bool) {
	if key == "" {
		return
	}
	st := m[key]
	st.Total++
	if passed {
		st.Passed++
	}
	if st.Total > 0 {
		st.Rate = float64(st.Passed) / float64(st.Total)
	}
	m[key] = st
}
