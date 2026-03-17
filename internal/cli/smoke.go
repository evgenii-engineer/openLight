package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"openlight/internal/app"
	"openlight/internal/config"
	"openlight/internal/router"
	serviceskills "openlight/internal/skills/services"
)

type SmokeOptions struct {
	IncludeChat    bool
	IncludeRestart bool
	IncludeRouting bool
}

type SmokeStatus string

const (
	SmokePass SmokeStatus = "PASS"
	SmokeFail SmokeStatus = "FAIL"
	SmokeSkip SmokeStatus = "SKIP"
)

type SmokeRow struct {
	Check    string
	Command  string
	Status   SmokeStatus
	Duration time.Duration
	Summary  string
}

type SmokeReport struct {
	Rows []SmokeRow
}

func (r SmokeReport) FailedCount() int {
	_, failed, _ := r.Counts()
	return failed
}

func (r SmokeReport) Counts() (passed, failed, skipped int) {
	for _, row := range r.Rows {
		switch row.Status {
		case SmokePass:
			passed++
		case SmokeFail:
			failed++
		case SmokeSkip:
			skipped++
		}
	}
	return passed, failed, skipped
}

func (r SmokeReport) TotalDuration() time.Duration {
	var total time.Duration
	for _, row := range r.Rows {
		total += row.Duration
	}
	return total
}

func (r SmokeReport) OverallStatus() SmokeStatus {
	_, failed, _ := r.Counts()
	if failed > 0 {
		return SmokeFail
	}
	return SmokePass
}

func (r SmokeReport) RenderTable() string {
	headers := []string{"Check", "Command", "Status", "Duration", "Summary"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3]), len(headers[4])}

	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		record := []string{
			row.Check,
			row.Command,
			string(row.Status),
			formatSmokeDuration(row.Duration),
			row.Summary,
		}
		rows = append(rows, record)
		for idx, value := range record {
			if len(value) > widths[idx] {
				widths[idx] = len(value)
			}
		}
	}

	var lines []string
	lines = append(lines, formatSmokeTableRow(headers, widths))
	lines = append(lines, formatSmokeTableSeparator(widths))
	for _, row := range rows {
		lines = append(lines, formatSmokeTableRow(row, widths))
	}

	passed, failed, skipped := r.Counts()
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Result: %s | pass=%d fail=%d skip=%d | total=%s", r.OverallStatus(), passed, failed, skipped, formatSmokeDuration(r.TotalDuration())))
	lines = append(lines, fmt.Sprintf("Totals: %d/%d completed without failure", passed+skipped, len(r.Rows)))

	return strings.Join(lines, "\n")
}

