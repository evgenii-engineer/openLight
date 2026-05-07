# Regression Matrix

openLight uses a risk-based regression matrix. The goal is **not** to test
every feature in every possible combination — that would be prohibitively slow
and brittle as the agent grows. The goal is to protect the most important user
flows, the safety boundaries (auth, allowlists, LLM hallucinations), and the
integration seams that historically break first.

## Levels

| Level | When to run | Command | Notes |
|---|---|---|---|
| **P0** | Every commit | `make test` and `make smoke-cli` | Fast, deterministic, no real Telegram / Ollama / Docker / systemd / launchd / network. |
| **P1** | Before release or large feature merge | `make regression` | P0 plus extended deterministic checks. |
| **P2** | On a real host, opt-in | `make smoke-macmini SSH_HOST=…` or `make smoke-rpi PI_HOST=…` | Real host dependencies, service manager, Telegram, Ollama/browser/voice. |
| **Manual** | After deployment | 5-minute Telegram sanity check | `/start`, `/skills`, `/status`, service logs, browser check, voice if enabled. |

CI runs `make test`. CI does **not** run P2 — those targets SSH into a real
host and are a deployment-time check, not a commit-time check.

## Status legend

| Status | Meaning |
|---|---|
| `automated` | A real automated test exercises this scenario. |
| `partial` | Some coverage exists but does not cover every branch or every kind input. |
| `planned` | No automated test yet. The line is documented so it is not forgotten. |
| `manual` | Verified by a human on a real machine; no automated equivalent. |

`Test` column points to the file (and ideally the function) that owns the
scenario. When in doubt about a `partial` row, prefer adding more coverage
over relabeling it `automated`.

---

## P0 — every commit

### Core / Auth

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Core/Auth | `/start` | Returns welcome / help entrypoint | automated | `internal/skills/registry_test.go` (start skill registered) + `internal/router/router_test.go:TestRouterSlashCommand` |
| Core/Auth | `/skills` lists groups | Shows available skill groups | automated | `internal/skills/meta_test.go:TestSkillsSkillGroupsOutput` |
| Core/Auth | `skills <group>` expands a group | Shows usage for selected skills | automated | `internal/skills/meta_test.go:TestSkillsSkillExpandsGroup` |
| Core/Auth | `help <skill>` | Shows usage for one skill | automated | `internal/skills/meta_test.go:TestSkillsSkillCanShowSingleSkill` |
| Core/Auth | Unauthorized user/chat | Request is rejected | automated | `internal/auth/auth_test.go`, `internal/auth/boundaries_test.go` |
| Core/Auth | Only-users allowlist permits any chat | Allowed user passes regardless of chat | automated | `internal/auth/boundaries_test.go:TestAuthorizerOnlyUsersAllowlistIgnoresChat` |
| Core/Auth | Only-chats allowlist permits any user inside chat | Allowed chat passes regardless of user | automated | `internal/auth/boundaries_test.go:TestAuthorizerOnlyChatsAllowlistIgnoresUser` |
| Core/Auth | Empty input | Safe no-op or unknown decision | automated | `internal/router/safety_test.go:TestRouterEmptyInputIsUnknown` |
| Core/Auth | Unknown slash command | Friendly fallback (unknown), no panic | automated | `internal/router/safety_test.go:TestRouterUnknownSlashFallsThroughToUnknown` |
| Core/Auth | Skill execution failure surfaces | Friendly error, no crash | automated | `internal/core/agent_integration_test.go` (multiple) |

