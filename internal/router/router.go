package router

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"openlight/internal/router/rules"
	"openlight/internal/router/semantic"
	"openlight/internal/skills"
	"openlight/internal/utils"
)

type Mode string

const (
	ModeSlash    Mode = "slash"
	ModeExplicit Mode = "explicit"
	ModeAlias    Mode = "alias"
	ModeRule     Mode = "rule"
	ModeLLM      Mode = "llm"
	ModeUnknown  Mode = "unknown"
)

type Decision struct {
	Mode                  Mode
	SkillName             string
	Args                  map[string]string
	Confidence            float64
	NeedsClarification    bool
	ClarificationQuestion string
}

func (d Decision) Matched() bool {
	return strings.TrimSpace(d.SkillName) != ""
}

func (d Decision) ShouldClarify() bool {
	return d.NeedsClarification && strings.TrimSpace(d.ClarificationQuestion) != ""
}

type Classifier interface {
	Classify(ctx context.Context, text string) (Decision, bool, error)
}

type Router struct {
	registry   *skills.Registry
	classifier Classifier
	logger     *slog.Logger
}

func New(registry *skills.Registry, classifier Classifier) *Router {
	return NewWithLogger(registry, classifier, nil)
}

func NewWithLogger(registry *skills.Registry, classifier Classifier, logger *slog.Logger) *Router {
	return &Router{
		registry:   registry,
		classifier: classifier,
		logger:     logger,
	}
}

func (r *Router) Route(ctx context.Context, text string) (Decision, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Decision{Mode: ModeUnknown}, nil
	}

	sanitizedText := utils.RedactSensitiveText(text)
	sanitizedNormalized := semantic.Normalize(sanitizedText)
	r.logDebug(
		"router pipeline started",
		"text", shortTextForLog(sanitizedText),
		"normalized_text", shortTextForLog(sanitizedNormalized),
		"classifier_enabled", r.classifier != nil,
	)

	if decision, ok := routeSlash(text); ok {
		r.logDebug("router matched slash command", "skill", decision.SkillName, "args", utils.RedactSensitiveArgs(decision.Args))
		return decision, nil
	}

	if decision, ok := routeExplicit(text); ok {
		r.logDebug("router matched explicit command", "skill", decision.SkillName, "args", utils.RedactSensitiveArgs(decision.Args))
		return decision, nil
	}

	if skill, ok := r.registry.ResolveIdentifier(text); ok {
		r.logDebug("router matched registry alias", "skill", skill.Definition().Name)
		return Decision{
			Mode:      ModeAlias,
			SkillName: skill.Definition().Name,
			Args:      map[string]string{},
		}, nil
	}

	if match, ok := rules.Parse(text); ok {
		r.logDebug("router matched semantic rule", "skill", match.SkillName, "args", utils.RedactSensitiveArgs(match.Args), "normalized_text", shortTextForLog(sanitizedNormalized))
		return Decision{
			Mode:      ModeRule,
			SkillName: match.SkillName,
			Args:      match.Args,
		}, nil
	}

	if r.classifier != nil {
		r.logDebug("router invoking llm classifier", "normalized_text", shortTextForLog(sanitizedNormalized))
		decision, ok, err := r.classifier.Classify(ctx, text)
		if err != nil {
			return Decision{}, err
		}
		if ok {
			r.logDebug("router accepted llm classifier decision", "skill", decision.SkillName, "confidence", decision.Confidence, "clarify", decision.ShouldClarify())
			return decision, nil
		}
		r.logDebug("router classifier produced no executable match")
	}

	r.logDebug("router finished with no match", "normalized_text", shortTextForLog(sanitizedNormalized))
	return Decision{Mode: ModeUnknown}, nil
}

const maxLoggedTextChars = 160

func (r *Router) logDebug(msg string, args ...any) {
	if r.logger != nil {
		r.logger.Debug(msg, args...)
	}
}

func shortTextForLog(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxLoggedTextChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxLoggedTextChars])) + "..."
}

func routeSlash(text string) (Decision, bool) {
	if !strings.HasPrefix(text, "/") {
		return Decision{}, false
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Decision{}, false
	}

	command := normalizeRouteCommand(fields[0])
	argsText := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))

	return routeCommand(command, argsText, ModeSlash)
}

func routeExplicit(text string) (Decision, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Decision{}, false
	}

	maxParts := len(fields)
	if maxParts > 3 {
		maxParts = 3
	}

	for parts := maxParts; parts >= 1; parts-- {
		command := normalizeRouteCommand(strings.Join(fields[:parts], " "))
		argsText := strings.TrimSpace(strings.Join(fields[parts:], " "))
		if decision, ok := routeCommand(command, argsText, ModeExplicit); ok {
			return decision, true
		}
	}

	return Decision{}, false
}

