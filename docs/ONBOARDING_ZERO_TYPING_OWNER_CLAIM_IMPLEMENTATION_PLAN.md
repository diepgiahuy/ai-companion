# Zero-Typing Owner Claim — Implementation Plan

## Status

This document defines an implementation plan for zero-typing owner claim.

The verified baseline is `main@e31fb1cff75d82ab3740de758dab439cad5bc967` on 2026-08-18.

The implementation agent must revalidate the current repository before every code change.

If current repository truth conflicts with this document, the repository wins for implementation facts.

The focused GitHub Issue remains the source of truth for required output and acceptance.

## Goal

A new owner completes onboarding without a manual claim code.

The owner enters only Wi-Fi data during local setup.

The device creates its own approval session after network access exists.

The owner scans a QR code when selected hardware can display it reliably.

The owner signs in through the existing OIDC flow.

The owner verifies a short code that matches the device display.

The owner explicitly approves the Companion.

The device receives a short-lived claim authorization from the backend.

The existing device-claim transaction issues the long-lived device credential.

The normal `/v2/device` path authenticates the device.

## Verified baseline

The current backend already has owner authentication with OIDC and PKCE.

The current backend already has owner sessions and CSRF protection.

The current backend already binds claim authorization to `(bootstrap_id, device_id)`.

The current backend already exposes the canonical `/v1/owner/device-claims` transaction.

The current firmware already performs the existing device-claim request.

The current firmware already derives its stable Wi-Fi MAC device identity.

The current firmware already persists a stable idempotency key for one setup attempt.

The current local setup portal already exposes `bootstrap_id` and `device_id` to the owner handoff.

The current manual UX uses one short-lived human claim code.

Issue #123 completed that one-code UX and explicitly excluded a QR requirement.

Issue #122 owns same-owner credential recovery through the existing claim transaction.

Issue #104 owns physical onboarding and recovery evidence.

Issue #3 owns the trusted physical ESP32-S3 HIL base.

Issue #8 owns selection of the Production-v1 board and display stack.

The current product display path uses an SSD1306 `128x32` display.

The current SSD1306 implementation provides text rendering but no QR renderer.

## Verified gap at the baseline

`backend/internal/pgstore/claim_code.go` currently wraps `MemoryClaimCodeStore`.

That implementation does not provide durable PostgreSQL claim-code state across backend processes.

The implementation agent must revalidate this gap against current `main`.

If the gap still exists, the agent must fix it before zero-typing approval can claim restart safety.

## Architecture decision

Do not replace the current claim architecture with a new greenfield credential system.

Keep the existing owner authentication boundary.

Keep the existing claim-authorization boundary.

Keep the existing `/v1/owner/device-claims` transaction.

Keep the existing device credential model.

Keep the existing `/v2/device` authentication path.

Add one durable approval-session layer before the existing claim-authorization boundary.

The approval session removes manual transfer of the human claim code.

The approval session does not issue the device credential.

The approval session does not become a second device authentication protocol.

## Target flow

```text
Factory-new Companion
    |
    v
SoftAP local setup
    |
    v
Owner enters Wi-Fi credentials
    |
    v
Device joins home Wi-Fi
    |
    v
Device creates approval session
    |
    +--> backend returns device_code
    |
    +--> backend returns user_code
    |
    +--> backend returns verification_uri_complete
    |
    v
Device shows QR plus user_code
    |
    v
Owner scans QR
    |
    v
Owner signs in with existing OIDC + PKCE
    |
    v
Owner verifies matching user_code
    |
    v
Owner approves Companion
    |
    v
Device polls approval session
    |
    v
Backend returns short-lived claim_authorization
    |
    v
Device calls existing /v1/owner/device-claims
    |
    v
Existing atomic claim transaction issues or rotates credential
    |
    v
Device persists credential
    |
    v
Device opens authenticated /v2/device WSS
    |
    v
READY
```

