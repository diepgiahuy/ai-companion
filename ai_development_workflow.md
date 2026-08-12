# 2026 Gold-Standard Workflow for AI-Driven Development

This document outlines the strict, professional lifecycle for delegating complex tasks to autonomous AI Agents. By following this pipeline, we ensure that AI-generated code is robust, adheres to state-of-the-art (SotA) standards, and is fully verified before hitting the production `main` branch.

## 🛑 Core Axiom: Delegate, Don't Outsource
In 2026, the defining standard for AI coding is **human ownership**. The AI acts as a collaborative co-author, not a blind contractor.
*   **No "Vibe Coding":** Letting the AI guess until it works is an anti-pattern. All AI decisions must be grounded in `COMMERCIAL_ARCHITECTURE.md`, `BOM.md`, and strict constraints.
*   **The Zero Assumption Policy:** Never guess APIs, hardware states, or architectural patterns. If a requirement is ambiguous, the AI MUST stop and ask the human or read the source code.
*   **Evidence-Driven Foundation (CP-AUDIT0):** We do not just write tests; we produce *Evidence*. Every PR must track its state in `evidence/status.json`. Code without real-provider or physical HIL evidence remains strictly `UNPROVEN`.

---

## 🧭 Phase 0: CP Milestone Alignment

Before any requirements are gathered, the AI MUST identify where the requested feature fits within the **Commercial Production (CP) Milestone Roadmap** (e.g., `CP-VOICE1`, `CP-SAFE1`).
*   **Strict Blocking Rule:** The AI is FORBIDDEN from implementing a feature if its underlying CP milestone dependency is still `UNPROVEN`. (e.g., Cannot build UI for Voice if `CP-VOICE1` is not yet proven).

---

## 📋 Phase 1: Requirement Gathering & Ambiguity Resolution

Before writing any code, a Planning Agent MUST generate a formal GitHub Issue. This Issue acts as a binding contract for the Coder Agents.

### The "1 Issue = 1 Agent Lead" Rule
Every Issue is assigned to a single **Lead Agent**. This agent holds absolute ownership of the Issue from planning to completion.
*   The Lead Agent **DOES NOT** dump all code into a single massive PR.
*   The Lead Agent orchestrates the work by breaking the Issue down into Stacked PRs (Phase 2) or spawning subagents (Phase 2.1).
*   If multiple Issues are open concurrently, the Lead Agents MUST NOT step on each other's toes. They must respect file-level locking and CP Milestone dependencies.

Once the Issue is created and assigned, the Lead Agent proceeds to:

1. **SotA Research:** The Planning Agent must research the absolute latest technologies (e.g., LovyanGFX, Kalman Filters) and forbid legacy solutions.
2. **Context & Hardware State:** Clearly define the "why" and explicitly list any hardware additions/replacements (e.g., Mac M1 Runner, ESP32 USB).
3. **Strict Acceptance Criteria (AC):** Write testable conditions (e.g., *WHEN button GPIO40 is held, THEN transition to VoiceMail state*).
4. **Architectural Validation:** The agent must cross-reference `COMMERCIAL_ARCHITECTURE.md` to ensure zero violations (e.g., using Transactional Outbox pattern, no unauthorized mocks).

> [!IMPORTANT]
> The human MUST review and approve this Issue. Once approved, the Issue is locked and handed off to the execution phase.

---

## ✂️ Phase 2: Stacked PRs (Scientific Task Decomposition)

To prevent AI "hallucinations" and un-reviewable massive diffs, the industry standard is to use **Stacked Pull Requests** (chains of small, dependent PRs). The Coder Agent MUST slice the approved Issue across all architectural layers:

### The Multi-Dimensional Breakdown Strategy
1. **Layer 0 - Schema & Database (If applicable):**
    * *Architecture Contract:* SQLite (POC) -> Postgres (Prod).
    * *Strict Rule:* DO NOT use full Event Sourcing. MUST use the **Transactional Outbox** pattern (state mutations create durable outbox events in the same DB transaction).
