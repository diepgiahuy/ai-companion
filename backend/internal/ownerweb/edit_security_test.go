package ownerweb

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"companion-server/internal/store"
)

func TestOwnerWebEditMutationsRejectMissingAndInvalidCSRF(t *testing.T) {
	auth, session, _, cleanup := newTestAuthService(t, "user-alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Auth: auth})

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPatch, "/v1/owner/data/expenses"},
		{http.MethodPatch, "/v1/owner/data/notes"},
		{http.MethodPatch, "/v1/owner/data/journal"},
		{http.MethodPatch, "/v1/owner/data/reminders"},
		{http.MethodPost, "/v1/owner/data/reminders/cancel"},
	}
	for _, tc := range cases {
		for _, csrf := range []string{"", "invalid-csrf"} {
			w := ownerEditRequest(t, handler, tc.method, tc.path, `{}`, session, csrf)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s csrf=%q status=%d want=%d", tc.method, tc.path, csrf, w.Code, http.StatusUnauthorized)
			}
		}
	}
}

func TestOwnerWebEditMutationsRejectCrossOwnerIDs(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_edit_cross_owner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	alice := "user-alice"
	if err := data.CreateExpense(ctx, alice, "expense-a", 100000, "food", "alice expense", now); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateNote(ctx, alice, "note-a", "alice note"); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateJournal(ctx, alice, "journal-a", "alice journal", now); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateReminderForDevice(ctx, alice, "reminder-a", "", "alice reminder", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	expenses, _ := data.ListExpenses(ctx, alice, now.Add(-time.Hour), now.Add(time.Hour), "", 10)
	notes, _ := data.ListNotes(ctx, alice, 10)
	journal, _ := data.ListJournal(ctx, alice, now.Add(-time.Hour), now.Add(time.Hour), 10)
	reminders, _ := data.ListReminders(ctx, alice, "", "active", 10)
	if len(expenses) != 1 || len(notes) != 1 || len(journal) != 1 || len(reminders) != 1 {
		t.Fatal("failed to seed cross-owner fixtures")
	}

	auth, session, csrf, cleanup := newTestAuthService(t, "user-bob")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPatch, "/v1/owner/data/expenses", `{"id":` + itoa64(expenses[0].ID) + `,"amount_vnd":200000,"category":"other","description":"bob","occurred_at":"` + now.Format(time.RFC3339) + `"}`},
		{http.MethodPatch, "/v1/owner/data/notes", `{"id":` + itoa64(notes[0].ID) + `,"content":"bob"}`},
		{http.MethodPatch, "/v1/owner/data/journal", `{"id":` + itoa64(journal[0].ID) + `,"content":"bob","occurred_at":"` + now.Format(time.RFC3339) + `"}`},
		{http.MethodPatch, "/v1/owner/data/reminders", `{"id":` + itoa64(reminders[0].ID) + `,"title":"bob","fire_at":"` + now.Add(2*time.Hour).Format(time.RFC3339) + `"}`},
		{http.MethodPost, "/v1/owner/data/reminders/cancel", `{"id":` + itoa64(reminders[0].ID) + `}`},
	}
	for _, tc := range cases {
		w := ownerEditRequest(t, handler, tc.method, tc.path, tc.body, session, csrf)
		if w.Code == http.StatusOK {
			t.Fatalf("cross-owner %s %s unexpectedly succeeded", tc.method, tc.path)
		}
	}

	remainingExpenses, _ := data.ListExpenses(ctx, alice, now.Add(-time.Hour), now.Add(time.Hour), "", 10)
	remainingNotes, _ := data.ListNotes(ctx, alice, 10)
	remainingJournal, _ := data.ListJournal(ctx, alice, now.Add(-time.Hour), now.Add(time.Hour), 10)
	remainingReminders, _ := data.ListReminders(ctx, alice, "", "active", 10)
	if remainingExpenses[0].AmountVND != 100000 || remainingExpenses[0].Description != "alice expense" {
		t.Fatalf("Alice expense changed: %+v", remainingExpenses[0])
	}
	if remainingNotes[0].Content != "alice note" {
		t.Fatalf("Alice note changed: %+v", remainingNotes[0])
	}
	if remainingJournal[0].Content != "alice journal" {
		t.Fatalf("Alice journal changed: %+v", remainingJournal[0])
	}
	if remainingReminders[0].Title != "alice reminder" || remainingReminders[0].Status == "cancelled" {
		t.Fatalf("Alice reminder changed: %+v", remainingReminders[0])
	}
}
