# Model development candidate

This file records one development-only model candidate for hardware testing.

It does not select a Production-v1 model.

## Candidate

- Model: `gemma-4-31b-it`.
- Protocol: OpenAI-compatible Chat Completions.
- Runtime path: Google ADK through `NewProvider` and `ToolRegistry`.
- Development endpoint: `https://generativelanguage.googleapis.com/v1beta/openai`.
- Use only non-sensitive development data with the hosted Free Tier endpoint.

## Verified repository evidence

The 30-case Companion tool benchmark passed all cases with zero provider failures.

The benchmark recorded zero forbidden tool calls and 100 percent task success.

The production ADK compatibility probe executed one read-only `ToolRegistry` call successfully.

Issue #267 proved explicit PostgreSQL retrieval with 57 of 57 deterministic cases.

No embedding model or vector database is required for the current Product-v1 retrieval path.

## Production boundary

The hosted Gemma 4 API does not satisfy the current Production-v1 privacy boundary.

Google documents Gemma 4 API access as Free Tier only.

Google documents Free Tier content as usable to improve Google products.

Issue #23 remains open until a privacy-safe production runtime has comparable evidence.

## Hardware-test configuration

Set the following development variables:

```text
ADK_OPENAI_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai
ADK_MODEL=gemma-4-31b-it
ADK_MODEL_PROTOCOL=chat_completions
ADK_OPENAI_API_KEY=<development-key>
```

Use test accounts and non-sensitive data only.

Do not copy this configuration into Production-v1 without a reviewed #23 decision.
