# Changelog

## v0.0.2

Compared with `v0.0.1`, this release turns `openLight` into a broader but still constrained operations agent for Raspberry Pi and small Linux hosts.

### Key Themes

- Broader skill surface: added file-management skills for listing, reading, writing, and replacing text in whitelisted paths, plus optional workbench execution for temporary code and exact allowlisted files.
- Stronger LLM integration: added native OpenAI provider support, an OpenAI config template, provider factory registration, and dedicated OpenAI skill selection via function calling.
- Redesigned routing: the runtime now uses deterministic routing first, then a two-stage LLM fallback that chooses a skill group before selecting one concrete skill. Route-stage confidence is the execution gate; the skill stage focuses on skill selection, argument extraction, and clarification.
- Better deployment ergonomics: expanded example configs with `files.*` and `workbench.*`, added an Ollama Docker Compose setup, and introduced `make` helpers for local Ollama startup, model pull, and end-to-end smoke testing.
- Better docs and confidence: refreshed `README.md` and `ARCHITECTURE.md`, expanded registry and module metadata, and added broad unit, integration, and end-to-end coverage across config, router, LLM, and agent flows.

### Notable Changes

- Added `openai` as a first-class LLM provider alongside `ollama` and `generic`.
- Added grouped built-in skills for `files` and `workbench`, including guarded execution via allowlisted runtimes and files.
- Extended natural-language routing with better normalization, semantic rules, and skill metadata for discovery and help.
- Simplified LLM execution semantics by removing the separate mutating execution threshold and using route-stage confidence as the single execution gate for tool groups.
- Simplified the skill-classification contract for `ollama` and `generic`: skill responses now carry `skill`, `arguments`, `needs_clarification`, and `clarification_question` without a separate skill-confidence score.

### Upgrade Notes

- Update existing configs from `v0.0.1` to include the new `files.*` and `workbench.*` sections.
- Remove `llm.mutating_execute_threshold` from configs and stop using `LLM_MUTATING_EXECUTE_THRESHOLD`; the setting is no longer supported.
- If you maintain a custom `generic` or `ollama`-compatible LLM backend, update skill-classification responses to the new schema without `confidence`.
- For OpenAI deployments, prefer `OPENAI_API_KEY` instead of storing `llm.api_key` in a tracked file.