### Router

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Router | Slash command | Routes to expected skill | automated | `internal/router/router_test.go:TestRouterSlashCommand` |
| Router | Plain-text command | Routes to expected skill | automated | `internal/router/router_test.go:TestRouterExplicitTwoWordCommand` |
| Router | Alias | Routes to expected skill | automated | `internal/router/router_test.go:TestRouterAliasMatch` |
| Router | Russian semantic command | Routes to expected skill | automated | `internal/router/router_test.go:TestRouterRuleBasedRussian*` |
| Router | Unknown slash command | Safe fallback to unknown | automated | `internal/router/safety_test.go:TestRouterUnknownSlashFallsThroughToUnknown` |
| Router | Slash beats classifier | Slash always wins over LLM classifier | automated | `internal/router/safety_test.go:TestRouterDoesNotInvokeClassifierOnSlashMatch` |
| Router | LLM disabled | No classifier path is invoked | automated | `internal/router/router_test.go` (uses `router.New(reg, nil)`) |
| Router | Classifier error propagates | Error surfaces to caller | automated | `internal/router/safety_test.go:TestRouterPropagatesClassifierError` |

### Skill registry

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Skill Registry | Register skill | Skill appears in registry | automated | `internal/skills/registry_test.go` |
| Skill Registry | Duplicate skill / alias | Duplicate registration is rejected | automated | `internal/skills/registry_test.go:TestRegistryRejectsDuplicateAlias` |
| Skill Registry | Default registry includes core skills | `/start`, `/help`, `/skills`, `/ping`, `/status` always present | automated | `internal/app/registry_contract_test.go:TestBuildRegistryIncludesCoreSkills` |
| Skill Registry | Every visible skill has a description | `/skills` is never empty | automated | `internal/app/registry_contract_test.go:TestBuildRegistryDefaultSkillContract` |
| Skill Registry | No empty groups | Listed groups always have a visible skill | automated | `internal/app/registry_contract_test.go:TestBuildRegistryGroupsAreNonEmpty` |

### Config

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Config | Valid minimal config | Loads/validates | automated | `internal/config/config_test.go:TestLoadAppliesNestedDefaults` |
| Config | Missing telegram bot_token | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Missing both auth allowlists | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Invalid telegram mode | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Webhook mode without URL | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Webhook URL not HTTPS | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Missing storage sqlite_path | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | LLM execute_threshold > 1 | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | LLM clarify_threshold ≥ execute_threshold | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | files.max_read_bytes ≤ 0 | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Browser enabled without domains and without allow_all_domains | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Browser enabled with allow_all_domains | Validates | automated | `internal/config/validation_negative_test.go:TestLoadValidationAllowsBrowserAllowAllDomains` |
| Config | Voice enabled without model_path | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Remote host without auth method | Fails validation | automated | `internal/config/validation_negative_test.go` |
| Config | Remote host without known_hosts_path | Fails unless `insecure_ignore_host_key` | automated | `internal/config/config_test.go:TestLoadRejectsRemoteHostWithoutHostKeyPolicy` |
| Config | Vision/OCR provider blank | Defaulted (not a validation failure) | automated | covered implicitly by `TestLoadAppliesNestedDefaults` |
| Config | LLM profile selection | Direct fields stay coherent with profile | automated | `internal/config/config_test.go:TestLoadAppliesLLMProfileFromConfig` |

### Files

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Files | List allowed root | Returns entries | automated | `internal/skills/files/skills_test.go:TestListSkillShowsAllowedRoots` |
| Files | Read allowed file | Returns content | automated | `internal/skills/files/skills_test.go:TestReadSkillReadsWhitelistedFile` |
| Files | Path outside allowed roots | ErrAccessDenied | automated | `internal/skills/files/skills_test.go:TestLocalManagerRejectsPathOutsideAllowedRoots` |
| Files | Parent-traversal `..` | ErrAccessDenied | automated | `internal/skills/files/limits_test.go:TestLocalManagerRejectsParentTraversal` |
| Files | Write denied when allow_write=false | ErrAccessDenied | automated | `internal/skills/files/skills_test.go:TestWriteSkillRespectsAllowWrite` |
| Files | Write allowed when allow_write=true | Creates file | automated | `internal/skills/files/skills_test.go:TestWriteSkillCreatesFileWhenEnabled` |
| Files | Sensitive file (.env) | ErrAccessDenied unless explicitly allowed | automated | `internal/skills/files/skills_test.go:TestLocalManagerBlocksSensitiveFiles` |
| Files | Secret-looking content | Redacted | automated | `internal/skills/files/skills_test.go:TestReadSkillRedactsSecrets` |
| Files | max_read_bytes enforced | Content truncated, flag set | automated | `internal/skills/files/limits_test.go:TestLocalManagerEnforcesMaxReadBytes` |
| Files | list_limit enforced | Entries truncated, flag set | automated | `internal/skills/files/limits_test.go:TestLocalManagerEnforcesListLimit` |
| Files | Disabled manager | Returns error, never panics | automated | `internal/skills/files/limits_test.go:TestLocalManagerDisabledReturnsAccessDenied` |

