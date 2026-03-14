# Architecture

This document describes how `openLight` is wired today.

## Goals

`openLight` is intentionally built around a narrow set of constraints:

- small enough for Raspberry Pi and other modest Linux hosts
- Telegram-first interface
- deterministic routing before any LLM fallback
- explicit skills instead of general shell autonomy
- easy deployment as one Go binary plus one YAML config

## Runtime Flow

The main runtime path is:

`Telegram transport -> auth -> persistence -> router -> optional LLM route/skill layers -> validation -> skill execution -> persistence -> reply`

In practice:

1. Telegram transport receives a message through polling or webhook mode.
2. The agent stores the incoming message in SQLite.
3. Auth checks user ID and chat ID against allowlists.
4. The router tries deterministic matching first.
5. If needed, the LLM route layer chooses `chat`, `unknown`, or one visible skill group.
6. If a group was selected, the LLM skill layer chooses one concrete skill inside that group and extracts arguments.
7. The Go side validates confidence, required arguments, mutating thresholds, and clarification state.
8. The agent either asks a clarification question or executes the chosen skill.
9. The result and skill-call metadata are stored.
10. The reply is sent back to Telegram and also stored in SQLite.

## Routing Pipeline

Routing is intentionally layered and deterministic-first.

Priority order:

1. slash commands like `/cpu`
2. explicit commands without slash like `note_add hello`
3. exact name and alias resolution from the skill registry
4. semantic rule parsing like `restart tailscale`
5. optional two-stage LLM fallback
6. chat fallback when no executable skill was matched

Relevant files:

- [internal/router/router.go](./internal/router/router.go)
- [internal/router/rules/rules.go](./internal/router/rules/rules.go)
- [internal/router/semantic/normalize.go](./internal/router/semantic/normalize.go)
- [internal/router/llm/classifier.go](./internal/router/llm/classifier.go)

### Deterministic Layers

These layers bypass the LLM entirely:

- slash commands
- explicit commands
- exact registry identifiers
- semantic rules on normalized natural language

The semantic normalizer rewrites common Russian and English variants into a tighter vocabulary before rules and LLM routing see them. Examples:

- `мемори` -> `memory`
- `рам` -> `memory`
- `цпу` -> `cpu`
- `перезапусти` -> `restart`
- `заметку` -> `note`
- `добавь` -> `add`

Examples of deterministic file commands:

- `/files /tmp/openlight`
- `read /tmp/openlight/app.conf`
- `write /tmp/openlight/hello.txt :: hello world`
- `replace 8080 with 8081 in /tmp/openlight/app.conf`

Free-form file requests such as `можешь показать содержимое файла /tmp/openlight/app.conf?` currently rely on the LLM route layer and the LLM skill layer.

### LLM Route Layer

If deterministic routing fails, the first LLM step receives:

- the raw user text
- a visible catalog of registered skill groups
- a small route budget for prompt size and output length

It returns strict JSON with:

- `intent`
- `confidence`
- `needs_clarification`
- `clarification_question`

The route intent is limited to:

- `chat`
- `unknown`
- one visible group key such as `files`, `system`, `services`, `notes`, or `core`

### LLM Skill Layer

If the route layer selected a group, the second LLM step receives:

- the raw user text
- the selected group key
- the visible skills inside that group
- allowed service names only when the selected group is `services`

It returns strict JSON with:

- `skill`
- `arguments`
- `confidence`
- `needs_clarification`
- `clarification_question`

For the `files` group, the main extracted arguments are:

- `path`
- `content`
- `find`
- `replace`

The Go side then validates:

- the chosen skill exists and belongs to the allowed group
- required arguments are present
- confidence passes the correct threshold
- mutating skills use a higher threshold than read-only skills

## Pending Clarification Flow

Clarification is stateful.

When routing returns `needs_clarification=true`, the agent:

- sends the clarification question to the user
- stores the original request and clarification question in SQLite settings

When the next user reply arrives, the agent can merge:

- original request
- clarification question
- follow-up answer

and retry routing on the combined text instead of treating the follow-up as a fresh unrelated request.

This logic lives in [internal/core/agent.go](./internal/core/agent.go).

## Main Components

### Telegram Transport

Telegram transport supports both:

- polling
- webhook

It owns update ingestion and outgoing text replies.

File:

- [internal/telegram/client.go](./internal/telegram/client.go)

### Auth

Auth is a simple allowlist check for:

- allowed user IDs
- allowed chat IDs

File:

- [internal/auth/auth.go](./internal/auth/auth.go)

### Core Agent

The core agent coordinates:

- message persistence
- auth checks
- pending clarification state
- router decisions
- skill execution with timeout
- reply sending
- user-facing error mapping

File:

- [internal/core/agent.go](./internal/core/agent.go)

### Skills

Skills are the unit of executable behavior.

Each skill exposes:

- `Definition()`
- `Execute(...)`

`Definition` contains metadata such as:

