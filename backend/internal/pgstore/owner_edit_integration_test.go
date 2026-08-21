package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPostgresOwnerEditPersistenceAndIsolation(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("owner-edit-%d", time.Now().UnixNano())
	alice := prefix + "-alice"
	bob := prefix + "-bob"
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.CreateExpense(ctx, alice, prefix+"-expense", 100000, "food", "old expense", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNote(ctx, alice, prefix+"-note", "old note"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJournal(ctx, alice, prefix+"-journal", "old journal", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateReminderForDevice(ctx, alice, prefix+"-reminder", "", "old reminder", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	expenses, err := store.ListExpenses(ctx, alice, now.Add(-24*time.Hour), now.Add(24*time.Hour), "", 10)
	if err != nil || len(expenses) != 1 {
		t.Fatalf("expenses=%+v err=%v", expenses, err)
	}
	notes, err := store.ListNotes(ctx, alice, 10)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes=%+v err=%v", notes, err)
	}
	journal, err := store.ListJournal(ctx, alice, now.Add(-24*time.Hour), now.Add(24*time.Hour), 10)
	if err != nil || len(journal) != 1 {
		t.Fatalf("journal=%+v err=%v", journal, err)
	}
	reminders, err := store.ListReminders(ctx, alice, "", "active", 10)
	if err != nil || len(reminders) != 1 {
		t.Fatalf("reminders=%+v err=%v", reminders, err)
	}

	if err := store.UpdateExpense(ctx, alice, expenses[0].ID, 125000, "transport", "train", now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateNote(ctx, alice, notes[0].ID, "corrected note"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJournal(ctx, alice, journal[0].ID, "corrected journal", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateScheduledItem(ctx, alice, reminders[0].ID, "corrected reminder", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateExpense(ctx, bob, expenses[0].ID, 999999, "other", "bob overwrite", now); err == nil {
		t.Fatal("cross-owner expense update unexpectedly succeeded")
	}
	if err := store.UpdateNote(ctx, bob, notes[0].ID, "bob overwrite"); err == nil {
		t.Fatal("cross-owner note update unexpectedly succeeded")
	}
	if err := store.UpdateJournal(ctx, bob, journal[0].ID, "bob overwrite", now); err == nil {
		t.Fatal("cross-owner journal update unexpectedly succeeded")
	}
	if err := store.UpdateScheduledItem(ctx, bob, reminders[0].ID, "bob overwrite", now.Add(3*time.Hour)); err == nil {
		t.Fatal("cross-owner scheduled update unexpectedly succeeded")
	}

	updatedExpenses, _ := store.ListExpenses(ctx, alice, now.Add(-24*time.Hour), now.Add(24*time.Hour), "", 10)
	if len(updatedExpenses) != 1 || updatedExpenses[0].AmountVND != 125000 || updatedExpenses[0].Description != "train" {
		t.Fatalf("expense state=%+v", updatedExpenses)
	}
	updatedNotes, _ := store.ListNotes(ctx, alice, 10)
	if len(updatedNotes) != 1 || updatedNotes[0].Content != "corrected note" {
		t.Fatalf("note state=%+v", updatedNotes)
	}
	updatedJournal, _ := store.ListJournal(ctx, alice, now.Add(-24*time.Hour), now.Add(24*time.Hour), 10)
	if len(updatedJournal) != 1 || updatedJournal[0].Content != "corrected journal" {
		t.Fatalf("journal state=%+v", updatedJournal)
	}
	updatedReminders, _ := store.ListReminders(ctx, alice, "", "active", 10)
	if len(updatedReminders) != 1 || updatedReminders[0].Title != "corrected reminder" {
		t.Fatalf("reminder state=%+v", updatedReminders)
	}

	if err := store.CancelScheduledItem(ctx, bob, reminders[0].ID); err == nil {
		t.Fatal("cross-owner scheduled cancel unexpectedly succeeded")
	}
	if err := store.CancelScheduledItem(ctx, alice, reminders[0].ID); err != nil {
		t.Fatal(err)
	}
	allReminders, err := store.ListReminders(ctx, alice, "", "all", 10)
	if err != nil || len(allReminders) != 1 || allReminders[0].Status != "cancelled" {
		t.Fatalf("cancelled reminders=%+v err=%v", allReminders, err)
	}
}