func routeCommand(command, argsText string, mode Mode) (Decision, bool) {
	switch command {
	case "start":
		return routeNoArgCommand(mode, "start", argsText)
	case "help":
		return Decision{Mode: mode, SkillName: "help", Args: map[string]string{"topic": argsText}}, true
	case "ping":
		return routeNoArgCommand(mode, "ping", argsText)
	case "skills":
		return Decision{Mode: mode, SkillName: "skills", Args: map[string]string{"topic": argsText}}, true
	case "enable":
		return Decision{Mode: mode, SkillName: "watch_enable", Args: map[string]string{"pack": strings.TrimSpace(argsText)}}, true
	case "status":
		if argsText == "" {
			return Decision{Mode: mode, SkillName: "status", Args: map[string]string{}}, true
		}
		return Decision{Mode: mode, SkillName: "service_status", Args: map[string]string{"service": argsText}}, true
	case "cpu":
		return routeNoArgCommand(mode, "cpu", argsText)
	case "memory", "ram":
		return routeNoArgCommand(mode, "memory", argsText)
	case "disk", "storage":
		return routeNoArgCommand(mode, "disk", argsText)
	case "uptime":
		return routeNoArgCommand(mode, "uptime", argsText)
	case "ip":
		return routeNoArgCommand(mode, "ip", argsText)
	case "hostname", "host":
		return routeNoArgCommand(mode, "hostname", argsText)
	case "temp", "temperature":
		return routeNoArgCommand(mode, "temperature", argsText)
	case "services", "service list":
		return routeNoArgCommand(mode, "service_list", argsText)
	case "service", "service status":
		return Decision{Mode: mode, SkillName: "service_status", Args: map[string]string{"service": argsText}}, true
	case "restart", "service restart":
		return Decision{Mode: mode, SkillName: "service_restart", Args: map[string]string{"service": argsText}}, true
	case "logs", "log", "service logs":
		return Decision{Mode: mode, SkillName: "service_logs", Args: map[string]string{"service": argsText}}, true
	case "watch":
		return parseWatchCommand(mode, argsText)
	case "watch add":
		return Decision{Mode: mode, SkillName: "watch_add", Args: map[string]string{"spec": strings.TrimSpace(argsText)}}, true
	case "watch list":
		return routeNoArgCommand(mode, "watch_list", argsText)
	case "watch pause", "watch toggle":
		return Decision{Mode: mode, SkillName: "watch_pause", Args: map[string]string{"id": strings.TrimSpace(argsText)}}, true
	case "watch remove", "watch delete":
		return Decision{Mode: mode, SkillName: "watch_remove", Args: map[string]string{"id": strings.TrimSpace(argsText)}}, true
	case "watch history", "watch incidents":
		return Decision{Mode: mode, SkillName: "watch_history", Args: map[string]string{"id": strings.TrimSpace(argsText)}}, true
	case "watch test", "watch probe":
		return Decision{Mode: mode, SkillName: "watch_test", Args: map[string]string{"id": strings.TrimSpace(argsText)}}, true
	case "users", "accounts", "user providers", "account providers":
		return routeNoArgCommand(mode, "user_providers", argsText)
	case "user list", "list users":
		return Decision{Mode: mode, SkillName: "user_list", Args: parseUserListArgs(argsText)}, true
	case "user add", "add user", "register user":
		return Decision{Mode: mode, SkillName: "user_add", Args: parseUserAddArgs(argsText)}, true
	case "user delete", "delete user", "remove user", "unregister user":
		return Decision{Mode: mode, SkillName: "user_delete", Args: parseUserDeleteArgs(argsText)}, true
	case "note delete", "delete note", "remove note", "note remove", "note_delete":
		return Decision{Mode: mode, SkillName: "note_delete", Args: map[string]string{"id": argsText}}, true
	case "note", "note add", "add note", "remember":
		return Decision{Mode: mode, SkillName: "note_add", Args: map[string]string{"text": argsText}}, true
	case "notes", "note list", "list notes":
		return routeNoArgCommand(mode, "note_list", argsText)
	case "files", "file list", "list files", "file_list":
		return Decision{Mode: mode, SkillName: "file_list", Args: filePathArgs(argsText)}, true
	case "read", "show", "cat", "file read", "read file", "file_read":
		args, ok := parseFileReadArgs(command, argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: "file_read", Args: args}, true
	case "write", "file write", "write file", "create file", "file_write":
		args, ok := parseFileWriteArgs(argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: "file_write", Args: args}, true
	case "replace", "file replace", "replace file", "file_replace":
		args, ok := parseFileReplaceArgs(argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: "file_replace", Args: args}, true
	case "run":
		skillName, args, ok := parseWorkbenchRunArgs(argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: skillName, Args: args}, true
	case "exec code", "exec_code", "run code":
		args, ok := parseExecCodeArgs(argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: "exec_code", Args: args}, true
	case "exec file", "exec_file", "run file":
		args, ok := parseExecFileArgs(argsText)
		if !ok {
			return Decision{}, false
		}
		return Decision{Mode: mode, SkillName: "exec_file", Args: args}, true
	case "workspace clean", "workspace_clean", "clean workspace":
		return routeNoArgCommand(mode, "workspace_clean", argsText)
	case "chat", "ask":
		return Decision{Mode: mode, SkillName: "chat", Args: map[string]string{"text": argsText}}, true
	default:
		return Decision{}, false
	}
}