- name
- group
- description
- aliases
- usage
- examples
- mutating flag
- hidden flag

Core files:

- [internal/skills/skill.go](./internal/skills/skill.go)
- [internal/skills/groups.go](./internal/skills/groups.go)
- [internal/skills/registry.go](./internal/skills/registry.go)

Current built-in groups:

- `files`
- `system`
- `services`
- `notes`
- `core`
- `chat`
- `other`

### Skill Modules

`main.go` no longer registers built-ins one by one.  
It assembles a list of `skills.Module` values and registers them as bundles.

Core files:

- [internal/skills/module.go](./internal/skills/module.go)
- [internal/skills/core_module.go](./internal/skills/core_module.go)
- [internal/skills/files/module.go](./internal/skills/files/module.go)
- [internal/skills/system/module.go](./internal/skills/system/module.go)
- [internal/skills/services/module.go](./internal/skills/services/module.go)
- [internal/skills/notes/module.go](./internal/skills/notes/module.go)
- [internal/skills/chat/module.go](./internal/skills/chat/module.go)

### Registry

The skill registry stores:

- registered skills by normalized name
- normalized definitions
- identifiers and aliases
- visible groups
- visible skills by group

This registry is the source of truth for:

- exact routing
- `/skills`
- `/help`
- LLM group catalogs
- LLM per-group skill catalogs

### LLM Layer

LLM support is optional.

Providers currently implemented:

- generic HTTP provider
- Ollama provider
- OpenAI Responses API provider

Core files:

- [internal/llm/provider.go](./internal/llm/provider.go)
- [internal/llm/factory.go](./internal/llm/factory.go)
- [internal/llm/prompts.go](./internal/llm/prompts.go)
- [internal/llm/schemas.go](./internal/llm/schemas.go)
- [internal/llm/helpers.go](./internal/llm/helpers.go)
- [internal/llm/ollama.go](./internal/llm/ollama.go)
- [internal/llm/openai.go](./internal/llm/openai.go)

The split is deliberate:

- shared prompts, schemas, and helpers are provider-agnostic
- `ollama.go` is mostly an Ollama transport and decoding adapter
- `openai.go` is mostly an OpenAI transport and structured-output adapter
- `factory.go` wires providers into runtime config

### Provider Factory Registry

Providers are extensible through a factory registry, not through hardcoded switches in `main.go`.

The package-level defaults expose:

- `llm.BuildProvider(...)`
- `llm.RegisterDefaultProviderFactory(...)`
- `llm.MustRegisterDefaultProviderFactory(...)`
- `llm.DefaultProviderNames()`

Built-ins are surfaced through:

- `llm.NewHTTPProviderFactory()`
- `llm.NewOllamaProviderFactory()`
- `llm.NewOpenAIProviderFactory()`

### Storage

SQLite stores:

- `messages`
- `skill_calls`
- `notes`
- `settings`

`settings` is also used for lightweight runtime state such as pending clarification context.

Files:

- [internal/storage/storage.go](./internal/storage/storage.go)
- [internal/storage/sqlite/sqlite.go](./internal/storage/sqlite/sqlite.go)
- [migrations/0001_init.sql](./migrations/0001_init.sql)

## Service Management Model

Service operations are intentionally constrained:

- only explicitly allowed services are exposed
- no arbitrary shell execution path exists in the runtime
- `systemctl` and `journalctl` are called directly
- `tailscale` is normalized to `tailscaled`
- restart can retry with `sudo -n` when permissions require it

Files:

- [internal/skills/services/manager.go](./internal/skills/services/manager.go)
- [internal/skills/services/skills.go](./internal/skills/services/skills.go)

## File Access Model

File operations are intentionally constrained:

- only explicitly allowed roots are exposed
- paths are resolved to absolute filesystem paths
- symlink targets must still stay inside an allowed root
- reads are capped to a configured byte limit
- the runtime exposes specific file skills instead of a generic shell

The built-in file skills are:

- `file_list`
- `file_read`
- `file_write`
- `file_replace`

Their main argument shapes are:

- `file_list`: optional `path`
- `file_read`: required `path`
- `file_write`: `path` plus `content`
- `file_replace`: `path` plus `find` and `replace`

Files:

- [internal/skills/files/manager.go](./internal/skills/files/manager.go)
- [internal/skills/files/skills.go](./internal/skills/files/skills.go)

## Workbench Model

Workbench operations are intentionally constrained:

- `exec_code` writes temporary code only inside one configured workspace directory
- only explicitly allowed runtimes can be used for temporary code execution
- `exec_file` can run only exact allowlisted files
- stdout and stderr are capped to a configured byte limit
- this is still a narrow skill surface, not unrestricted shell access

Files:

- [internal/skills/workbench/manager.go](./internal/skills/workbench/manager.go)
- [internal/skills/workbench/skills.go](./internal/skills/workbench/skills.go)

## Chat Model