## External protocol reference

Use RFC 8628 as the protocol pattern for device approval.

Verify RFC 8628 from the current RFC Editor source before implementation.

Use `device_code` as the high-entropy device secret.

Use `user_code` as the human verification value.

Use `verification_uri_complete` for QR handoff.

Use bounded device polling.

Use explicit pending, denied, expired, and slow-down outcomes.

Do not implement an OAuth token grant unless current repository architecture requires it.

Map the RFC pattern to the existing Companion claim-authorization model.

## Phase 0 — Mandatory preflight

Fetch exact current `main` before edits.

Record the exact baseline SHA in the PR description.

Fetch parent issue #91.

Fetch issue #123.

Fetch issue #122.

Fetch issue #104.

Fetch issue #3.

Fetch issue #8.

Search for a newer focused zero-typing or QR onboarding issue.

Search open PRs that touch owner authentication or onboarding.

Inspect current owner-auth routes.

Inspect current claim-authorization code.

Inspect current claim transaction code.

Inspect current PostgreSQL migrations.

Inspect current firmware provisioning state.

Inspect current local setup portal.

Inspect the exact ESP-IDF version from repository configuration.

Inspect the current display path and current #8 decision state.

Verify version-sensitive external facts from official primary sources.

Classify drift as `VALID`, `NON_MATERIAL_DRIFT`, `MATERIAL_DRIFT`, or `NEEDS_DECISION`.

If a material fact remains unknown, stop code edits and create a focused spike or decision.

## Phase 1 — Focused GitHub Issue

Search for an existing focused issue before creating a new issue.

If no focused issue exists, create one under #91.

Use this title:

`[Onboarding/UX] Replace manual claim-code transfer with zero-typing owner approval`

Classify the implementation as L3 risk.

The change crosses authentication, short-lived secrets, persistence, concurrency, replay, firmware state, and credential issuance.

Do not branch from parent #91 without a focused implementation issue.

## Phase 2 — Durable approval-session store

Use PostgreSQL as the sole authoritative product store.

Do not add Redis.

Do not add an in-memory production fallback.

Use the current migration process.

Use current repository naming conventions for the final schema.

A logical session record needs these properties:

```text
session_id
device_code_hash
user_code_hash
bootstrap_binding
device_id
owner_user_id
status
expires_at
approved_at
consumed_at
created_at
last_poll_at
```

Do not store raw `device_code`.

Do not store the long-lived device credential in this session table.

Use the repository secret-hash convention for opaque secrets.

Use a bounded state model.

```text
PENDING -> APPROVED -> CONSUMED
PENDING -> DENIED
PENDING -> EXPIRED
APPROVED -> EXPIRED when authorization delivery never completes
```

Do not allow a transition back to `PENDING`.

Do not reuse an expired session.

Make approval state durable across process restart.

Make approval state available across multiple backend instances.

## Phase 3 — Device approval-session creation API

Add one device-facing session creation operation.

Use current route conventions for the final route name.

A candidate route is:

```http
POST /v1/device-claim-sessions
```

A candidate request is:

```json
{
  "device_id": "...",
  "bootstrap_id": "..."
}
```

A candidate response is:

```json
{
  "device_code": "<high-entropy-secret>",
  "user_code": "K7X-9M2",
  "verification_uri": "https://companion.example/claim",
  "verification_uri_complete": "https://companion.example/claim?s=...",
  "expires_in": 300,
  "interval": 5
}
```

Generate `device_code` with cryptographic randomness.

Generate `user_code` with sufficient entropy for the selected rate limit.

Bind the session to exact `(bootstrap_id, device_id)` intent.

Do not include raw `device_code` in `verification_uri_complete`.

Use an opaque public session reference inside the complete URI.

Do not expose owner identity before approval completes.

Do not expose any long-lived credential through this API.

## Phase 4 — Owner browser handoff

Reuse the existing OIDC and PKCE implementation.