func parseWatchCommand(mode Mode, argsText string) (Decision, bool) {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return Decision{Mode: mode, SkillName: "watch_list", Args: map[string]string{}}, true
	}

	switch {
	case argsText == "list":
		return Decision{Mode: mode, SkillName: "watch_list", Args: map[string]string{}}, true
	case strings.HasPrefix(argsText, "add "):
		return Decision{Mode: mode, SkillName: "watch_add", Args: map[string]string{"spec": strings.TrimSpace(strings.TrimPrefix(argsText, "add "))}}, true
	case strings.HasPrefix(argsText, "pause "):
		return Decision{Mode: mode, SkillName: "watch_pause", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "pause "))}}, true
	case strings.HasPrefix(argsText, "toggle "):
		return Decision{Mode: mode, SkillName: "watch_pause", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "toggle "))}}, true
	case strings.HasPrefix(argsText, "remove "):
		return Decision{Mode: mode, SkillName: "watch_remove", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "remove "))}}, true
	case strings.HasPrefix(argsText, "delete "):
		return Decision{Mode: mode, SkillName: "watch_remove", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "delete "))}}, true
	case argsText == "history":
		return Decision{Mode: mode, SkillName: "watch_history", Args: map[string]string{}}, true
	case strings.HasPrefix(argsText, "history "):
		return Decision{Mode: mode, SkillName: "watch_history", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "history "))}}, true
	case strings.HasPrefix(argsText, "incidents "):
		return Decision{Mode: mode, SkillName: "watch_history", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "incidents "))}}, true
	case strings.HasPrefix(argsText, "test "):
		return Decision{Mode: mode, SkillName: "watch_test", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "test "))}}, true
	case strings.HasPrefix(argsText, "probe "):
		return Decision{Mode: mode, SkillName: "watch_test", Args: map[string]string{"id": strings.TrimSpace(strings.TrimPrefix(argsText, "probe "))}}, true
	default:
		return Decision{}, false
	}
}

func routeNoArgCommand(mode Mode, skillName, argsText string) (Decision, bool) {
	if mode != ModeSlash && strings.TrimSpace(argsText) != "" {
		return Decision{}, false
	}

	return Decision{Mode: mode, SkillName: skillName, Args: map[string]string{}}, true
}

func normalizeRouteCommand(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	if idx := strings.Index(value, "@"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func filePathArgs(argsText string) map[string]string {
	path := strings.TrimSpace(argsText)
	if path == "" {
		return map[string]string{}
	}
	return map[string]string{"path": path}
}

func parseFileReadArgs(command, argsText string) (map[string]string, bool) {
	path := strings.TrimSpace(argsText)
	if path == "" {
		return map[string]string{}, command == "file_read" || command == "read file" || command == "file read"
	}
	if !looksLikePath(path) {
		return nil, false
	}
	return map[string]string{"path": path}, true
}

func parseFileWriteArgs(argsText string) (map[string]string, bool) {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return nil, false
	}

	path := argsText
	content := ""
	switch {
	case strings.Contains(argsText, "\n"):
		parts := strings.SplitN(argsText, "\n", 2)
		path = strings.TrimSpace(parts[0])
		content = parts[1]
	case strings.Contains(argsText, "::"):
		parts := strings.SplitN(argsText, "::", 2)
		path = strings.TrimSpace(parts[0])
		content = trimSingleLeadingSpace(parts[1])
	}

	if !looksLikePath(path) {
		return nil, false
	}

	return map[string]string{
		"path":    path,
		"content": content,
	}, true
}

func parseFileReplaceArgs(argsText string) (map[string]string, bool) {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return nil, false
	}

	if strings.Contains(argsText, "::") && strings.Contains(argsText, "=>") {
		parts := strings.SplitN(argsText, "::", 2)
		path := strings.TrimSpace(parts[0])
		replaceParts := strings.SplitN(parts[1], "=>", 2)
		if len(replaceParts) != 2 || !looksLikePath(path) {
			return nil, false
		}
		find := strings.TrimSpace(replaceParts[0])
		replacement := trimSingleLeadingSpace(replaceParts[1])
		if find == "" {
			return nil, false
		}
		return map[string]string{
			"path":    path,
			"find":    find,
			"replace": replacement,
		}, true
	}

	lowered := strings.ToLower(argsText)
	withIdx := strings.Index(lowered, " with ")
	inIdx := strings.LastIndex(lowered, " in ")
	if withIdx <= 0 || inIdx <= withIdx+len(" with ") {
		return nil, false
	}

	find := strings.TrimSpace(argsText[:withIdx])
	replacement := strings.TrimSpace(argsText[withIdx+len(" with ") : inIdx])
	path := strings.TrimSpace(argsText[inIdx+len(" in "):])
	if find == "" || !looksLikePath(path) {
		return nil, false
	}

	return map[string]string{
		"path":    path,
		"find":    find,
		"replace": replacement,
	}, true
}

