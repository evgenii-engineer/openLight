package llm

import (
	"path/filepath"
	"regexp"
	"strings"

	"openlight/internal/router"
	"openlight/internal/router/semantic"
)

type heuristicSkillMatch struct {
	skillName string
	args      map[string]string
	score     int
}

var (
	watchIDPattern = regexp.MustCompile(`(?i)\bwatch\s+#?(\d+)\b`)
	noteIDPattern  = regexp.MustCompile(`(?i)\bnote\s+#?(\d+)\b`)
)

func (c *Classifier) heuristicSkillDecision(text string, allowedSkills []string, confidence float64) (router.Decision, int, bool) {
	match, ok := c.bestHeuristicSkillMatch(text, allowedSkills)
	if !ok {
		return router.Decision{}, 0, false
	}

	decision := router.Decision{
		Mode:       router.ModeLLM,
		SkillName:  match.skillName,
		Args:       c.routeArguments(text, match.skillName, match.args),
		Confidence: maxConfidence(confidence, c.executeThreshold),
	}
	if question := c.requiredArgumentQuestion(decision.SkillName, decision.Args); question != "" {
		return router.Decision{}, 0, false
	}
	return decision, match.score, true
}

func (c *Classifier) bestHeuristicSkillMatch(text string, allowedSkills []string) (heuristicSkillMatch, bool) {
	allowed := c.heuristicAllowedSkillSet(allowedSkills)
	if len(allowed) == 0 {
		return heuristicSkillMatch{}, false
	}

	lowered := strings.ToLower(strings.TrimSpace(text))
	normalized := semantic.Normalize(text)
	service := c.extractAllowedServiceFromText(text)
	filePath := firstPathLikeToken(text)
	noteText := extractNoteTextFromText(text)
	noteID := extractNumericID(text, noteIDPattern)
	watchID := extractNumericID(text, watchIDPattern)
	watchSpec := extractWatchSpecFromText(text)
	runtime := extractRuntimeFromText(text)
	code := extractCodeSnippet(text)
	provider := extractAccountProvider(text)
	username := extractAccountUsername(text)
	password := extractAccountPassword(text)
	pattern := extractAccountPattern(text)
	find, replace := extractFileReplaceValues(text)
	fileContent := extractFileWriteContent(text)

	matches := make(map[string]heuristicSkillMatch)
	add := func(skillName string, score int, args map[string]string) {
		if score <= 0 {
			return
		}
		if _, ok := allowed[skillName]; !ok {
			return
		}
		current, exists := matches[skillName]
		if exists && current.score >= score {
			return
		}
		matches[skillName] = heuristicSkillMatch{
			skillName: skillName,
			args:      args,
			score:     score,
		}
	}

	if containsAnyFold(lowered, "getting started", "start message", "onboarding", "welcome introduction", "welcome message") {
		add("start", 5, nil)
	}
	if containsAnyFold(lowered, "connectivity check", "alive check", "ping check") {
		add("ping", 5, nil)
	}
	if containsAnyFold(lowered, "skill groups", "tool groups", "built-in skill groups", "available skill groups") {
		add("skills", 5, nil)
	}
	if containsAnyFold(lowered, "general help", "default help", "help text", "help message") {
		add("help", 5, nil)
	}
	if !containsAnyFold(lowered, "service ") && service == "" && containsAnyFold(lowered, "health snapshot", "host health", "system status", "overall status") {
		add("status", 5, nil)
	}
	if containsAnyFold(lowered, "cpu", "processor") {
		add("cpu", 5, nil)
	}
	if containsAnyFold(lowered, "memory", "ram") {
		add("memory", 5, nil)
	}
	if containsAnyFold(lowered, "storage space", "root filesystem", "disk usage") || (strings.Contains(normalized, "disk") && !containsAnyFold(lowered, "service ")) {
		add("disk", 5, nil)
	}
	if containsAnyFold(lowered, "how long has this machine been running", "system uptime", "uptime") {
		add("uptime", 5, nil)
	}
	if containsAnyFold(lowered, "hostname") {
		add("hostname", 5, nil)
	}
	if containsAnyFold(lowered, "ip addresses", "ip address") {
		add("ip", 5, nil)
	}
	if containsAnyFold(lowered, "how hot", "device temperature", "temperature") {
		add("temperature", 5, nil)
	}
	if containsAnyFold(lowered, "allowed services", "services can you manage") {
		add("service_list", 5, nil)
	}
	if service != "" {
		if containsAnyFold(lowered, "restart", "bounce") {
			add("service_restart", 6, map[string]string{"service": service})
		}
		if containsAnyFold(lowered, "logs", "log lines") {
			add("service_logs", 6, map[string]string{"service": service})
		}
		if containsAnyFold(lowered, "is running", "status update", "check whether", "service status") {
			add("service_status", 6, map[string]string{"service": service})
		}
	}
	if noteText != "" && containsAnyFold(lowered, "save a note", "remember this note", "note with this exact text") {
		add("note_add", 6, map[string]string{"text": noteText})
	}
	if containsAnyFold(lowered, "saved notes", "notes are currently saved", "list my saved notes") {
		add("note_list", 5, nil)
	}
	if noteID != "" && containsAnyFold(lowered, "delete note", "remove note") {
		add("note_delete", 6, map[string]string{"id": noteID})
	}
	if watchSpec != "" && containsAnyFold(lowered, "create a watch", "set up monitoring", "watch using this exact rule") {
		add("watch_add", 6, map[string]string{"spec": watchSpec})
	}
	if containsAnyFold(lowered, "watch rules are configured", "configured watches", "list configured watches") {
		add("watch_list", 5, nil)
	}
	if watchID != "" && containsAnyFold(lowered, "probe watch", "test watch") {
		add("watch_test", 6, map[string]string{"id": watchID})
	}
	if watchID != "" && containsAnyFold(lowered, "recent incidents for watch", "watch history", "inspect recent incidents") {
		add("watch_history", 6, map[string]string{"id": watchID})
	}
	if watchID != "" && containsAnyFold(lowered, "pause watch") {
		add("watch_pause", 6, map[string]string{"id": watchID})
	}
	if watchID != "" && containsAnyFold(lowered, "remove watch") {
		add("watch_remove", 6, map[string]string{"id": watchID})
	}
	if filePath != "" && fileContent != "" && containsAnyFold(lowered, "create a text file", "file write", "write ") {
		add("file_write", 6, map[string]string{"path": filePath, "content": fileContent})
	}
	if filePath != "" && containsAnyFold(lowered, "what files are in", "list files") {
		add("file_list", 5, map[string]string{"path": filePath})
	}
	if filePath != "" && find != "" && containsAnyFold(lowered, "replace ") {
		add("file_replace", 6, map[string]string{"path": filePath, "find": find, "replace": replace})
	}
	if filePath != "" && (containsAnyFold(lowered, "read file", "could you read", "what it contains") || hasOpenFileIntent(lowered)) {
		add("file_read", 6, map[string]string{"path": filePath})
	}
	if runtime != "" && code != "" && containsAnyFold(lowered, "execute this", "run code", "runtime ") {
		add("exec_code", 6, map[string]string{"runtime": runtime, "code": code})
	}
	if filePath != "" && containsAnyFold(lowered, "allowed file", "run the allowed file", "run file") {
		add("exec_file", 6, map[string]string{"path": filePath})
	}
	if containsAnyFold(lowered, "clean the workbench workspace", "clear the temporary workbench workspace", "clean workspace") {
		add("workspace_clean", 5, nil)
	}
	if containsAnyFold(lowered, "account providers", "configured account providers") {
		add("user_providers", 5, nil)
	}
	if username != "" && password != "" && provider != "" && containsAnyFold(lowered, "create a user", "add user") {
		add("user_add", 6, map[string]string{"provider": provider, "username": username, "password": password})
	}
	if provider != "" && containsAnyFold(lowered, "list users", "show me users") {
		args := map[string]string{"provider": provider}
		if pattern != "" {
			args["pattern"] = pattern
		}
		add("user_list", 6, args)
	}
	if username != "" && provider != "" && containsAnyFold(lowered, "delete user", "remove account") {
		add("user_delete", 6, map[string]string{"provider": provider, "username": username})
	}

	bestScore := 0
	bestSkill := ""
	best := heuristicSkillMatch{}
	tie := false
	for _, match := range matches {
		switch {
		case match.score > bestScore:
			bestScore = match.score
			bestSkill = match.skillName
			best = match
			tie = false
		case match.score == bestScore && match.skillName != bestSkill:
			tie = true
		}
	}
	if bestScore == 0 || tie {
		return heuristicSkillMatch{}, false
	}
	return best, true
}

