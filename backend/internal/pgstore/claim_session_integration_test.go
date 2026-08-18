package pgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"companion-server/internal/ownerauth"
)

func TestPostgresClaimSessionLifecycleAndConcurrency(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("claim-sess-%d", time.Now().UnixNano())
	sessionID := prefix + "-sess"
	deviceID := prefix + "-device"
	bootstrapID := prefix + "-boot"
	deviceCode := prefix + "-device-secret-12345678"
	deviceCodeHash := ownerauth.HashSecret(deviceCode)
	userCode := "K7X-9M2"
	userCodeHash := ownerauth.HashSecret(userCode)
	ownerUserID := prefix + "-owner"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_claim_sessions WHERE session_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM claim_rate_limits WHERE rate_key LIKE $1`, prefix+"%")
	})

	sessionRecord := ownerauth.ClaimSessionRecord{
		SessionID:      sessionID,
		DeviceID:       deviceID,
		BootstrapID:    bootstrapID,
		DeviceCodeHash: deviceCodeHash,
		UserCodeHash:   userCodeHash,
		UserCodePlain:  userCode,
		Status:         ownerauth.ClaimSessionPending,
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}

	// 1. Create session
	if err := store.CreateClaimSession(ctx, sessionRecord); err != nil {
		t.Fatalf("create claim session: %v", err)
	}

	// 2. Fetch by session ID
	fetched, err := store.GetClaimSessionByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("get by session id: %v", err)
	}
	if fetched.DeviceID != deviceID || fetched.UserCodePlain != userCode || fetched.Status != ownerauth.ClaimSessionPending {
		t.Fatalf("fetched session mismatch: %+v", fetched)
	}

	// 3. Fetch by device code hash
	byHash, err := store.GetClaimSessionByDeviceCodeHash(ctx, deviceCodeHash)
	if err != nil {
		t.Fatalf("get by code hash: %v", err)
	}
	if byHash.SessionID != sessionID {
		t.Fatalf("session id mismatch: %s != %s", byHash.SessionID, sessionID)
	}

	// 4. Poll while pending
	now := time.Now().UTC()
	outcome, err := store.PollClaimSession(ctx, deviceCodeHash, 5*time.Second, now, nil)
	if err != nil {
		t.Fatalf("poll pending session: %v", err)
	}
	if outcome.Status != ownerauth.PollOutcomePending {
		t.Fatalf("expected PollOutcomePending, got %s", outcome.Status)
	}

	// 5. Poll immediately again -> slow_down
	now = now.Add(1 * time.Second)
	slowOutcome, err := store.PollClaimSession(ctx, deviceCodeHash, 5*time.Second, now, nil)
	if err != nil {
		t.Fatalf("poll slow down: %v", err)
	}
	if slowOutcome.Status != ownerauth.PollOutcomeSlowDown {
		t.Fatalf("expected PollOutcomeSlowDown, got %s", slowOutcome.Status)
	}

	// 6. Approve session
	now = now.Add(6 * time.Second)
	if err := store.ApproveClaimSession(ctx, sessionID, ownerUserID, now); err != nil {
		t.Fatalf("approve claim session: %v", err)
	}

	// Duplicate approve should fail
	if err := store.ApproveClaimSession(ctx, sessionID, ownerUserID, now); err != ownerauth.ErrSessionAlreadyApproved {
		t.Fatalf("expected ErrSessionAlreadyApproved, got %v", err)
	}

	// 7. Poll after approval -> mint claim authorization and transition to consumed
	mintCalled := false
	expectedToken := prefix + "-claim-auth-token"
	expectedExp := now.Add(5 * time.Minute)
	mintFn := func(boot, dev, owner string) (string, time.Time, error) {
		if boot != bootstrapID || dev != deviceID || owner != ownerUserID {
			t.Fatalf("mintAuthFn args mismatch: %s %s %s", boot, dev, owner)
		}
		mintCalled = true
		return expectedToken, expectedExp, nil
	}

	approvedOutcome, err := store.PollClaimSession(ctx, deviceCodeHash, 5*time.Second, now.Add(6*time.Second), mintFn)
	if err != nil {
		t.Fatalf("poll approved session: %v", err)
	}
	if !mintCalled || approvedOutcome.Status != ownerauth.PollOutcomeApproved || approvedOutcome.ClaimAuthorization != expectedToken {
		t.Fatalf("expected approved outcome with token, got %+v", approvedOutcome)
	}

	// 8. Poll after consume (idempotent retry for lost response)
	replayedOutcome, err := store.PollClaimSession(ctx, deviceCodeHash, 5*time.Second, now.Add(15*time.Second), nil)
	if err != nil {
		t.Fatalf("poll consumed session replay: %v", err)
	}
	if replayedOutcome.Status != ownerauth.PollOutcomeApproved || replayedOutcome.ClaimAuthorization != expectedToken {
		t.Fatalf("expected replayed token, got %+v", replayedOutcome)
	}

	// 9. Restarted store instance preserves the exact state
	restartedStore, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	restartOutcome, err := restartedStore.PollClaimSession(ctx, deviceCodeHash, 5*time.Second, now.Add(25*time.Second), nil)
	if err != nil {
		t.Fatalf("restart poll: %v", err)
	}
	if restartOutcome.Status != ownerauth.PollOutcomeApproved || restartOutcome.ClaimAuthorization != expectedToken {
		t.Fatalf("expected replayed token after restart, got %+v", restartOutcome)
	}
}

func TestPostgresClaimSessionDenyAndExpiry(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("claim-deny-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_claim_sessions WHERE session_id LIKE $1`, prefix+"%")
	})

	// Test Deny
	denySessionID := prefix + "-deny-sess"
	denyCodeHash := ownerauth.HashSecret(prefix + "-deny-code")
	if err := store.CreateClaimSession(ctx, ownerauth.ClaimSessionRecord{
		SessionID:      denySessionID,
		DeviceID:       prefix + "-dev",
		BootstrapID:    prefix + "-boot",
		DeviceCodeHash: denyCodeHash,
		UserCodeHash:   ownerauth.HashSecret("USERCODE1"),
		UserCodePlain:  "USERCODE1",
		Status:         ownerauth.ClaimSessionPending,
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := store.DenyClaimSession(ctx, denySessionID, now); err != nil {
		t.Fatalf("deny session: %v", err)
	}

	denyOutcome, err := store.PollClaimSession(ctx, denyCodeHash, 5*time.Second, now, nil)
	if err != nil {
		t.Fatalf("poll denied session: %v", err)
	}
	if denyOutcome.Status != ownerauth.PollOutcomeDenied {
		t.Fatalf("expected PollOutcomeDenied, got %s", denyOutcome.Status)
	}

	// Test Expiry
	expireSessionID := prefix + "-exp-sess"
	expireCodeHash := ownerauth.HashSecret(prefix + "-exp-code")
	past := time.Now().UTC().Add(-10 * time.Second)
	if err := store.CreateClaimSession(ctx, ownerauth.ClaimSessionRecord{
		SessionID:      expireSessionID,
		DeviceID:       prefix + "-dev-exp",
		BootstrapID:    prefix + "-boot-exp",
		DeviceCodeHash: expireCodeHash,
		UserCodeHash:   ownerauth.HashSecret("USERCODE2"),
		UserCodePlain:  "USERCODE2",
		Status:         ownerauth.ClaimSessionPending,
		ExpiresAt:      past,
	}); err != nil {
		t.Fatal(err)
	}

	expOutcome, err := store.PollClaimSession(ctx, expireCodeHash, 5*time.Second, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("poll expired session: %v", err)
	}
	if expOutcome.Status != ownerauth.PollOutcomeExpired {
		t.Fatalf("expected PollOutcomeExpired, got %s", expOutcome.Status)
	}
}

func TestPostgresClaimRateLimits(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	claimCodeStore := NewPgClaimCodeStore(store)
	ctx := context.Background()
	host := fmt.Sprintf("test-host-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM claim_rate_limits WHERE rate_key = $1`, host)
	})

	limit := 3
	window := 1 * time.Minute

	for i := 1; i <= limit; i++ {
		allowed, err := claimCodeStore.AllowAttempt(ctx, host, limit, window)
		if err != nil {
			t.Fatalf("attempt %d error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}

	// 4th attempt should be denied
	allowed, err := claimCodeStore.AllowAttempt(ctx, host, limit, window)
	if err != nil {
		t.Fatalf("attempt 4 error: %v", err)
	}
	if allowed {
		t.Fatalf("attempt 4 should be denied")
	}
}

func TestPostgresConcurrentClaimSessionApproval(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("claim-conc-%d", time.Now().UnixNano())
	sessionID := prefix + "-sess"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_claim_sessions WHERE session_id = $1`, sessionID)
	})

	if err := store.CreateClaimSession(ctx, ownerauth.ClaimSessionRecord{
		SessionID:      sessionID,
		DeviceID:       prefix + "-dev",
		BootstrapID:    prefix + "-boot",
		DeviceCodeHash: ownerauth.HashSecret(prefix + "-code"),
		UserCodeHash:   ownerauth.HashSecret("CONCUSER"),
		UserCodePlain:  "CONCUSER",
		Status:         ownerauth.ClaimSessionPending,
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 5)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		wg.Add(1)
		owner := fmt.Sprintf("%s-owner-%d", prefix, i)
		go func() {
			defer wg.Done()
			results <- store.ApproveClaimSession(ctx, sessionID, owner, now)
		}()
	}
	wg.Wait()
	close(results)

	var successes, alreadyApproved int
	for res := range results {
		if res == nil {
			successes++
		} else if res == ownerauth.ErrSessionAlreadyApproved {
			alreadyApproved++
		} else {
			t.Fatalf("unexpected error in concurrent approval: %v", res)
		}
	}
	if successes != 1 || alreadyApproved != 4 {
		t.Fatalf("expected 1 success and 4 alreadyApproved, got %d and %d", successes, alreadyApproved)
	}
}