Reuse the existing owner session.

Reuse the existing CSRF boundary.

Do not add a second owner-auth framework.

Add one owner-facing claim page.

Use current owner-web route conventions for the final route.

The page must display the short `user_code`.

The page must request explicit owner approval.

The page must provide a deny action.

The page must not expose `device_code`.

The page must not expose `claim_authorization`.

The page must not expose the device credential.

The page must not write claim secrets to localStorage.

### Safe post-login continuation

The current baseline OIDC callback returns JSON after successful login.

Zero-typing QR handoff needs a safe return to the original claim page.

Store the continuation server-side with the login transaction.

Allow only same-origin application paths.

Reject absolute external URLs.

Reject protocol-relative URLs.

Reject malformed paths.

Add deterministic open-redirect tests.

After successful OIDC login, return to the original claim page.

### Approval mutation

Use one authenticated mutation for approval.

A candidate route is:

```http
POST /v1/owner/device-claim-sessions/{session}/approve
```

Require an authenticated owner session.

Require the canonical CSRF token.

Require an unexpired approval session.

Require `PENDING` state.

Write `owner_user_id` only after successful approval.

Reject duplicate approval after the terminal state.

Reject another owner when existing ownership rules deny that owner.

## Phase 5 — Device polling API

Add one device-facing poll operation.

Use current route conventions for the final route name.

A candidate route is:

```http
POST /v1/device-claim-sessions/token
```

A candidate request is:

```json
{
  "device_code": "..."
}
```

Support these semantic outcomes:

```text
authorization_pending
slow_down
access_denied
expired_token
approved
```

Use repository-native error formats when they already define equivalent behavior.

Enforce a minimum poll interval.

Enforce poll limits across backend instances.

Expire sessions deterministically.

Reject an invalid `device_code`.

Reject a consumed session.

Reject an expired session.

Do not reveal owner details through polling responses.

## Phase 6 — Bridge approval to existing claim authorization

Do not issue the device credential from the approval-session endpoint.

After approval, produce the existing short-lived claim authorization.

Keep the existing `/v1/owner/device-claims` transaction.

Keep its exact `(bootstrap_id, device_id)` authorization check.

Keep its idempotency-key behavior.

Keep its encrypted credential-delivery behavior.

Keep same-owner recovery in the existing transaction.

Keep different-owner rejection in the existing transaction.

If current claim authorization remains process-local, add a PostgreSQL-backed opaque authorization store.

Do not replace the opaque authorization with JWT or PASETO without a separate verified decision.

Preserve the existing `ClaimAuthorizer` boundary where practical.

## Phase 7 — Firmware provisioning state

Inspect the current `PendingConfig` before edits.

Replace the manual-code phase with durable approval-session state.

A target logical model can contain:

```text
bootstrap_id
device_code
claim_session_id
claim_authorization
idempotency_key
server_url
expires_at
poll_interval
```

Adapt the final layout to verified NVS size constraints.

Do not persist raw owner session data on the device.

Do not persist browser CSRF data on the device.

Use the existing `ProvisioningStore`.

Do not create a second firmware storage system.

### Target firmware state flow

```text
UNPROVISIONED
    -> SETUP
    -> CONNECTING_WIFI
    -> CREATE_APPROVAL
    -> WAITING_OWNER
    -> CLAIMING
    -> VALIDATING
    -> READY
```

Keep retry state explicit.

Do not create unlimited new approval sessions after expiry.

After expiry, show an explicit setup or retry action.

Preserve the stable idempotency key across claim retries.

## Phase 8 — Local SoftAP simplification

The current local page requests Wi-Fi, backend URL, and a claim code.

Remove the claim-code input after the new path reaches cutover.

The target local form must contain Wi-Fi credentials.

Keep backend selection only when current product requirements still require it.

Verify the production backend-origin decision before removing that field.

Do not hard-code an assumed production hostname.