func parseWorkbenchRunArgs(argsText string) (string, map[string]string, bool) {
	if args, ok := parseExecCodeArgs(argsText); ok {
		return "exec_code", args, true
	}
	if args, ok := parseExecFileArgs(argsText); ok {
		return "exec_file", args, true
	}
	return "", nil, false
}

func parseExecCodeArgs(argsText string) (map[string]string, bool) {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return nil, false
	}

	if strings.Contains(argsText, "::") {
		parts := strings.SplitN(argsText, "::", 2)
		runtime := strings.TrimSpace(parts[0])
		if !looksLikeRuntime(runtime) {
			return nil, false
		}
		return map[string]string{
			"runtime": runtime,
			"code":    trimSingleLeadingSpace(parts[1]),
		}, true
	}

	lines := strings.SplitN(argsText, "\n", 2)
	if len(lines) == 2 {
		header := strings.TrimSpace(lines[0])
		header = strings.TrimSuffix(header, ":")
		if looksLikeRuntime(header) {
			return map[string]string{
				"runtime": header,
				"code":    lines[1],
			}, true
		}
	}

	idx := strings.Index(argsText, ":")
	if idx <= 0 {
		return nil, false
	}

	runtime := strings.TrimSpace(argsText[:idx])
	if !looksLikeRuntime(runtime) {
		return nil, false
	}

	return map[string]string{
		"runtime": runtime,
		"code":    trimSingleLeadingSpace(argsText[idx+1:]),
	}, true
}

func parseExecFileArgs(argsText string) (map[string]string, bool) {
	path := strings.TrimSpace(argsText)
	if !looksLikePath(path) {
		return nil, false
	}
	return map[string]string{"path": path}, true
}

func parseUserAddArgs(argsText string) map[string]string {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return map[string]string{}
	}

	fields := strings.Fields(argsText)
	switch len(fields) {
	case 0:
		return map[string]string{}
	case 1:
		return map[string]string{"username": fields[0]}
	case 2:
		return map[string]string{
			"username": fields[0],
			"password": fields[1],
		}
	default:
		return map[string]string{
			"provider": fields[0],
			"username": fields[1],
			"password": strings.TrimSpace(strings.Join(fields[2:], " ")),
		}
	}
}

func parseUserDeleteArgs(argsText string) map[string]string {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return map[string]string{}
	}

	fields := strings.Fields(argsText)
	switch len(fields) {
	case 0:
		return map[string]string{}
	case 1:
		return map[string]string{"username": fields[0]}
	default:
		return map[string]string{
			"provider": fields[0],
			"username": strings.TrimSpace(strings.Join(fields[1:], " ")),
		}
	}
}

func parseUserListArgs(argsText string) map[string]string {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return map[string]string{}
	}

	fields := strings.Fields(argsText)
	switch len(fields) {
	case 0:
		return map[string]string{}
	case 1:
		return map[string]string{"provider": fields[0]}
	default:
		return map[string]string{
			"provider": fields[0],
			"pattern":  strings.TrimSpace(strings.Join(fields[1:], " ")),
		}
	}
}

func trimSingleLeadingSpace(value string) string {
	if strings.HasPrefix(value, " ") {
		return value[1:]
	}
	return value
}

func looksLikePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	switch {
	case strings.HasPrefix(value, "/"),
		strings.HasPrefix(value, "./"),
		strings.HasPrefix(value, "../"),
		strings.HasPrefix(value, "~"),
		strings.HasPrefix(value, "."):
		return true
	}

	if strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\') {
		return true
	}

	return filepath.Ext(value) != ""
}

func looksLikeRuntime(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "python", "python3", "sh", "shell", "bash", "node", "javascript", "js":
		return true
	default:
		return false
	}
}
