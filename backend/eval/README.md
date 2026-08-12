# Companion LLM evaluation corpus

`scenarios.jsonl` is a deterministic routing/tool-exposure gate for Vietnamese/English companion intents. It does not claim model quality. Real-model A/B evaluation must additionally measure tool-selection accuracy, argument validity, task success, latency, token cost, and spoken-response quality against the same user scenarios before promotion.

Keep scenarios versioned with prompt/tool-schema/model versions so regressions can be attributed and rolled back.