2. **Layer 1 - Protocol & Contract (The Synchronization Point):**
    * *Architecture Contract:* **Device Twin** pattern (`desired`, `reported`, `config_version`).
    * *2026 Frameworks (PR #1):* Must support **WebRTC Opus Bridge** alongside WebSockets for ultra-low latency. UI state uses the typed **`ui_state` emotion protocol** via streaming server emission.
    * *Strict Rule:* Config resolution order (built-in -> global -> tenant -> plan -> user -> device) must be respected. Do NOT mix feature flags with authorization.
3. **Layer 2 - Firmware Core (Logic & State):**
    * *Architecture Contract:* Modular ESP-IDF logic. The LLM is a reasoning/composition component, never the authoritative database.
    * *2026 Frameworks (PR #1):* The AI must interface with the backend via the **Semantic Embedding Router** and the **Official MCP (Model Context Protocol) SDK Bridge**. Destructive auth requires canonical args hash + expiry confirmation scope.
    * *Strict Rule:* Must be hardware-agnostic C/C++ logic.
4. **Layer 3 - Firmware HAL (Hardware Abstraction):**
    * *Architecture Contract:* ESP-SR (WakeNet9, AEC), I2S, I2C, BLE.
    * *Strict Rule:* Must mock physical peripherals for Tier 1 tests.
5. **Layer 4 - Firmware UI (Presentation):**
    * *Architecture Contract:* **LovyanGFX** for DMA-based, tearing-free display rendering. BCP-47 locale tags (`vi-VN`).
    * *Strict Rule:* No business logic allowed in the UI layer.
6. **Layer 5 - Integration (E2E):**
    * *Architecture Contract:* Physical Hardware-in-the-Loop (HIL) testing.
    * *Strict Rule:* Must use `pytest-embedded` on the Mac M1 self-hosted runner. Must test Secure Boot v2/eFuse compatibility if touching OTA.

---

## 🚀 Phase 2.1: Multi-Agent Orchestration (Parallel Execution)

To drastically speed up development without causing Git merge conflicts or logical collisions, the project utilizes a **Swarm Orchestration Pattern**:

1. **Contract-First Synchronization (The Bottleneck):** Multiple agents CANNOT work in parallel until **Layer 1 (Protocol & Contract)** is defined and merged. The API schema, Go interfaces, and `evidence/status.json` structure must be locked first.
2. **Horizontal Slicing (Independent PRs):** Once the contract is locked, the Lead Agent spawns independent Subagents (e.g., `invoke_subagent`). 
    * Agent A implements **Layer 2 (Backend Core)** in `branch-backend-feat`.
    * Agent B implements **Layer 4 (Firmware UI)** in `branch-firmware-ui`.
    * Because they code against the same locked Layer 1 contract, they will not logically collide.
3. **File-Level Locking:** Two agents MUST NEVER be assigned to modify the same file or directory concurrently. If a shared configuration file must be updated, the Lead Agent handles it before delegating the leaf nodes to Subagents.

### Execution Rule
*   **PR Size Limit:** No PR shall exceed 500 lines of code (excluding auto-generated code).
*   **Sequential Execution:** Layer N cannot be implemented until Layer N-1 is merged or mocked in the local branch.

---

## 🤖 Phase 3: AI Execution & Tiered CI/CD (The Sandbox)

The Coder Agent works iteratively inside its PR branch. **The Human does nothing during this phase.**

1. **Implementation:** The AI writes the code.
2. **Evidence Generation:** The AI updates `evidence/status.json` with the new feature block (defaulting to `UNPROVEN` unless physical/real-provider CI evidence is attached).
3. **Push & Trigger:** The AI pushes to GitHub, which automatically triggers a **Tiered CI/CD Pipeline**:
    * **Tier 1 (Fast Feedback - Virtual):** Runs `Govulncheck`, `CodeQL`, MISRA/Lint checks, Wokwi Simulation, and the `module-lock` workflow. If this fails, it rejects the build instantly.
    * **Tier 2 (Physical - HIL):** Triggers the **Mac M1 Self-Hosted Runner**. The runner builds the `.bin`, flashes the connected ESP32, and runs `pytest-embedded` tests on real hardware.
    * **Tier 3 (Evidence Gate - CP-AUDIT0):** Validates `evidence/status.json`. Code cannot be promoted to `PASS/PROVEN` without cryptographic or HIL evidence.
4. **Self-Healing Loop:** If CI fails, the AI must independently read the logs, fix the bug, and push again.

> [!CAUTION]
> The AI is strictly **FORBIDDEN** from merging its own PRs, even if all CI pipelines are green.

---

## 👁️ Phase 4: Human-in-the-Loop (HITL) Final Review (The Gatekeeper)

To avoid **The Cluster Gap** (Review Fatigue caused by AI generating code faster than humans can review it leading to "rubber-stamping" PRs), we mandate strict physical checkpoints. Once the CI is 100% green, the AI tags the PR as `Ready for Review` and generates a **Human Action Note**.

### Format of the Human Action Note:
The AI must leave a comment on the PR detailing exactly what the human needs to do:

> ### 🛑 HUMAN VERIFICATION REQUIRED
> **1. Flash Command:** 
> `idf.py flash monitor -p /dev/cu.usbserial-123` (or use WebSerial link).
> **2. Physical Test Steps:**
> - Bring two ESP32 devices within 5cm of each other.
> - Verify the "Squash and Stretch" animation plays smoothly at 60FPS.
> - Hold the physical button for 3 seconds and verify the Voice Mail icon appears.
> **3. Sign-off:**
> If everything functions flawlessly in the physical world, please Approve and Merge this PR.

1. **Human Action:** The developer follows the AI's exact physical test steps on their desk.
2. **Merge:** If the physical test passes, the human clicks "Merge". The feature is now safely in production.

---

## 📈 Summary of Roles

| Phase | Actor | Responsibility |
| :--- | :--- | :--- |
| **1. Requirement** | Human + Planning AI | Define SotA specs, AC, and architectural constraints. |
| **2. Decomposition**| Coder AI | Split task into manageable, sequential PRs. |
| **3. Execution** | Coder AI + CI/CD | Write code, run HIL tests, self-heal errors. |
| **4. Verification** | Human | Physically validate the hardware behavior and Merge. |
