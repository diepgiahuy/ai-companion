# GitHub Issue Generation for AI Delegation

Whenever the user asks to create or write a GitHub issue, requirement document, or ticket that is intended to be picked up by an AI Agent, you MUST follow this strict template and best practices.

## Best Practices for AI Delegation
1. **Mandatory State-of-the-Art (SotA) Research**: Before writing the issue, the AI MUST search the web for the absolute latest (2026+) technologies, algorithms, optimizations, and frameworks relevant to the requirement. Do not use legacy solutions. (e.g., use LovyanGFX instead of TFT_eSPI; use Kalman filters for RSSI; use Testcontainers for DB tests).
2. **Context & Motivation**: Always explain the "why" so the AI doesn't hallucinate edge cases.
3. **Explicit Hardware/BOM State**: Clearly state what hardware is being [ADDED] or [REPLACED].
3. **Acceptance Criteria (AC)**: Write AC as strict, testable assertions (e.g., "When X happens, Y must occur"). Do not use vague terms like "make it look nice".
4. **Architectural Pointers**: Explicitly list the files or directories the AI should modify to limit its search space and prevent it from inventing new architectures.
5. **Architectural Policy Check**: Always read `COMMERCIAL_ARCHITECTURE.md` or equivalent policy documents beforehand. Ensure the issue does not violate Eventing (e.g., Transactional Outbox) or Privacy rules.
6. **Testing Strategy (No Mocks)**: Enforce the use of Ephemeral Environments (Testcontainers) and Hardware-in-the-Loop (HIL/Emulators) over Unit Test Mocks.
7. **AI Review Loop**: Require the AI to self-correct using raw `stdout`/`stderr` from CI, without asking humans for help on syntax/lint errors.
8. **Human-in-the-Loop (HITL) Checkpoints**: Clearly list the specific gates where the AI must STOP and wait for human physical verification or business approval.
9. **PR Splitting Strategy**: Break the large issue down into sequential, non-overlapping Pull Requests. Justify the split using Computer Science Principles (e.g., SOLID, Separation of Concerns).
10. **Anti-Patterns**: Explicitly list what the AI is FORBIDDEN to do (e.g., Do NOT use `delay()`, Do NOT use `gomock`).

## Template Structure
Use Markdown with the following H2 sections:
- `## 📖 Context & Motivation`
- `## 🛠 Hardware Updates (BOM Changes)`
- `## 🏗 Features & Acceptance Criteria (AC)`
- `## 📁 Architectural Code Pointers for the AI Agent`
- `## 🧪 Testing & Verification Strategy (NO MOCKS)`
- `## 🔄 AI Self-Healing Review Loop`
- `## 🙋‍♂️ Human-In-The-Loop (HITL) Checkpoints`
- `## 🔀 Execution Plan (PR Splitting Strategy)`
- `## 🚫 Anti-Patterns (Do NOT do this)`