func RunSmoke(ctx context.Context, cfg config.Config, runtime app.Runtime, userID, chatID int64, options SmokeOptions) (SmokeReport, error) {
	harness := NewHarness(cfg, runtime, userID, chatID)
	state := smokeState{
		serviceName:     firstAllowedService(cfg),
		accountProvider: firstAccountProvider(cfg),
		accountUsername: fmt.Sprintf("smoke_%d", time.Now().UTC().Unix()),
		accountPassword: fmt.Sprintf("smoke-pass-%d", time.Now().UTC().Unix()%100000),
		fileRoot:        firstString(cfg.Files.Allowed),
		allowedFile:     firstString(cfg.Workbench.AllowedFiles),
		workRuntime:     preferredRuntime(cfg.Workbench.AllowedRuntimes),
	}
	if state.fileRoot != "" {
		state.filePath = filepath.Join(state.fileRoot, fmt.Sprintf("openlight-smoke-%d.txt", time.Now().UTC().UnixNano()))
	}
	fileRootReady := false
	fileRootError := ""
	if state.fileRoot != "" {
		if err := os.MkdirAll(state.fileRoot, 0o755); err == nil {
			fileRootReady = true
		} else {
			fileRootError = err.Error()
		}
	}

	var report SmokeReport
	addPassFail := func(check, command string, validate func(string) error) {
		start := time.Now()
		response, err := harness.Exec(ctx, command)
		row := SmokeRow{
			Check:    check,
			Command:  command,
			Duration: time.Since(start),
			Summary:  summarizeSmokeText(response),
			Status:   SmokePass,
		}
		if err != nil {
			row.Status = SmokeFail
			row.Summary = summarizeSmokeText(err.Error())
		} else if isSmokeFrameworkFailure(response) {
			row.Status = SmokeFail
		} else if validate != nil {
			if validateErr := validate(response); validateErr != nil {
				row.Status = SmokeFail
				row.Summary = summarizeSmokeText(validateErr.Error())
			}
		}
		report.Rows = append(report.Rows, row)
	}
	addSkip := func(check, command, reason string) {
		report.Rows = append(report.Rows, SmokeRow{
			Check:   check,
			Command: command,
			Status:  SmokeSkip,
			Summary: reason,
		})
	}

	addPassFail("core.start", "start", expectContains("openLight is ready"))
	addPassFail("core.ping", "ping", expectContains("pong"))
	addPassFail("core.skills", "skills", expectContains("Available skill groups"))
	addPassFail("core.help", "help ping", expectContains("ping:"))

	addPassFail("system.status", "status", expectContains("Hostname:"))
	addPassFail("system.cpu", "cpu", expectContains("CPU"))
	addPassFail("system.memory", "memory", expectContains("Memory"))
	addPassFail("system.disk", "disk", expectContains("Disk"))
	addPassFail("system.uptime", "uptime", expectContains("Uptime"))
	addPassFail("system.hostname", "hostname", expectContains("Hostname:"))
	addPassFail("system.ip", "ip", expectContains("IP"))
	addPassFail("system.temperature", "temperature", expectContains("Temperature"))

	if state.serviceName == "" {
		addSkip("services.list", "services", "no allowed services")
		addSkip("services.status", "", "no allowed services")
		addSkip("services.logs", "", "no allowed services")
		addSkip("services.restart", "", "no allowed services")
	} else {
		addPassFail("services.list", "services", expectContains(state.serviceName))
		addPassFail("services.status", "service "+state.serviceName, expectContains("Service:"))
		addPassFail("services.logs", "logs "+state.serviceName, expectNonEmpty())
		if options.IncludeRestart {
			addPassFail("services.restart", "restart "+state.serviceName, expectContains("Service restarted"))
		} else {
			addSkip("services.restart", "restart "+state.serviceName, "skipped for safety; rerun with restart enabled")
		}
	}

	addPassFail("notes.add", "note "+state.noteText(), func(response string) error {
		noteID, err := parseSmokeNoteID(response)
		if err != nil {
			return err
		}
		state.noteID = noteID
		return nil
	})
	addPassFail("notes.list", "notes", expectContains(state.noteText()))
	if state.noteID == "" {
		addSkip("notes.delete", "note_delete", "note id not captured from add step")
	} else {
		addPassFail("notes.delete", "note_delete "+state.noteID, expectContains("Deleted note"))
	}

	if state.fileRoot == "" || state.filePath == "" {
		addSkip("files.list", "files", "no file roots configured")
		addSkip("files.write", "", "no file roots configured")
		addSkip("files.read", "", "no file roots configured")
		addSkip("files.replace", "", "no file roots configured")
	} else if !fileRootReady {
		reason := "failed to prepare file root"
		if fileRootError != "" {
			reason += ": " + summarizeSmokeText(fileRootError)
		}
		addSkip("files.list", "files "+state.fileRoot, reason)
		addSkip("files.write", "write "+state.filePath+" :: smoke-alpha", reason)
		addSkip("files.read", "read "+state.filePath, reason)
		addSkip("files.replace", "replace smoke-alpha with smoke-beta in "+state.filePath, reason)
		addSkip("files.read_after_replace", "read "+state.filePath, reason)
	} else {
		addPassFail("files.list", "files "+state.fileRoot, expectContains(filepath.Base(state.fileRoot)))
		addPassFail("files.write", "write "+state.filePath+" :: smoke-alpha", expectContains("file:"))
		addPassFail("files.read", "read "+state.filePath, expectContains("smoke-alpha"))
		addPassFail("files.replace", "replace smoke-alpha with smoke-beta in "+state.filePath, expectContains("Replaced"))
		addPassFail("files.read_after_replace", "read "+state.filePath, expectContains("smoke-beta"))
	}

	if !cfg.Workbench.Enabled {
		addSkip("workbench.exec_code", "", "workbench disabled")
	} else if state.workRuntime == "" {
		addSkip("workbench.exec_code", "", "no workbench runtime configured")
	} else {
		addPassFail("workbench.exec_code", smokeExecCodeCommand(state.workRuntime), expectContains("smoke-workbench-ok"))
	}
	if !cfg.Workbench.Enabled {
		addSkip("workbench.exec_file", "", "workbench disabled")
	} else if state.allowedFile == "" {
		addSkip("workbench.exec_file", "", "no allowed workbench file configured")
	} else {
		addPassFail("workbench.exec_file", "run "+state.allowedFile, expectNonEmpty())
	}
	if cfg.Workbench.Enabled {
		addPassFail("workbench.clean", "workspace_clean", expectContains("Workspace"))
	} else {
		addSkip("workbench.clean", "workspace_clean", "workbench disabled")
	}

	if state.accountProvider == "" {
		addSkip("accounts.providers", "users", "no account providers configured")
		addSkip("accounts.add", "", "no account providers configured")
		addSkip("accounts.list", "", "no account providers configured")
		addSkip("accounts.delete", "", "no account providers configured")
		addSkip("accounts.list_after_delete", "", "no account providers configured")
	} else {
		addPassFail("accounts.providers", "users", expectContains(state.accountProvider))
		addPassFail("accounts.add", fmt.Sprintf("user add %s %s %s", state.accountProvider, state.accountUsername, state.accountPassword), expectContains(state.accountUsername))
		addPassFail("accounts.list", fmt.Sprintf("user list %s %s", state.accountProvider, state.accountUsername), expectContains(state.accountUsername))
		addPassFail("accounts.delete", fmt.Sprintf("user delete %s %s", state.accountProvider, state.accountUsername), expectContains(state.accountUsername))
		addPassFail("accounts.list_after_delete", fmt.Sprintf("user list %s %s", state.accountProvider, state.accountUsername), expectNotContains(state.accountUsername))
	}

	if cfg.LLM.Enabled {
		if options.IncludeRouting {
			addRoutingCheck(&report, runtime, ctx, "llm.route_status", func(decision router.Decision) error {
				if decision.Mode != router.ModeLLM {
					return fmt.Errorf("expected llm mode, got %s", decision.Mode)
				}
				if decision.SkillName != "status" {
					return fmt.Errorf("expected status skill, got %s", decision.SkillName)
				}
				return nil
			}, "Could you give me a quick health snapshot of this host?")
			if state.serviceName == "" {
				addSkip("llm.route_service_status", "Can you check whether the configured service is healthy right now?", "no allowed services configured")
			} else {
				addRoutingCheck(&report, runtime, ctx, "llm.route_service_status", func(decision router.Decision) error {
					if decision.Mode != router.ModeLLM {
						return fmt.Errorf("expected llm mode, got %s", decision.Mode)
					}
					if decision.SkillName != "service_status" {
						return fmt.Errorf("expected service_status skill, got %s", decision.SkillName)
					}
					if decision.Args["service"] != state.serviceName {
						return fmt.Errorf("expected service arg %q, got %q", state.serviceName, decision.Args["service"])
					}
					return nil
				},
					fmt.Sprintf("I need to know if service called %s is up.", state.serviceName),
					fmt.Sprintf("Please tell me whether service %s is running.", state.serviceName),
					fmt.Sprintf("Can you check service %s for me?", state.serviceName),
				)
			}
		} else {
			addSkip("llm.route_status", "Could you give me a quick health snapshot of this host?", "skipped to avoid extra LLM routing cost; rerun with routing enabled")
			addSkip("llm.route_service_status", "Can you check whether the configured service is healthy right now?", "skipped to avoid extra LLM routing cost; rerun with routing enabled")
		}

		if options.IncludeChat {
			addPassFail("chat.chat", "chat reply with exactly SMOKE_CHAT_OK", expectNonEmpty())
		} else {
			addSkip("chat.chat", "chat reply with exactly SMOKE_CHAT_OK", "skipped to avoid LLM cost; rerun with chat enabled")
		}
	}

	if state.filePath != "" {
		_ = os.Remove(state.filePath)
	}

	if report.FailedCount() > 0 {
		return report, fmt.Errorf("smoke suite failed with %d check(s)", report.FailedCount())
	}
	return report, nil
}