### Services

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Services | Simple systemd allowlist parsed | Service is allowed | automated | `internal/skills/services/parse_test.go:TestParseAllowedServicesContract` |
| Services | Compose service spec parsed | Spec parsed safely | automated | `internal/skills/services/parse_test.go:TestParseAllowedServicesContract` |
| Services | Docker container spec parsed | Spec parsed safely | automated | `internal/skills/services/parse_test.go:TestParseAllowedServicesContract` |
| Services | Empty alias / empty spec rejected | Parser returns error | automated | `internal/skills/services/parse_test.go:TestParseAllowedServicesContract` |
| Services | Duplicate service names | Parser returns error | automated | `internal/skills/services/parse_test.go:TestParseAllowedServicesContract` |
| Services | Status of allowlisted service | Returns formatted info | automated | `internal/skills/services/skills_test.go:TestStatusSkillFormatsResponse` |
| Services | Logs of allowlisted service | Returns formatted info | automated | `internal/skills/services/skills_test.go:TestLogsSkillUsesSingleWhitelistedServiceWhenArgumentMissing` |
| Services | Restart of allowlisted service | Calls fake restart | automated | `internal/skills/services/skills_test.go:TestRestartSkillRequiresService` |
| Services | Non-allowlisted service | Rejected | automated | `internal/skills/services/skills_test.go:TestSystemdManagerRejectsNonWhitelistedService` |
| Services | Logs truncated to max_log_chars | Truncation marker appended | automated | `internal/skills/services/parse_test.go:TestLimitLogOutputBoundedByMaxChars` |
| Services | Logs unbounded when max=0 | Pass-through | automated | `internal/skills/services/parse_test.go:TestLimitLogOutputUnboundedWhenMaxIsZero` |
| Services | Remote host without registered host | Rejected | automated | `internal/skills/services/skills_test.go:TestNewManagerRejectsUnknownRemoteHost` |

### Watch

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Watch | Parse service watch | Spec parsed | automated | `internal/watch/service_test.go:TestParseAddSpecServiceAcceptsLLMStyleWatchRule` |
| Watch | Parse CPU/disk metric watch | Spec parsed | automated | `internal/watch/service_test.go:TestParseAddSpecMetric` |
| Watch | Add watch via skill | Watch is stored, list shows it | automated | `internal/skills/watch/skills_test.go:TestWatchAddSkillCreatesWatchAndListsIt` |
| Watch | Reject empty watch spec | Friendly error | automated | `internal/skills/watch/skills_test.go:TestWatchAddSkillRejectsEmptySpec` |
| Watch | List watches when none configured | Friendly empty message | automated | `internal/skills/watch/skills_test.go:TestWatchListSkillEmpty` |
| Watch | Service-down trigger creates incident with action buttons | Buttons rendered, incident created | automated | `internal/watch/service_test.go:TestServiceRunCycleAskAndRestartAction` |
| Watch | Auto reaction restarts service | Action succeeds, incident closed | automated | `internal/watch/service_test.go:TestServiceRunCycleAutoRestartsService` |
| Watch | Action callback (restart/logs/status/ignore) | Routed to expected handler | automated | `internal/watch/service_test.go:TestParseActionRequestCallback` |
| Watch | Action expired callback | "no longer pending" reply | automated | `internal/watch/service_test.go:TestServiceHandleActionExpiredCallback` |
| Watch | Recovery resolves incident | "Resolved #" message | automated | `internal/watch/service_test.go:TestServiceRunCycleAskAndRestartAction` |
| Watch | Cooldown suppresses duplicates | Second flap inside cooldown produces no new incident | automated | `internal/watch/cooldown_test.go:TestServiceCooldownSuppressesDuplicateIncidents` |
| Watch | Enable docker pack | Creates ask watch | automated | `internal/watch/service_test.go:TestEnableDockerPackCreatesAskWatch` |
| Watch | Enable auto-heal pack updates existing watch | Reaction flips to auto | automated | `internal/watch/service_test.go:TestEnableAutoHealPackUpdatesExistingServiceWatch` |

