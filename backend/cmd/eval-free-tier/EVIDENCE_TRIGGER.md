# #23 Free-Tier Evidence Trigger

This file intentionally triggers the trusted `model-free-tier-evidence` pull-request workflow after the benchmark harness landed on `main`.

- Base main: `91a7ace7c4f1aa3163e61fd9ec58190b1739bc5d`
- Scope: measured tool/action evidence only
- Allowed hosted candidates: `gemma-4-26b-a4b-it`, `gemma-4-31b-it`
- Spend policy: zero paid inference; no priced provider tools; no retries
- Production selection remains unchanged until evidence review