func (c *Classifier) heuristicAllowedSkillSet(allowedSkills []string) map[string]struct{} {
	result := make(map[string]struct{})
	if len(allowedSkills) > 0 {
		for _, skillName := range allowedSkills {
			skillName = strings.TrimSpace(skillName)
			if skillName == "" {
				continue
			}
			result[skillName] = struct{}{}
		}
		return result
	}

	for name := range c.skillCatalog {
		result[name] = struct{}{}
	}
	return result
}

func (c *Classifier) shouldUseRouteRescue(decision router.Decision, ok bool, heuristic router.Decision) bool {
	if heuristic.SkillName == "" {
		return false
	}
	if decision.ShouldClarify() && c.shouldPreserveClarification(heuristic.SkillName) {
		return false
	}
	if !ok || !decision.Matched() || decision.ShouldClarify() {
		return true
	}
	return decision.SkillName == "chat" && heuristic.SkillName != "chat"
}

func (c *Classifier) shouldUseRouteGroupRescue(groupKey string, heuristic router.Decision, score int) bool {
	if heuristic.SkillName == "" || score < 5 {
		return false
	}
	return c.skillGroupKey(heuristic.SkillName) != strings.TrimSpace(groupKey)
}

func (c *Classifier) shouldUseSkillRescue(decision router.Decision, ok bool, heuristic router.Decision, score int) bool {
	if heuristic.SkillName == "" {
		return false
	}
	if decision.ShouldClarify() && c.shouldPreserveClarification(heuristic.SkillName) {
		return false
	}
	if !ok || !decision.Matched() || decision.ShouldClarify() {
		return true
	}
	if decision.SkillName == heuristic.SkillName {
		return shouldPreferHeuristicArgs(decision.SkillName, decision.Args, heuristic.Args)
	}
	if score < 5 {
		return false
	}
	switch decision.SkillName {
	case "chat", "help", "skills", "status", "service_list", "watch_list", "note_list", "user_providers":
		return true
	}
	if len(heuristic.Args) > len(decision.Args) {
		return true
	}
	return false
}