type smokeState struct {
	serviceName     string
	accountProvider string
	accountUsername string
	accountPassword string
	fileRoot        string
	filePath        string
	allowedFile     string
	workRuntime     string
	noteID          string
}

func (s smokeState) noteText() string {
	return "openlight-smoke-note-" + strings.ReplaceAll(s.accountUsername, "_", "-")
}

func firstAllowedService(cfg config.Config) string {
	names, err := serviceskills.AllowedServiceNames(cfg.Services.Allowed)
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0]
}

func firstAccountProvider(cfg config.Config) string {
	if len(cfg.Accounts.Providers) == 0 {
		return ""
	}
	names := make([]string, 0, len(cfg.Accounts.Providers))
	for name := range cfg.Accounts.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func addRoutingCheck(report *SmokeReport, runtime app.Runtime, ctx context.Context, check string, validate func(router.Decision) error, prompts ...string) {
	start := time.Now()
	row := SmokeRow{
		Check:   check,
		Command: firstString(prompts),
		Status:  SmokePass,
	}
	if runtime.Classifier == nil {
		row.Status = SmokeSkip
		row.Summary = "llm classifier not configured"
		report.Rows = append(report.Rows, row)
		return
	}

	var failures []string
	engine := router.New(runtime.Registry, runtime.Classifier)
	for _, prompt := range prompts {
		row.Command = prompt
		decision, err := engine.Route(ctx, prompt)
		if err != nil {
			failures = append(failures, summarizeSmokeText(prompt+": "+err.Error()))
			continue
		}
		if !decision.Matched() || decision.Mode == router.ModeUnknown {
			failures = append(failures, summarizeSmokeText(prompt+": router returned no executable decision"))
			continue
		}
		if validate != nil {
			if validateErr := validate(decision); validateErr != nil {
				failures = append(failures, summarizeSmokeText(prompt+": "+validateErr.Error()))
				continue
			}
		}
		row.Duration = time.Since(start)
		row.Summary = summarizeSmokeDecision(decision)
		report.Rows = append(report.Rows, row)
		return
	}

	row.Duration = time.Since(start)
	row.Status = SmokeFail
	if len(failures) == 0 {
		row.Summary = "no routing prompts configured"
	} else {
		row.Summary = summarizeSmokeText(strings.Join(failures, " || "))
	}
	report.Rows = append(report.Rows, row)
}

func preferredRuntime(runtimes []string) string {
	preferred := []string{"sh", "bash", "python", "python3", "node", "js"}
	normalized := make(map[string]struct{}, len(runtimes))
	for _, runtime := range runtimes {
		normalized[strings.ToLower(strings.TrimSpace(runtime))] = struct{}{}
	}
	for _, candidate := range preferred {
		if _, ok := normalized[candidate]; ok {
			return candidate
		}
	}
	if len(runtimes) == 0 {
		return ""
	}
	return strings.TrimSpace(runtimes[0])
}

func smokeExecCodeCommand(runtime string) string {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "python", "python3":
		return "exec_code " + runtime + " :: print('smoke-workbench-ok')"
	case "node", "js", "javascript":
		return "exec_code " + runtime + " :: console.log('smoke-workbench-ok')"
	default:
		return "exec_code " + runtime + " :: printf 'smoke-workbench-ok\\n'"
	}
}