Keep the setup-session nonce protection.

Keep the random WPA2 SoftAP password.

Keep `WIFI_STORAGE_RAM` behavior unless current architecture changed.

## Phase 9 — Power-loss and restart safety

Persist each meaningful state before the next external network boundary.

Persist the setup attempt before the device leaves local setup.

Persist the approval session before the first poll.

Persist claim authorization before the credential request.

Persist the device credential before runtime validation.

Erase temporary approval secrets after credential commitment.

Use atomic state transitions in the existing provisioning store.

A restart must resume from one deterministic persisted state.

A restart must not invent a second ownership transaction.

A response loss must not create another credential rotation with the same request.

## Phase 10 — QR presentation gate

Do not implement final QR presentation before #8 selects the Production-v1 display stack.

The baseline SSD1306 display is `128x32`.

The baseline renderer supports text only.

Software support for `verification_uri_complete` can merge before final QR rendering.

Before #8 completes, display the short verification code and approval status only.

Do not claim physical zero-typing usability before physical proof exists.

After #8 selects the display, add QR rendering to the selected display path.

Encode only `verification_uri_complete` in the QR code.

Display `user_code` near the QR code.

Measure phone scan reliability on the real display.

Measure scan reliability at the real enclosure distance.

Do not add a second permanent graphics runtime.

## Phase 11 — Same-owner recovery

Reuse issue #122 semantics.

Do not create another recovery protocol.

A same-owner approval must feed the existing claim transaction.

The existing transaction must rotate one credential atomically.

The old credential must fail after committed rotation.

A retry with the same idempotency key must return the same delivery outcome.

A different owner must receive deterministic rejection.

A different owner must not mutate ownership.

## Phase 12 — Old manual claim-code path removal

Search current deployed firmware compatibility requirements before removal.

Verify whether any released firmware still calls the old claim-code routes.

If no released client depends on them, remove the old manual-code path after integrated proof.

If released clients depend on them, define one bounded compatibility sunset.

Do not keep the old path indefinitely.

Do not keep two permanent product onboarding flows.

Do not restore the old path as a hidden production fallback.

## Required backend tests

Add deterministic tests for session creation.

Add deterministic tests for unique `device_code` generation.

Add deterministic tests for unique `user_code` generation.

Add deterministic tests for exact device and bootstrap binding.

Add deterministic tests for session expiry.

Add deterministic tests for approval.

Add deterministic tests for denial.

Add deterministic tests for duplicate approval.

Add deterministic tests for approval after expiry.

Add deterministic tests for poll before approval.

Add deterministic tests for poll after approval.

Add deterministic tests for poll after denial.

Add deterministic tests for poll after expiry.

Add deterministic tests for poll rate limits.

Add deterministic tests for `slow_down` behavior.

Add deterministic tests for unknown `device_code`.

Add deterministic tests for replay after consume.

Add deterministic tests for concurrent poll requests.

Add deterministic tests for concurrent approval requests.

Add real PostgreSQL tests across process restart boundaries.

Add real PostgreSQL tests across two logical backend instances.

Add same-owner recovery tests.

Add different-owner rejection tests.

Add CSRF rejection tests.

Add unauthenticated approval tests.

Add OIDC continuation tests.

Add open-redirect rejection tests.

Add secret-safe response tests.

Mocks do not prove PostgreSQL persistence or cross-instance behavior.

## Required firmware tests

Add host tests for factory-new state.

Add host tests for Wi-Fi failure.

Add host tests for backend unavailability.

Add host tests for approval-session creation failure.

Add host tests for pending approval.

Add host tests for approval expiry.

Add host tests for approval denial.

Add host tests for poll retry.

Add host tests for reboot during a pending session.

Add host tests for reboot after approval.

Add host tests for response loss.

Add host tests for claim retry.

Add host tests for stable idempotency reuse.

Add host tests for claim conflict.

Add host tests for credential commit.

