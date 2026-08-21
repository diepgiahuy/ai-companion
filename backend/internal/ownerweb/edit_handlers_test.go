package ownerweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"companion-server/internal/store"
)

func ownerEditRequest(t *testing.T, handler *Handler, method, path, body, session, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: session})
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestOwnerWebEditMutationsUseExistingDomainPaths(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_edits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	ctx := context.Background()
	userID := "user-alice"
	now := time.Now().UTC().Truncate(time.Second)
	if err := data.CreateExpense(ctx, userID, "expense-1", 100000, "food", "lunch", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateNote(ctx, userID, "note-1", "old note"); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateJournal(ctx, userID, "journal-1", "old journal", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateReminderForDevice(ctx, userID, "reminder-1", "", "old reminder", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	expenses, err := data.ListExpenses(ctx, userID, now.Add(-24*time.Hour), now.Add(24*time.Hour), "", 10)
	if err != nil || len(expenses) != 1 {
		t.Fatalf("expenses=%+v err=%v", expenses, err)
	}
	notes, err := data.ListNotes(ctx, userID, 10)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes=%+v err=%v", notes, err)
	}
	journal, err := data.ListJournal(ctx, userID, now.Add(-24*time.Hour), now.Add(24*time.Hour), 10)
	if err != nil || len(journal) != 1 {
		t.Fatalf("journal=%+v err=%v", journal, err)
	}
	reminders, err := data.ListReminders(ctx, userID, "", "active", 10)
	if err != nil || len(reminders) != 1 {
		t.Fatalf("reminders=%+v err=%v", reminders, err)
	}

	auth, session, csrf, cleanup := newTestAuthService(t, userID)
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	updatedExpenseAt := now.Add(-30 * time.Minute)
	w := ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/expenses",
		`{"id":`+itoa64(expenses[0].ID)+`,"amount_vnd":125000,"category":"transport","description":"train","occurred_at":"`+updatedExpenseAt.Format(time.RFC3339)+`"}`,
		session, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("expense update status=%d body=%s", w.Code, w.Body.String())
	}

	w = ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/notes",
		`{"id":`+itoa64(notes[0].ID)+`,"content":"corrected note"}`, session, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("note update status=%d body=%s", w.Code, w.Body.String())
	}

	updatedJournalAt := now.Add(-time.Hour)
	w = ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/journal",
		`{"id":`+itoa64(journal[0].ID)+`,"content":"corrected journal","occurred_at":"`+updatedJournalAt.Format(time.RFC3339)+`"}`,
		session, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("journal update status=%d body=%s", w.Code, w.Body.String())
	}

	updatedFireAt := now.Add(2 * time.Hour)
	w = ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/reminders",
		`{"id":`+itoa64(reminders[0].ID)+`,"title":"corrected reminder","fire_at":"`+updatedFireAt.Format(time.RFC3339)+`"}`,
		session, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("reminder update status=%d body=%s", w.Code, w.Body.String())
	}

	w = ownerEditRequest(t, handler, http.MethodPost, "/v1/owner/data/reminders/cancel",
		`{"id":`+itoa64(reminders[0].ID)+`}`, session, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("reminder cancel status=%d body=%s", w.Code, w.Body.String())
	}

	updatedExpenses, _ := data.ListExpenses(ctx, userID, now.Add(-24*time.Hour), now.Add(24*time.Hour), "", 10)
	if len(updatedExpenses) != 1 || updatedExpenses[0].AmountVND != 125000 || updatedExpenses[0].Description != "train" {
		t.Fatalf("expense not updated: %+v", updatedExpenses)
	}
	updatedNotes, _ := data.ListNotes(ctx, userID, 10)
	if len(updatedNotes) != 1 || updatedNotes[0].Content != "corrected note" {
		t.Fatalf("note not updated: %+v", updatedNotes)
	}
	updatedJournal, _ := data.ListJournal(ctx, userID, now.Add(-24*time.Hour), now.Add(24*time.Hour), 10)
	if len(updatedJournal) != 1 || updatedJournal[0].Content != "corrected journal" || !updatedJournal[0].OccurredAt.Equal(updatedJournalAt) {
		t.Fatalf("journal not updated: %+v", updatedJournal)
	}
	allReminders, _ := data.ListReminders(ctx, userID, "", "all", 10)
	if len(allReminders) != 1 || allReminders[0].Title != "corrected reminder" || allReminders[0].Status != "cancelled" {
		t.Fatalf("reminder not updated/cancelled: %+v", allReminders)
	}
}

func TestOwnerWebEditMutationRequiresCSRF(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_edit_csrf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.CreateNote(context.Background(), "user-alice", "note-1", "old note"); err != nil {
		t.Fatal(err)
	}
	notes, _ := data.ListNotes(context.Background(), "user-alice", 10)
	auth, session, _, cleanup := newTestAuthService(t, "user-alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	w := ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/notes",
		`{"id":`+itoa64(notes[0].ID)+`,"content":"blocked"}`, session, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("PATCH without CSRF status=%d want=%d", w.Code, http.StatusUnauthorized)
	}
	remaining, _ := data.ListNotes(context.Background(), "user-alice", 10)
	if len(remaining) != 1 || remaining[0].Content != "old note" {
		t.Fatalf("CSRF rejection changed note: %+v", remaining)
	}
}

func TestOwnerWebEditMutationFailsClosedAcrossOwners(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_edit_idor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	if err := data.CreateNote(ctx, "user-alice", "note-a", "alice private note"); err != nil {
		t.Fatal(err)
	}
	notes, _ := data.ListNotes(ctx, "user-alice", 10)
	auth, session, csrf, cleanup := newTestAuthService(t, "user-bob")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	w := ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/notes",
		`{"id":`+itoa64(notes[0].ID)+`,"content":"bob overwrite"}`, session, csrf)
	if w.Code == http.StatusOK {
		t.Fatalf("cross-owner update unexpectedly succeeded: %s", w.Body.String())
	}
	remaining, _ := data.ListNotes(ctx, "user-alice", 10)
	if len(remaining) != 1 || remaining[0].Content != "alice private note" {
		t.Fatalf("cross-owner update changed Alice data: %+v", remaining)
	}
}

func TestOwnerWebEditRejectsMalformedTimestampWithoutMutation(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_edit_time.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := data.CreateExpense(ctx, "user-alice", "expense-1", 100000, "food", "original", now); err != nil {
		t.Fatal(err)
	}
	expenses, _ := data.ListExpenses(ctx, "user-alice", now.Add(-time.Hour), now.Add(time.Hour), "", 10)
	auth, session, csrf, cleanup := newTestAuthService(t, "user-alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	w := ownerEditRequest(t, handler, http.MethodPatch, "/v1/owner/data/expenses",
		`{"id":`+itoa64(expenses[0].ID)+`,"amount_vnd":200000,"category":"food","description":"changed","occurred_at":"not-a-time"}`,
		session, csrf)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed timestamp status=%d want=%d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	remaining, _ := data.ListExpenses(ctx, "user-alice", now.Add(-time.Hour), now.Add(time.Hour), "", 10)
	if len(remaining) != 1 || remaining[0].AmountVND != 100000 || remaining[0].Description != "original" {
		t.Fatalf("invalid update changed expense: %+v", remaining)
	}
}

func itoa64(value int64) string {
	return strconv.FormatInt(value, 10)
}
