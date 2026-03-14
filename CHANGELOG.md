# Changelog

## v0.0.2

Compared with `v0.0.1`, this release turns `openLight` into a broader but still constrained operations agent for Raspberry Pi and small Linux hosts.

### Key Themes

- Broader skill surface: added file-management skills for listing, reading, writing, and replacing text in whitelisted paths, plus optional workbench execution for temporary code and exact allowlisted files.
- Stronger LLM integration: added native OpenAI provider support, an OpenAI config template, provider factory registration, and richer prompt/schema plumbing for structured route and skill classification.
- Redesigned routing: the runtime now uses deterministic routing first, then a two-stage LLM fallback that chooses a skill group before selecting one concrete skill, with clearer clarification behavior and stricter thresholds for mutating actions.
- Better deployment ergonomics: expanded example configs with `files.*` and `workbench.*`, added an Ollama Docker Compose setup, and introduced `make` helpers for local Ollama startup, model pull, and end-to-end smoke testing.
- Better docs and confidence: refreshed `README.md` and `ARCHITECTURE.md`, expanded registry and module metadata, and added broad unit, integration, and end-to-end coverage across config, router, LLM, and agent flows.

### Notable Changes

- Added `openai` as a first-class LLM provider alongside `ollama` and `generic`.
- Added grouped built-in skills for `files` and `workbench`, including guarded execution via allowlisted runtimes and files.
- Extended natural-language routing with better normalization, semantic rules, and skill metadata for discovery and help.
- Added `llm.mutating_execute_threshold` and increased decision token limits in the example configs.

### Upgrade Notes

- Update existing configs from `v0.0.1` to include the new `files.*` and `workbench.*` sections.
- Review `llm.mutating_execute_threshold` before enabling mutating LLM-routed skills in production.
- For OpenAI deployments, prefer `OPENAI_API_KEY` instead of storing `llm.api_key` in a tracked file.