func (c *Classifier) shouldPreserveClarification(skillName string) bool {
	switch skillName {
	case "service_restart":
		return true
	default:
		return false
	}
}

func (c *Classifier) skillGroupKey(skillName string) string {
	definition, ok := c.skillCatalog[skillName]
	if !ok {
		return ""
	}
	return strings.TrimSpace(definition.Group.Key)
}

func shouldPreferHeuristicArgs(skillName string, currentArgs map[string]string, heuristicArgs map[string]string) bool {
	switch skillName {
	case "file_list", "file_read", "exec_file":
		return preferPathArg(skillName, currentArgs, heuristicArgs)
	case "file_write":
		return preferPathArg(skillName, currentArgs, heuristicArgs) || preferStringArg(currentArgs, heuristicArgs, "content")
	case "file_replace":
		return preferPathArg(skillName, currentArgs, heuristicArgs) ||
			preferStringArg(currentArgs, heuristicArgs, "find") ||
			preferStringArg(currentArgs, heuristicArgs, "replace")
	case "service_status", "service_logs", "service_restart":
		return preferStringArg(currentArgs, heuristicArgs, "service")
	case "note_add":
		return preferStringArg(currentArgs, heuristicArgs, "text")
	case "note_delete", "watch_pause", "watch_remove", "watch_test", "watch_history":
		return preferStringArg(currentArgs, heuristicArgs, "id")
	case "watch_add":
		return preferStringArg(currentArgs, heuristicArgs, "spec")
	case "exec_code":
		return preferStringArg(currentArgs, heuristicArgs, "runtime") ||
			preferStringArg(currentArgs, heuristicArgs, "code")
	case "user_add":
		return preferStringArg(currentArgs, heuristicArgs, "provider") ||
			preferStringArg(currentArgs, heuristicArgs, "username") ||
			preferStringArg(currentArgs, heuristicArgs, "password")
	case "user_list":
		return preferStringArg(currentArgs, heuristicArgs, "provider") ||
			preferStringArg(currentArgs, heuristicArgs, "pattern")
	case "user_delete":
		return preferStringArg(currentArgs, heuristicArgs, "provider") ||
			preferStringArg(currentArgs, heuristicArgs, "username")
	default:
		return false
	}
}

func preferPathArg(skillName string, currentArgs map[string]string, heuristicArgs map[string]string) bool {
	current := strings.TrimSpace(currentArgs["path"])
	heuristic := strings.TrimSpace(heuristicArgs["path"])
	if heuristic == "" || current == heuristic {
		return false
	}
	if current == "" {
		return true
	}

	normalizedCurrent := normalizePathArg(current)
	normalizedHeuristic := normalizePathArg(heuristic)
	if normalizedCurrent != "" && normalizedCurrent == normalizedHeuristic {
		return true
	}
	if shouldPreferHeuristicPath(skillName, normalizedCurrent, normalizedHeuristic) {
		return true
	}
	if looksCorruptedArgValue(current, "path") {
		return true
	}
	return false
}