func expectContains(fragment string) func(string) error {
	return func(response string) error {
		if !strings.Contains(response, fragment) {
			return fmt.Errorf("expected response to contain %q, got %q", fragment, summarizeSmokeText(response))
		}
		return nil
	}
}

func expectNotContains(fragment string) func(string) error {
	return func(response string) error {
		if strings.Contains(response, fragment) {
			return fmt.Errorf("expected response not to contain %q, got %q", fragment, summarizeSmokeText(response))
		}
		return nil
	}
}

func expectNonEmpty() func(string) error {
	return func(response string) error {
		if strings.TrimSpace(response) == "" {
			return fmt.Errorf("expected non-empty response")
		}
		return nil
	}
}

var smokeNoteIDPattern = regexp.MustCompile(`Saved note #([0-9]+)`)

func parseSmokeNoteID(response string) (string, error) {
	matches := smokeNoteIDPattern.FindStringSubmatch(response)
	if len(matches) != 2 {
		return "", fmt.Errorf("failed to parse note id from %q", summarizeSmokeText(response))
	}
	return matches[1], nil
}

func isSmokeFrameworkFailure(response string) bool {
	switch strings.TrimSpace(strings.ToLower(response)) {
	case "access denied", "invalid arguments", "internal error", "not found", "request timed out", "skill not found", "unavailable":
		return true
	default:
		return false
	}
}

func summarizeSmokeText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(empty)"
	}
	value = strings.ReplaceAll(value, "\n", " | ")
	runes := []rune(value)
	if len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return value
}

func summarizeSmokeDecision(decision router.Decision) string {
	args := make([]string, 0, len(decision.Args))
	keys := make([]string, 0, len(decision.Args))
	for key := range decision.Args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, fmt.Sprintf("%s=%s", key, decision.Args[key]))
	}

	parts := []string{
		"mode=" + string(decision.Mode),
		"skill=" + decision.SkillName,
		fmt.Sprintf("confidence=%.2f", decision.Confidence),
	}
	if len(args) > 0 {
		parts = append(parts, "args="+strings.Join(args, ","))
	}
	return summarizeSmokeText(strings.Join(parts, " | "))
}

func formatSmokeDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	if value < time.Second {
		return fmt.Sprintf("%dms", value.Milliseconds())
	}
	return value.Round(time.Millisecond).String()
}

func formatSmokeTableRow(values []string, widths []int) string {
	parts := make([]string, 0, len(values))
	for idx, value := range values {
		parts = append(parts, padSmokeCell(value, widths[idx]))
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

func formatSmokeTableSeparator(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width))
	}
	return "|-" + strings.Join(parts, "-|-") + "-|"
}

func padSmokeCell(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}