The chat skill is tuned for small models and narrow assistant behavior:

- short system prompt
- bounded history size
- history filtering to drop command noise
- trimmed output length
- optional history reset for simple greetings

File:

- [internal/skills/chat/skill.go](./internal/skills/chat/skill.go)

## Process Model

The application starts in [cmd/agent/main.go](./cmd/agent/main.go), where it wires together:

- config
- logger
- SQLite repository
- LLM provider through the provider factory registry
- skill registry through modules
- router
- Telegram transport
- core agent

Graceful shutdown is handled through `context` and OS signals.

## Deployment Model

The repo includes:

- Raspberry Pi build and deploy scripts
- a systemd unit
- local Docker Compose for Ollama

Files:

- [Makefile](./Makefile)
- [scripts/deploy-rpi.sh](./scripts/deploy-rpi.sh)
- [scripts/deploy-rpi-config.sh](./scripts/deploy-rpi-config.sh)
- [scripts/deploy-rpi-service.sh](./scripts/deploy-rpi-service.sh)
- [deployments/systemd/openlight-agent.service](./deployments/systemd/openlight-agent.service)
- [deployments/docker/ollama-compose.yaml](./deployments/docker/ollama-compose.yaml)

## Extension Model

The extension points are intentionally small:

- `skills.Skill` for one executable tool
- `skills.Module` for a bundle of skills
- `llm.Provider` for one LLM transport and response adapter
- `llm.ProviderFactory` for wiring providers into runtime config

### Adding A New Skill

The smallest unit is one `skills.Skill`.

```go
type EchoSkill struct{}

func (EchoSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "echo",
		Group:       skills.GroupOther,
		Description: "Repeat the given text.",
		Aliases:     []string{"repeat"},
		Usage:       "echo <text>",
		Examples:    []string{"echo hello"},
	}
}

func (EchoSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	return skills.Result{Text: input.Args["text"]}, nil
}
```

What matters in practice:

- `Definition.Name` is the stable skill id used by routing and help
- `Definition.Group` controls where the skill appears in `/skills` and in the LLM group catalog
- `Aliases`, `Usage`, and `Examples` improve deterministic routing and help output
- `Mutating` should be `true` for actions like restart, delete, or write
- `Hidden` keeps a skill executable but removes it from discovery and LLM catalogs

If a skill belongs to a brand new family, add a new `skills.Group` constant in [internal/skills/groups.go](./internal/skills/groups.go) with:

- stable `Key` for routing
- human-friendly `Title` for `/skills`
- short `Description` for the route classifier
- `Order` for display sorting

### Adding A New Skill Module

Modules let startup wiring register bundles instead of individual skills.

```go
func NewModule(dep SomeDependency) skills.Module {
	return skills.NewModule("echo", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewEchoSkill(dep),
			NewEchoListSkill(dep),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
```

Then wire it once during startup:

```go
modules := []skills.Module{
	fileskills.NewModule(fileManager),
	systemskills.NewModule(systemProvider),
	serviceskills.NewModule(serviceManager, cfg.Services.LogLines),
	echoskills.NewModule(dep),
	skills.NewCoreModule(),
}

if err := skills.RegisterModules(registry, modules...); err != nil {
	return nil, err
}
```

Reference implementations:

- [internal/skills/files/module.go](./internal/skills/files/module.go)
- [internal/skills/system/module.go](./internal/skills/system/module.go)
- [internal/skills/services/module.go](./internal/skills/services/module.go)
- [internal/skills/notes/module.go](./internal/skills/notes/module.go)
- [internal/skills/chat/module.go](./internal/skills/chat/module.go)

### Adding A New LLM Provider

Providers need to satisfy:

- `ClassifyRoute(...)`
- `ClassifySkill(...)`
- `Summarize(...)`
- `Chat(...)`

The quickest way is to implement `llm.Provider` and register a factory.

```go
func init() {
	llm.MustRegisterDefaultProviderFactory(
		llm.NewProviderFactory("dummy", func(cfg llm.ProviderConfig, logger *slog.Logger) (llm.Provider, error) {
			return NewDummyProvider(cfg.Endpoint, cfg.Model, cfg.Timeout, logger), nil
		}),
	)
}
```

After that the runtime can use:

```yaml
llm:
  enabled: true
  provider: "dummy"
```

If you want a local factory registry instead of mutating the package default, build one explicitly:

```go
registry := llm.NewFactoryRegistry(
	llm.NewOllamaProviderFactory(),
	llm.NewOpenAIProviderFactory(),
	llm.NewProviderFactory("dummy", buildDummyProvider),
)

provider, err := registry.Build("dummy", cfg, logger)
```

Reference implementations:

- [internal/llm/factory.go](./internal/llm/factory.go)
- [internal/llm/provider.go](./internal/llm/provider.go)
- [internal/llm/ollama.go](./internal/llm/ollama.go)
- [internal/llm/openai.go](./internal/llm/openai.go)