Add host tests for temporary-secret erase.

Add host tests for runtime authentication.

Add host tests for factory reset.

Add host tests for Wi-Fi-only reprovision.

Test state transitions directly.

Do not use log text as the only state oracle.

## Required software integration path

Run one real production-boundary software path.

Use the real Go server composition.

Use real PostgreSQL.

Use the canonical owner-auth router.

Use the canonical device-claim route.

Use the canonical `/v2/device` route.

The integrated scenario must follow this path:

```text
software device
    -> real Go backend
    -> real PostgreSQL
    -> create approval session
    -> authenticated owner approval
    -> device poll
    -> existing device claim
    -> device credential
    -> authenticated /v2/device
    -> READY
```

Do not bypass the production router for final boundary evidence.

## Required security review

Verify a stolen public QR URI cannot approve a device without owner authentication.

Verify a guessed `user_code` cannot create a credential.

Verify a stolen `device_code` remains bounded by expiry and exact session semantics.

Verify expired sessions fail closed.

Verify consumed sessions fail closed.

Verify cross-device substitution fails.

Verify cross-bootstrap substitution fails.

Verify cross-owner approval follows ownership rules.

Verify missing CSRF fails.

Verify invalid CSRF fails.

Verify open redirects fail.

Verify poll flooding receives bounded behavior.

Verify response loss remains idempotent.

Verify concurrent consume cannot issue two different outcomes.

Verify backend restart keeps durable approval state.

Verify database failure cannot create ambiguous ownership state.

Verify logs do not contain `device_code`.

Verify logs do not contain `claim_authorization`.

Verify logs do not contain device credentials.

Verify logs do not contain Wi-Fi passwords.

Verify logs do not contain owner session or CSRF secrets.

## CI and evidence

Treat this change as L3 until final risk review lowers a proven boundary.

Use the nearest deterministic test after each coherent change.

Run final affected backend tests before review.

Run final affected firmware tests before review.

Run one real PostgreSQL integration gate.

Run the canonical Tier-1 software-device path.

Run an independent final integrated diff review.

Require the exact-head PR Gate before merge.

Do not claim physical HIL evidence from software tests.

Do not trigger personal HIL from untrusted PR code.

## Physical qualification

Issue #104 remains the physical onboarding evidence owner.

Issue #3 remains the trusted HIL base owner.

Update #104 scenarios after the new software path merges.

Physical HIL must prove factory-new setup.

Physical HIL must prove SoftAP usability.

Physical HIL must prove home Wi-Fi join.

Physical HIL must prove QR scan usability after #8 selects hardware.

Physical HIL must prove OIDC return to the claim page.

Physical HIL must prove visible code match.

Physical HIL must prove explicit owner approval.

Physical HIL must prove credential installation.

Physical HIL must prove authenticated WSS READY.

Physical HIL must prove controlled power-loss recovery.

Physical HIL must prove Wi-Fi-only reprovision.

Physical HIL must prove same-owner recovery.

Physical HIL must prove different-owner rejection.

Physical HIL must prove local factory-reset semantics.

Do not mark QR usability PASS from host tests.

## Recommended PR split

Use one accountable lead for the whole feature.

Do not parallelize product-code lanes before the shared contract stabilizes.

### PR 1 — Durable approval protocol

Include PostgreSQL approval-session state.

Include device session APIs.

Include OIDC continuation.

Include owner approval.

Include device polling.

Include the claim-authorization bridge.

Include real PostgreSQL integration tests.

### PR 2 — Firmware cutover

Start only after PR 1 stabilizes the contract.

Include firmware state changes.

Include provisioning-store changes.

Include SoftAP simplification.

Include the poll client.

Reuse the existing device-claim transaction.

Include host tests.

Include Tier-1 integration.

Remove the old code path when compatibility permits removal.

### PR 3 — Selected-display QR presentation

Start only after issue #8 selects the display stack.