### Storage

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Storage | New sqlite database applies migrations | Open succeeds | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Migrations are idempotent on re-open | Re-open succeeds | automated | `internal/storage/sqlite/migrations_test.go:TestRepositoryMigrationsAreIdempotent` |
| Storage | In-memory `:memory:` path | Migrations apply | automated | `internal/storage/sqlite/migrations_test.go:TestRepositoryAcceptsInMemoryPath` |
| Storage | Messages save/list | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Skill calls save | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Notes add/list/delete | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Memories add/list/search/delete | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Settings set/get/delete | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |
| Storage | Watches & incidents save/list/get-open | CRUD works | automated | `internal/storage/sqlite/sqlite_test.go:TestRepositoryCRUD` |

### LLM classifier safety (with fake provider)

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| LLM | Valid route classification | Selects expected group | automated | `internal/router/llm/classifier_test.go:TestClassifierRoutesHighConfidenceIntent` |
| LLM | Valid skill classification | Selects expected skill | automated | `internal/router/llm/classifier_test.go:TestClassifierRoutesWatchAddWithSpec` |
| LLM | Low confidence | Asks clarification or no-match | automated | `internal/router/llm/classifier_test.go:TestClassifierFallsBackOnLowConfidenceUnknown` |
| LLM | Provider error at route stage | Error propagates | automated | `internal/router/llm/safety_test.go:TestClassifierPropagatesRouteError` |
| LLM | Provider error at skill stage | Error propagates | automated | `internal/router/llm/safety_test.go:TestClassifierPropagatesSkillError` |
| LLM | Hallucinated skill name | Rejected (does not route to it) | automated | `internal/router/llm/safety_test.go:TestClassifierRejectsUnknownSkillReturnedByLLM` |
| LLM | Service argument not in allowlist | Rejected by skill boundary | automated | `internal/skills/services/skills_test.go:TestSystemdManagerRejectsNonWhitelistedService` |
| LLM | Mutating skill clarification preserved | Confirmation always required | automated | `internal/router/llm/classifier_test.go:TestClassifierPreservesClarificationForMutatingSkill` |

---

## P1 — before release / large feature merge

These are deterministic and run by `make regression` (which itself depends on
`make test` + `make smoke-cli`).

### Browser

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Browser | Allowed domain | Request runs (with fake runner) | automated | `internal/skills/browser/manager_test.go:TestLocalManagerRunsTitleRequest` |
| Browser | Disallowed domain | Rejected | automated | `internal/skills/browser/manager_test.go:TestLocalManagerRejectsDisallowedDomain` |
| Browser | Host without dot | Rejected | automated | `internal/skills/browser/manager_test.go:TestLocalManagerRejectsHostWithoutDot` |
| Browser | Private network when disabled | Rejected | automated | `internal/skills/browser/manager_test.go:TestLocalManagerRejectsPrivateNetworkWhenDisabled` |
| Browser | allow_all_domains still blocks private | Rejected | automated | `internal/skills/browser/manager_test.go:TestLocalManagerAllowAllDomainsStillBlocksPrivateNetwork` |
| Browser | Screenshot path is built | Path returned | automated | `internal/skills/browser/manager_test.go:TestLocalManagerBuildsScreenshotPath` |

