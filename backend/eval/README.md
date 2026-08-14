# Companion model evaluation harness

`scenarios.jsonl` is the original Vietnamese/English routing corpus. The runner
keeps its `must_pack`, `fallback`, and `exact` fields compatible while allowing
new cases to describe tool calls, argument expectations, forbidden tools,
retrieval relevance, content assertions, and escalation ground truth.

The harness emits `companion.eval.report.v1` JSON with:

- per-trial normalized observations, provider errors, TTFT, and total latency;
- pack/tool precision, recall, F1, argument matching, supported JSON-schema
  validation, deterministic content checks, Recall@K, and nDCG;
- token usage and estimated cost only when usage and caller-supplied prices exist;
- escalation precision/recall, availability/failures, quality delta, latency
  penalty, and estimated cost penalty;
- corpus SHA-256 plus model/runtime/hardware/prompt/tool-schema metadata.

Reports always set `selection` to `not_selected`. There is no PASS verdict.
Mock reports use `evidence_class: synthetic`; they cannot establish real model
quality, latency, cost, or production readiness.

## Deterministic offline run

From `backend/`:

```bash
go run ./cmd/eval \
  -mode mock \
  -scenarios ./eval/scenarios.jsonl \
  -runs 2 \
  -out /tmp/companion-eval-mock.json
```

The existing corpus has no mock answers. The runner therefore emits empty
observations with explicit warnings instead of inferring answers from
expectations. Add an explicit fixture to a benchmark-only scenario when testing
the scoring pipeline:

```json
{"id":"timer","kind":"routing","input":"Hẹn giờ 10 phút","must_pack":["schedule"],"expect":{"escalate":false},"mock":{"primary":{"observation":{"packs":["schedule"],"escalate":false,"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}},"ttft_us":1000,"total_us":2500}}}
```

Identical scenario bytes, arguments, and mock fixtures produce identical report
bytes. No wall-clock timestamp or measured host timing is injected in mock mode.

## OpenAI-compatible measurement

The optional real-provider lane remains inside this harness and accepts a local
OpenAI-compatible Chat Completions endpoint:

```bash
go run ./cmd/eval \
  -mode openai \
  -scenarios ./eval/scenarios.jsonl \
  -endpoint http://127.0.0.1:8000/v1 \
  -provider mlx-lm \
  -model candidate-model \
  -model-version MODEL_ARTIFACT_SHA \
  -runtime 'mlx-lm VERSION' \
  -region local \
  -run-id M1-RUN-ID \
  -hardware 'Apple M1, RAM SIZE' \
  -runtime-config CONFIG_DIGEST \
  -prompt-version PROMPT_DIGEST \
  -tool-schema-commit GIT_SHA \
  -source-commit GIT_SHA \
  -runs 5 \
  -out /tmp/candidate-model.json
```

Streaming is enabled by default so TTFT can be measured. Plain HTTP is accepted
only for loopback endpoints; remote endpoints require HTTPS. API keys are read
from the environment variable named by `-api-key-env` and are never written to
the report. Prices are not guessed: pass `-input-usd-per-million` and
`-output-usd-per-million` to calculate an estimate from provider-reported usage.

An escalation endpoint is optional. Configure it with
`-escalation-endpoint` and `-escalation-model`; it is called only when the
primary normalized observation sets `escalate: true`.

## Extended scenario fields

`expect` supports:

- `packs`, `exact_packs`;
- `tool_calls` with expected argument subsets, `no_tool_call`, and
  `forbidden_tools`;
- `output_exact`, `must_contain`, and `must_not_contain`;
- `retrieval_ids` and `retrieval_k`;
- `escalate` as routing ground truth.

`tools` uses the OpenAI function-tool shape. Schema scoring deliberately covers
the deterministic subset needed by Companion tools: object/array/scalar types,
required properties, `additionalProperties`, enums, numeric bounds, item schema,
and item-count bounds. Unsupported schema keywords are not treated as evidence.

Keep scenarios versioned with prompt, tool-schema, model, quantization, runtime,
hardware, and source revisions so regressions can be attributed and rolled back.