func preferStringArg(currentArgs map[string]string, heuristicArgs map[string]string, key string) bool {
	current := strings.TrimSpace(currentArgs[key])
	heuristic := strings.TrimSpace(heuristicArgs[key])
	if heuristic == "" || current == heuristic {
		return false
	}
	if current == "" {
		return true
	}
	if looksCorruptedArgValue(current, key) {
		return true
	}
	return false
}

func normalizePathArg(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "\"'`,()[]{}"))
	value = strings.TrimSpace(strings.TrimRight(value, ".;:!?"))
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func shouldPreferHeuristicPath(skillName string, current string, heuristic string) bool {
	if current == "" || heuristic == "" || current == heuristic {
		return false
	}

	switch skillName {
	case "file_list":
		if isPathAncestor(heuristic, current) {
			return true
		}
		if filepath.Dir(current) == heuristic {
			return true
		}
		if filepath.Dir(current) == filepath.Dir(heuristic) && filepath.Ext(current) != "" && filepath.Ext(heuristic) == "" {
			return true
		}
	case "file_read", "file_write", "file_replace", "exec_file":
		if isPathAncestor(current, heuristic) {
			return true
		}
		if filepath.Dir(current) == filepath.Dir(heuristic) && filepath.Ext(current) == "" && filepath.Ext(heuristic) != "" {
			return true
		}
	}

	return false
}

func isPathAncestor(parent string, child string) bool {
	parent = filepath.Clean(strings.TrimSpace(parent))
	child = filepath.Clean(strings.TrimSpace(child))
	if parent == "" || child == "" || parent == child {
		return false
	}
	separator := string(filepath.Separator)
	return strings.HasPrefix(child, parent+separator)
}

func looksCorruptedArgValue(value string, key string) bool {
	lowered := strings.ToLower(strings.TrimSpace(value))
	if lowered == "" {
		return false
	}

	switch key {
	case "path":
		return strings.Contains(lowered, " containing ") ||
			strings.Contains(lowered, " with content ") ||
			strings.Contains(lowered, " replace ") ||
			strings.Contains(lowered, " and tell me what it contains")
	case "content":
		return looksLikePath(lowered)
	case "find":
		return strings.Contains(lowered, " with ") ||
			strings.Contains(lowered, " in /") ||
			strings.Contains(lowered, " in ./") ||
			strings.Contains(lowered, " in ../")
	case "replace":
		return strings.Contains(lowered, " in /") ||
			strings.Contains(lowered, " in ./") ||
			strings.Contains(lowered, " in ../")
	case "runtime":
		return !looksLikeRuntime(lowered)
	case "service":
		return strings.Contains(lowered, "service ")
	default:
		return false
	}
}

func (c *Classifier) extractAllowedServiceFromText(text string) string {
	lowered := strings.ToLower(text)
	for _, service := range c.allowedServices {
		service = strings.TrimSpace(service)
		if service == "" {
			continue
		}
		if strings.Contains(lowered, strings.ToLower(service)) {
			return service
		}
	}
	return ""
}

func extractNumericID(text string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func extractNoteTextFromText(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range []string{
		"exact text:",
		"remember this note for me:",
		"save a note with this exact text:",
	} {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return trimTrailingSentencePunctuation(text[idx+len(marker):])
		}
	}
	return ""
}

func extractWatchSpecFromText(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range []string{
		"exact rule:",
		"with this rule:",
		"using this exact rule:",
	} {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return trimTrailingSentencePunctuation(text[idx+len(marker):])
		}
	}
	return ""
}

func extractAccountUsername(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range []string{"user named ", "add user ", "delete user ", "remove account "} {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return firstToken(strings.TrimSpace(text[idx+len(marker):]))
		}
	}
	return ""
}

func extractAccountPassword(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range []string{"using password ", "with password ", "password "} {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return firstToken(strings.TrimSpace(text[idx+len(marker):]))
		}
	}
	return ""
}

func containsAnyFold(text string, fragments ...string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, fragment := range fragments {
		fragment = strings.ToLower(strings.TrimSpace(fragment))
		if fragment == "" {
			continue
		}
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func hasOpenFileIntent(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{
		"open /",
		"open ./",
		"open ../",
		"open ~/",
		"open file ",
		"please open /",
		"please open ./",
		"please open ../",
		"please open ~/",
		"please open file ",
		"could you open /",
		"could you open ./",
		"could you open ../",
		"could you open ~/",
		"could you open file ",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func maxConfidence(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