### Memory / Notes

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Memory | Add memory | Stored, returned in list | automated | `internal/skills/memory/skills_test.go:TestRememberSkillStoresMemory` |
| Memory | Disabled memory | Friendly response | automated | `internal/skills/memory/skills_test.go:TestRememberSkillReturnsDisabledMessage` |
| Memory | Search memories | Hits visible in list | automated | `internal/skills/memory/skills_test.go:TestListSkillSearchesMemories` |
| Memory | Forget by text | Deletes the right entry | automated | `internal/skills/memory/skills_test.go:TestForgetSkillDeletesByText` |
| Notes | Add/list/delete via skill | CRUD works | automated | `internal/skills/notes/skills_test.go` (existing) |

### Workbench

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Workbench | Allowed runtime runs | Output captured | automated | `internal/skills/workbench/skills_test.go:TestExecCodeSkillRunsAllowedRuntime` |
| Workbench | Exit code propagates | Reported to caller | automated | `internal/skills/workbench/skills_test.go:TestExecCodeSkillReturnsExitCodeAndOutput` |
| Workbench | Allowed exec file | Runs | automated | `internal/skills/workbench/skills_test.go:TestExecFileSkillRunsAllowedFile` |
| Workbench | Exec file outside allowlist | Rejected | automated | `internal/skills/workbench/skills_test.go:TestLocalManagerRejectsUnlistedExecFile` |
| Workbench | Workspace clean | Removes temp files | automated | `internal/skills/workbench/skills_test.go:TestWorkspaceCleanSkillRemovesTemporaryFiles` |

### OCR / Vision

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| OCR | Disabled returns friendly error | No crash | automated | `internal/skills/ocr/manager_test.go:TestManagerExtractRejectsWhenDisabled` |
| OCR | Missing file rejected | Error | automated | `internal/skills/ocr/manager_test.go:TestManagerExtractRejectsMissingFile` |
| OCR | Provider runs | Result returned | automated | `internal/skills/ocr/manager_test.go:TestManagerExtractRunsProvider` |
| Vision | Disabled returns friendly error | No crash | automated | `internal/skills/vision/manager_test.go:TestManagerAnalyzeRejectsWhenDisabled` |
| Vision | Unsupported extension | Rejected | automated | `internal/skills/vision/manager_test.go:TestManagerAnalyzeRejectsUnsupportedExt` |
| Vision | Default prompt used | Forwarded to provider | automated | `internal/skills/vision/manager_test.go:TestManagerAnalyzeUsesDefaultPrompt` |
| Vision | Compare two images | Diff produced | automated | `internal/skills/vision/manager_test.go:TestManagerCompareRunsDiff` |

### Visual watch

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Visual Watch | Add with parsed options | Spec stored | automated | `internal/skills/visualwatch/skills_test.go:TestAddSkillParsesOptions` |
| Visual Watch | Keywords mode without keywords | Rejected | automated | `internal/skills/visualwatch/skills_test.go:TestAddSkillRejectsKeywordsModeWithoutKeywords` |
| Visual Watch | Initialise baseline | First run sets baseline | automated | `internal/visualwatch/service_test.go:TestEvaluateInitializesBaseline` |
| Visual Watch | Notify on visual change | Alert sent | automated | `internal/visualwatch/service_test.go:TestEvaluateNotifiesOnVisualChange` |
| Visual Watch | Notify on keyword match | Alert sent | automated | `internal/visualwatch/service_test.go:TestEvaluateNotifiesOnKeywordMatch` |