Include the QR renderer for the selected display.

Include visible `user_code` presentation.

Include physical scan evidence hooks.

Do not create this PR before the hardware decision exists.

## Rollback

Backend rollback must preserve authoritative owner-device bindings.

Backend rollback must preserve committed device credentials.

Rollback must not restore a shared credential.

Rollback must not restore an in-memory PostgreSQL fallback.

If approval sessions fail, disable new session creation.

If firmware cutover fails, revert firmware through the normal reviewed release path.

Do not delete authoritative ownership data during onboarding rollback.

Expire temporary approval rows according to the bounded session TTL.

## Definition of Done

The human does not enter a claim code.

The device creates its own approval session.

The browser uses the existing OIDC and PKCE flow.

OIDC returns safely to the original claim page.

The human verifies the displayed short code.

The human explicitly approves the Companion.

PostgreSQL owns durable approval state.

Device polling survives backend restart.

Multi-instance retry remains deterministic.

The device receives only a short-lived claim authorization from the approval layer.

The existing `/v1/owner/device-claims` transaction issues or rotates the credential.

The browser never receives the device credential.

The device persists the credential.

The normal `/v2/device` path authenticates the credential.

The device reaches Protocol v2 READY.

Same-owner recovery rotates the credential atomically.

A different owner cannot transfer ownership through this flow.

The old manual path is removed or has a verified bounded sunset.

The exact-head PR Gate passes.

The final independent review returns PASS.

Physical QR onboarding remains incomplete until #8 and #104 provide real hardware evidence.

## Agent execution guard

Use this section as mandatory execution policy for the implementation agent.

Before any edit, fetch exact current `main` and all referenced issues.

Treat current `main` code and schema as implementation truth.

Treat the focused GitHub Issue as the required output.

Do not trust this plan when current repository facts conflict with it.

If a material fact changed, classify the drift before code changes.

Do not redesign `/v1/owner/device-claims` without verified necessity.

Do not redesign `/v2/device` without verified necessity.

Keep the existing owner-device credential boundary.

Use PostgreSQL for durable approval state.

Do not add Redis.

Do not add BLE provisioning.

Do not add a native mobile app.

Do not add an SSE credential-delivery path.

Do not add another permanent provisioning path.

Do not expose `device_code` in logs.

Do not expose `claim_authorization` in logs.

Do not expose device credentials in logs.

Do not expose Wi-Fi passwords in logs.

Do not expose owner session or CSRF secrets in logs.

Do not implement final QR rendering before the selected Production-v1 display is verified.

Use one accountable implementation lead.

Use the narrowest objective oracle after each coherent change.

Use real PostgreSQL for persistence, replay, concurrency, and restart evidence.

Run an independent final diff review because this change affects authentication and credentials.

Do not claim physical QR or browser usability from software tests.

Update the PR description with actual implementation facts.

Update the PR description with actual commands and results.

Update the PR description with meaningful risks and rollback.

Update the PR description with remaining physical gates.

## Repository references to revalidate

- `AGENTS.md`
- `ai_development_workflow.md`
- `docs/TEST_EVIDENCE_LADDER.md`
- `backend/internal/ownerauth/claim_code.go`
- `backend/internal/ownerauth/claim_authorization.go`
- `backend/internal/ownerauth/claim_store.go`
- `backend/internal/ownerauth/ownerauth.go`
- `backend/internal/onboarding/claim.go`
- `backend/internal/pgstore/claim_code.go`
- `components/esp32_provisioning/include/companion/provisioning_fsm.hpp`
- `components/esp32_provisioning/src/claim_client.cpp`
- `components/esp32_provisioning/src/setup_portal.cpp`
- `components/esp32_board/src/ssd1306_display.cpp`
- issue #91
- issue #123
- issue #122
- issue #104
- issue #3
- issue #8
- RFC 8628 from the RFC Editor
- current Espressif documentation for the exact ESP-IDF version