### CLI smoke

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| CLI | `cli -exec "skills"` | Lists groups | automated | `make smoke-cli` |
| CLI | `cli -exec "watch list"` | "No watches configured." | automated | `make smoke-cli` |
| CLI | `cli -exec "notes"` | "No notes saved yet." | automated | `make smoke-cli` |
| CLI | `cli -smoke` builds the smoke report | Report renders correctly | automated | `internal/cli/smoke_test.go` |
| CLI | LLM-fallback smoke check uses classifier decision | Routed to expected skill | automated | `internal/cli/smoke_test.go:TestAddLLMFallbackExecCheckUsesClassifierDecision` |

---

## P2 — real-host smoke (opt-in)

These targets SSH to a real machine and exercise the deployed binary. They
are **not** run by CI or by `make regression`.

### Mac mini (`make smoke-macmini SSH_HOST=…`)

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Mac mini | launchd service running | `launchctl list dev.openlight.agent` returns 0 | manual | `make status-macmini` |
| Mac mini | sqlite writable, configured roots exist | `cli -smoke` passes baseline checks | automated | `make smoke-macmini-cli` (existing) |
| Mac mini | Telegram polling reachable | `cli -smoke` polls Telegram metadata | automated | `make smoke-macmini-cli` |
| Mac mini | Browser helper installed | `cli -smoke` browser check passes | partial | `make smoke-macmini-cli` (when browser enabled in remote config) |
| Mac mini | Ollama reachable + model installed | `cli -smoke` LLM fallback checks pass | automated | `make smoke-macmini-cli-ollama` |
| Mac mini | Voice deps present (ffmpeg, whisper-cli) | `which` checks pass | planned | — |
| Mac mini | tesseract present (OCR) | `which tesseract` passes | planned | — |

### Raspberry Pi (`make smoke-rpi PI_HOST=…`)

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Raspberry Pi | systemd service running | `systemctl status openlight-agent` returns 0 | manual | `make smoke-rpi-cli` |
| Raspberry Pi | sqlite writable, configured roots exist | `cli -smoke` baseline passes | automated | `make smoke-rpi-cli` |
| Raspberry Pi | Telegram polling reachable | `cli -smoke` polls Telegram metadata | automated | `make smoke-rpi-cli` |
| Raspberry Pi | Ollama reachable + model installed | `cli -smoke` LLM fallback passes | automated | `make smoke-rpi-cli-ollama` |
| Raspberry Pi | Allowlisted services have correct units | `service status` for each works | partial | `make smoke-rpi-cli` (only the configured set) |

---

## Manual sanity (5 minutes after deployment)

Not a substitute for any of the above; confidence-only.

| Area | Scenario | Expected result | Status | Test |
|---|---|---|---|---|
| Manual | `/start` in Telegram | Welcome shown, enable buttons rendered | manual | — |
| Manual | `/skills` | Group list shown | manual | — |
| Manual | `/status` | System status shown | manual | — |
| Manual | `service status <one>` | Real service status | manual | — |
| Manual | `logs <one>` | Last few log lines | manual | — |
| Manual | One inline button callback | Button reacts | manual | — |
| Manual | `/browser check <safe URL>` (if browser enabled) | OK / failure summary | manual | — |
| Manual | Voice "status" reply (if voice enabled) | Transcribed and routed | manual | — |

---

## Adding new tests

When adding a new feature:

1. Decide the level (P0 if it can break a core flow, P1 if it gates a release,
   P2 if it depends on a real host).
2. Add the test under the package the feature lives in. Use
   `internal/testkit` helpers for sqlite/config.
3. Add a row to this matrix. If you cannot automate it yet, add a `planned`
   row so it is not forgotten.
4. Update `make smoke-cli` only for cheap, deterministic, fast checks (no
   network, no real services). Anything slower belongs in `make regression`
   or stays in `go test`.

The matrix is the source of truth for what we promise to keep working. If a
row says `automated` but the test does not exist, that is a bug in the
matrix. Fix the matrix or write the test — do not silently downgrade the
status.
