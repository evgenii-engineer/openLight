package cli

import (
	"context"
	"fmt"
	"io"
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
	ProgressWriter io.Writer
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

type SmokeLatencySummary struct {
	Count int
	Min   time.Duration
	P50   time.Duration
	P95   time.Duration
	Max   time.Duration
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

func (r SmokeReport) LatencySummary() (SmokeLatencySummary, bool) {
	return buildSmokeLatencySummary(r.Rows, func(row SmokeRow) bool {
		return row.Status != SmokeSkip
	})
}

func (r SmokeReport) LLMLatencySummary() (SmokeLatencySummary, bool) {
	return buildSmokeLatencySummary(r.Rows, func(row SmokeRow) bool {
		if row.Status == SmokeSkip {
			return false
		}
		return isSmokeLLMCheck(row.Check)
	})
}

func (r SmokeReport) SlowestRows(limit int) []SmokeRow {
	if limit <= 0 {
		return nil
	}

	rows := make([]SmokeRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Status == SmokeSkip || row.Duration <= 0 {
			continue
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Duration == rows[j].Duration {
			return rows[i].Check < rows[j].Check
		}
		return rows[i].Duration > rows[j].Duration
	})

	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
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
	if stats, ok := r.LatencySummary(); ok {
		lines = append(lines, fmt.Sprintf("Latency: checks=%d min=%s p50=%s p95=%s max=%s", stats.Count, formatSmokeDuration(stats.Min), formatSmokeDuration(stats.P50), formatSmokeDuration(stats.P95), formatSmokeDuration(stats.Max)))
	}
	if stats, ok := r.LLMLatencySummary(); ok {
		lines = append(lines, fmt.Sprintf("Latency LLM: checks=%d min=%s p50=%s p95=%s max=%s", stats.Count, formatSmokeDuration(stats.Min), formatSmokeDuration(stats.P50), formatSmokeDuration(stats.P95), formatSmokeDuration(stats.Max)))
	}
	if slowest := r.SlowestRows(3); len(slowest) > 0 {
		lines = append(lines, "Slowest: "+formatSmokeSlowestRows(slowest))
	}

	return strings.Join(lines, "\n")
}

func RunSmoke(ctx context.Context, cfg config.Config, runtime app.Runtime, userID, chatID int64, options SmokeOptions) (SmokeReport, error) {
	harness := NewHarness(cfg, runtime, userID, chatID)
	serviceNames := allAllowedServices(cfg)
	accountProviders := allAccountProviders(cfg)
	state := smokeState{
		serviceName:     firstString(serviceNames),
		accountProvider: firstString(accountProviders),
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
		writeSmokeProgressStart(options.ProgressWriter, check, command)
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
		writeSmokeProgressDone(options.ProgressWriter, row)
	}
	addSkip := func(check, command, reason string) {
		row := SmokeRow{
			Check:   check,
			Command: command,
			Status:  SmokeSkip,
			Summary: reason,
		}
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(options.ProgressWriter, row)
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

	if len(serviceNames) == 0 {
		addSkip("services.list", "services", "no allowed services")
		addSkip("services.status", "", "no allowed services")
		addSkip("services.logs", "", "no allowed services")
		addSkip("services.restart", "", "no allowed services")
	} else {
		addPassFail("services.list", "services", expectContainsAll(serviceNames...))
		addPassFail("services.status", "service "+state.serviceName, expectContains("Service:"))
		addPassFail("services.logs", "logs "+state.serviceName, expectNonEmpty())
		for _, serviceName := range serviceNames[1:] {
			addPassFail("services.status."+serviceName, "service "+serviceName, expectContains("Service:"))
			addPassFail("services.logs."+serviceName, "logs "+serviceName, expectNonEmpty())
		}
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

	if !cfg.Watch.Enabled || runtime.Watch == nil {
		addSkip("watch.add", "watch add memory > 0.1% for 1ms cooldown 1m", "watch subsystem disabled")
		addSkip("watch.list", "watch list", "watch subsystem disabled")
		addSkip("watch.test", "watch test", "watch subsystem disabled")
		addSkip("watch.run_once", "watch runner cycle", "watch subsystem disabled")
		addSkip("watch.history", "watch history", "watch subsystem disabled")
		addSkip("watch.pause", "watch pause", "watch subsystem disabled")
		addSkip("watch.resume", "watch pause", "watch subsystem disabled")
		addSkip("watch.remove", "watch remove", "watch subsystem disabled")
	} else {
		addPassFail("watch.add", "watch add memory > 0.1% for 1ms cooldown 1m", func(response string) error {
			watchID, err := parseSmokeWatchID(response)
			if err != nil {
				return err
			}
			state.watchID = watchID
			return expectContains("memory high")(response)
		})
		if state.watchID == "" {
			addSkip("watch.list", "watch list", "watch id not captured from add step")
			addSkip("watch.test", "watch test", "watch id not captured from add step")
			addSkip("watch.run_once", "watch runner cycle", "watch id not captured from add step")
			addSkip("watch.history", "watch history", "watch id not captured from add step")
			addSkip("watch.pause", "watch pause", "watch id not captured from add step")
			addSkip("watch.resume", "watch pause", "watch id not captured from add step")
			addSkip("watch.remove", "watch remove", "watch id not captured from add step")
		} else {
			addPassFail("watch.list", "watch list", expectContains("#"+state.watchID))
			addPassFail("watch.test", "watch test "+state.watchID, expectContains("Watch #"+state.watchID))
			addWatchRunnerCheck(&report, runtime, harness, options.ProgressWriter, ctx, "watch.run_once", "watch runner cycle", expectContainsAllFold("alert #", "memory"))
			addPassFail("watch.history", "watch history "+state.watchID, expectContainsAll("#", "memory"))
			addPassFail("watch.pause", "watch pause "+state.watchID, expectContains("paused"))
			addPassFail("watch.resume", "watch pause "+state.watchID, expectContains("enabled"))
			addPassFail("watch.remove", "watch remove "+state.watchID, expectContains("Removed watch"))
		}
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

	if len(accountProviders) == 0 {
		addSkip("accounts.providers", "users", "no account providers configured")
		addSkip("accounts.add", "", "no account providers configured")
		addSkip("accounts.list", "", "no account providers configured")
		addSkip("accounts.delete", "", "no account providers configured")
		addSkip("accounts.list_after_delete", "", "no account providers configured")
	} else {
		addPassFail("accounts.providers", "users", expectContainsAll(accountProviders...))
		for idx, provider := range accountProviders {
			providerCfg := cfg.Accounts.Providers[provider]
			username := smokeAccountUsername(state.accountUsername, provider)
			addCheck := "accounts.add"
			listCheck := "accounts.list"
			deleteCheck := "accounts.delete"
			listAfterDeleteCheck := "accounts.list_after_delete"
			if idx > 0 {
				addCheck += "." + provider
				listCheck += "." + provider
				deleteCheck += "." + provider
				listAfterDeleteCheck += "." + provider
			}

			addCommand := fmt.Sprintf("user add %s %s %s", provider, username, state.accountPassword)
			listCommand := fmt.Sprintf("user list %s %s", provider, username)
			deleteCommand := fmt.Sprintf("user delete %s %s", provider, username)

			if len(providerCfg.AddCommand) == 0 {
				addSkip(addCheck, addCommand, "provider add_command is not configured")
			} else {
				addPassFail(addCheck, addCommand, expectContains(username))
			}

			if len(providerCfg.ListCommand) == 0 {
				addSkip(listCheck, listCommand, "provider list_command is not configured")
			} else {
				addPassFail(listCheck, listCommand, expectContains(username))
			}

			if len(providerCfg.DeleteCommand) == 0 {
				addSkip(deleteCheck, deleteCommand, "provider delete_command is not configured")
			} else {
				addPassFail(deleteCheck, deleteCommand, expectContains(username))
			}

			if len(providerCfg.ListCommand) == 0 {
				addSkip(listAfterDeleteCheck, listCommand, "provider list_command is not configured")
			} else {
				addPassFail(listAfterDeleteCheck, listCommand, expectNotContains(username))
			}
		}
	}

	if cfg.LLM.Enabled {
		if options.IncludeRouting {
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.start", expectDecisionSkill("start", nil), expectContains("openLight is ready"),
				"I just connected. Show me the short getting started message for this bot.",
				"Give me the welcome introduction and first steps for openLight.",
				"Please show the start message for this bot.",
				"Please show the onboarding message for this bot.",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.ping", expectDecisionSkill("ping", nil), expectContains("pong"),
				"Could you run a quick connectivity check for the bot?",
				"Do a fast alive check and tell me the result.",
				"Please run a ping check for this bot.",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.skills", expectDecisionSkill("skills", nil), expectContains("Available skill groups"),
				"What built-in skill groups can you handle here?",
				"List the available tool groups in this assistant.",
				"Please list the available skill groups for this bot.",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.help", expectDecisionSkill("help", nil), expectContains("You can talk to me normally."),
				"Show me the general help message for this assistant.",
				"I need the default help text for this bot.",
			)

			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.status", expectDecisionSkill("status", nil), expectContains("Hostname:"),
				"Could you give me a quick health snapshot of this host?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.cpu", expectDecisionSkill("cpu", nil), expectContains("CPU usage:"),
				"How busy is the processor right now?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.memory", expectDecisionSkill("memory", nil), expectContains("Memory usage:"),
				"How much RAM is being used right now?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.disk", expectDecisionSkill("disk", nil), expectContains("Disk usage:"),
				"How much storage space is left on the root filesystem?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.uptime", expectDecisionSkill("uptime", nil), expectContains("Uptime:"),
				"How long has this machine been running?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.hostname", expectDecisionSkill("hostname", nil), expectContains("Hostname:"),
				"What is the hostname of this machine?",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.ip", expectDecisionSkill("ip", nil), expectContains("IP addresses:"),
				"What local IP addresses does this machine have?",
				"Please show this machine's local IP addresses.",
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.temperature", expectDecisionSkill("temperature", nil), expectContains("Temperature:"),
				"How hot is the device right now?",
			)

			if state.serviceName == "" {
				addSkip("llm.fallback.service_list", "Which allowed services can you manage on this host?", "no allowed services configured")
				addSkip("llm.fallback.service_status", "", "no allowed services configured")
				addSkip("llm.fallback.service_logs", "", "no allowed services configured")
				addSkip("llm.fallback.service_restart", "", "no allowed services configured")
			} else {
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.service_list", expectDecisionSkill("service_list", nil), expectContainsAll(serviceNames...),
					"Which allowed services can you manage on this host?",
				)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.service_status", expectDecisionSkill("service_status", map[string]string{"service": state.serviceName}), expectContains("Service:"),
					fmt.Sprintf("Please check whether service %s is running.", state.serviceName),
					fmt.Sprintf("I need a status update for service %s.", state.serviceName),
				)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.service_logs", expectDecisionSkill("service_logs", map[string]string{"service": state.serviceName}), expectNonEmpty(),
					fmt.Sprintf("Show me the recent logs for service %s.", state.serviceName),
					fmt.Sprintf("I want to inspect recent log lines from %s.", state.serviceName),
				)
				if options.IncludeRestart {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.service_restart", expectDecisionSkill("service_restart", map[string]string{"service": state.serviceName}), expectContains("Service restarted"),
						fmt.Sprintf("Please restart service %s now.", state.serviceName),
						fmt.Sprintf("I need you to bounce service %s right away.", state.serviceName),
					)
				} else {
					addSkip("llm.fallback.service_restart", "Please restart the configured service.", "skipped for safety; rerun with restart enabled")
				}
			}

			llmNoteText := "openlight-smoke-llm-note-" + strings.ReplaceAll(state.accountUsername, "_", "-")
			llmNoteID := ""
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.note_add", expectDecisionSkill("note_add", nil), func(response string) error {
				noteID, err := parseSmokeNoteID(response)
				if err != nil {
					return err
				}
				llmNoteID = noteID
				return nil
			},
				fmt.Sprintf("Please save a note with this exact text: %s", llmNoteText),
				fmt.Sprintf("Remember this note for me: %s", llmNoteText),
			)
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.note_list", expectDecisionSkill("note_list", nil), expectContains(llmNoteText),
				"Could you list my saved notes?",
				"What notes are currently saved?",
			)
			if llmNoteID == "" {
				addSkip("llm.fallback.note_delete", "Please remove the LLM-created smoke note.", "note id not captured from llm add step")
			} else {
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.note_delete", expectDecisionSkill("note_delete", map[string]string{"id": llmNoteID}), expectContains("Deleted note"),
					fmt.Sprintf("I no longer need saved note %s. Remove it.", llmNoteID),
					fmt.Sprintf("Please delete note %s for me.", llmNoteID),
				)
			}

			llmWatchRule := "memory > 0.1% for 1ms cooldown 1m"
			llmWatchID := ""
			if !cfg.Watch.Enabled || runtime.Watch == nil {
				addSkip("llm.fallback.watch_add", "Create an LLM smoke watch.", "watch subsystem disabled")
				addSkip("llm.fallback.watch_list", "", "watch subsystem disabled")
				addSkip("llm.fallback.watch_test", "", "watch subsystem disabled")
				addSkip("llm.watch.run_once", "", "watch subsystem disabled")
				addSkip("llm.fallback.watch_history", "", "watch subsystem disabled")
				addSkip("llm.fallback.watch_pause", "", "watch subsystem disabled")
				addSkip("llm.fallback.watch_remove", "", "watch subsystem disabled")
			} else {
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_add", func(decision router.Decision) error {
					if decision.SkillName != "watch_add" {
						return fmt.Errorf("expected watch_add skill, got %s", decision.SkillName)
					}
					if !strings.Contains(strings.ToLower(decision.Args["spec"]), "memory") {
						return fmt.Errorf("expected watch spec to mention memory, got %q", decision.Args["spec"])
					}
					return nil
				}, func(response string) error {
					watchID, err := parseSmokeWatchID(response)
					if err != nil {
						return err
					}
					llmWatchID = watchID
					return expectContains("memory high")(response)
				},
					fmt.Sprintf("Please create a watch using this exact rule: %s", llmWatchRule),
					fmt.Sprintf("Set up monitoring with this rule: %s", llmWatchRule),
				)
				if llmWatchID == "" {
					addSkip("llm.fallback.watch_list", "List configured watches.", "watch id not captured from llm add step")
					addSkip("llm.fallback.watch_test", "Probe the LLM watch.", "watch id not captured from llm add step")
					addSkip("llm.watch.run_once", "watch runner cycle", "watch id not captured from llm add step")
					addSkip("llm.fallback.watch_history", "Show incidents for the LLM watch.", "watch id not captured from llm add step")
					addSkip("llm.fallback.watch_pause", "Pause the LLM watch.", "watch id not captured from llm add step")
					addSkip("llm.fallback.watch_remove", "Remove the LLM watch.", "watch id not captured from llm add step")
				} else {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_list", expectDecisionSkill("watch_list", nil), expectContains("#"+llmWatchID),
						"What watch rules are configured right now?",
					)
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_test", expectDecisionSkill("watch_test", map[string]string{"id": llmWatchID}), expectContains("Watch #"+llmWatchID),
						fmt.Sprintf("Could you probe watch %s right now?", llmWatchID),
					)
					addWatchRunnerCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.watch.run_once", "watch runner cycle", expectContainsAllFold("alert #", "memory"))
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_history", expectDecisionSkill("watch_history", map[string]string{"id": llmWatchID}), expectContainsAll("#", "memory"),
						fmt.Sprintf("I want to inspect recent incidents for watch %s.", llmWatchID),
					)
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_pause", expectDecisionSkill("watch_pause", map[string]string{"id": llmWatchID}), expectContains("paused"),
						fmt.Sprintf("Please pause watch %s for now.", llmWatchID),
					)
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.watch_remove", expectDecisionSkill("watch_remove", map[string]string{"id": llmWatchID}), expectContains("Removed watch"),
						fmt.Sprintf("Please remove watch %s from monitoring.", llmWatchID),
					)
				}
			}

			llmFilePath := ""
			if state.fileRoot != "" {
				llmFilePath = filepath.Join(state.fileRoot, fmt.Sprintf("openlight-smoke-llm-%d.txt", time.Now().UTC().UnixNano()))
			}
			if state.fileRoot == "" || llmFilePath == "" {
				addSkip("llm.fallback.file_list", "List the configured file root.", "no file roots configured")
				addSkip("llm.fallback.file_write", "", "no file roots configured")
				addSkip("llm.fallback.file_replace", "", "no file roots configured")
				addSkip("llm.fallback.file_read", "", "no file roots configured")
			} else if !fileRootReady {
				reason := "failed to prepare file root"
				if fileRootError != "" {
					reason += ": " + summarizeSmokeText(fileRootError)
				}
				addSkip("llm.fallback.file_list", "List the configured file root.", reason)
				addSkip("llm.fallback.file_write", "Create an LLM smoke file.", reason)
				addSkip("llm.fallback.file_replace", "Edit the LLM smoke file.", reason)
				addSkip("llm.fallback.file_read", "Read the LLM smoke file.", reason)
			} else {
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.file_write", expectDecisionSkill("file_write", map[string]string{"path": llmFilePath}), expectContains("file:"),
					fmt.Sprintf("Create a text file at %s containing smoke-gamma", llmFilePath),
					fmt.Sprintf("Please write smoke-gamma into %s", llmFilePath),
					fmt.Sprintf("Please use the file write skill to create %s with content smoke-gamma.", llmFilePath),
				)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.file_list", expectDecisionSkill("file_list", map[string]string{"path": state.fileRoot}), expectContains(filepath.Base(llmFilePath)),
					fmt.Sprintf("What files are in %s right now?", state.fileRoot),
				)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.file_replace", expectDecisionSkill("file_replace", map[string]string{"path": llmFilePath}), expectContains("Replaced"),
					fmt.Sprintf("In %s replace smoke-gamma with smoke-delta.", llmFilePath),
					fmt.Sprintf("Please use file replace on %s: replace smoke-gamma with smoke-delta.", llmFilePath),
				)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.file_read", expectDecisionSkill("file_read", map[string]string{"path": llmFilePath}), expectContains("smoke-delta"),
					fmt.Sprintf("Open %s and tell me what it contains.", llmFilePath),
					fmt.Sprintf("Could you read %s for me?", llmFilePath),
					fmt.Sprintf("Please read file %s.", llmFilePath),
				)
				_ = os.Remove(llmFilePath)
			}

			if !cfg.Workbench.Enabled {
				addSkip("llm.fallback.exec_code", "", "workbench disabled")
				addSkip("llm.fallback.exec_file", "", "workbench disabled")
				addSkip("llm.fallback.workspace_clean", "", "workbench disabled")
			} else {
				if state.workRuntime == "" {
					addSkip("llm.fallback.exec_code", "", "no workbench runtime configured")
				} else {
					llmWorkbenchToken := "smoke-workbench-llm-ok"
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.exec_code", expectDecisionSkill("exec_code", map[string]string{"runtime": state.workRuntime}), expectContains(llmWorkbenchToken),
						smokeExecCodePrompt(state.workRuntime, llmWorkbenchToken),
						fmt.Sprintf("Run code with runtime %s: %s", state.workRuntime, smokeExecCodeSnippet(state.workRuntime, llmWorkbenchToken)),
					)
				}
				if state.allowedFile == "" {
					addSkip("llm.fallback.exec_file", "", "no allowed workbench file configured")
				} else {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.exec_file", expectDecisionSkill("exec_file", map[string]string{"path": state.allowedFile}), expectNonEmpty(),
						fmt.Sprintf("Please run the allowed file %s and show me the output.", state.allowedFile),
					)
				}
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.workspace_clean", expectDecisionSkill("workspace_clean", nil), expectContains("Workspace"),
					"Please clean the workbench workspace for me.",
					"Clear the temporary workbench workspace now.",
				)
			}

			if len(accountProviders) == 0 {
				addSkip("llm.fallback.user_providers", "Which account providers are configured?", "no account providers configured")
				addSkip("llm.fallback.user_add", "", "no account providers configured")
				addSkip("llm.fallback.user_list", "", "no account providers configured")
				addSkip("llm.fallback.user_delete", "", "no account providers configured")
			} else {
				llmAccountUsername := smokeAccountUsername(state.accountUsername+"_llm", state.accountProvider)
				addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.user_providers", expectDecisionSkill("user_providers", nil), expectContains(state.accountProvider),
					"Which account providers are configured here?",
					"List the configured account providers for this bot.",
				)
				providerCfg := cfg.Accounts.Providers[state.accountProvider]
				if len(providerCfg.AddCommand) == 0 {
					addSkip("llm.fallback.user_add", "Create an LLM smoke account user.", "provider add_command is not configured")
				} else {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.user_add", expectDecisionSkill("user_add", map[string]string{"username": llmAccountUsername}), expectContains(llmAccountUsername),
						fmt.Sprintf("Create a user named %s in provider %s with password %s.", llmAccountUsername, state.accountProvider, state.accountPassword),
						fmt.Sprintf("Please add user %s to %s using password %s.", llmAccountUsername, state.accountProvider, state.accountPassword),
					)
				}
				if len(providerCfg.ListCommand) == 0 {
					addSkip("llm.fallback.user_list", "List LLM smoke account users.", "provider list_command is not configured")
				} else {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.user_list", expectDecisionSkill("user_list", map[string]string{"pattern": llmAccountUsername}), expectContains(llmAccountUsername),
						fmt.Sprintf("List users in provider %s matching %s.", state.accountProvider, llmAccountUsername),
						fmt.Sprintf("Show me users from %s filtered by %s.", state.accountProvider, llmAccountUsername),
						fmt.Sprintf("Please list users for provider %s matching %s.", state.accountProvider, llmAccountUsername),
					)
				}
				if len(providerCfg.DeleteCommand) == 0 {
					addSkip("llm.fallback.user_delete", "Delete the LLM smoke account user.", "provider delete_command is not configured")
				} else {
					addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.user_delete", expectDecisionSkill("user_delete", map[string]string{"username": llmAccountUsername}), expectContains(llmAccountUsername),
						fmt.Sprintf("Delete user %s from provider %s.", llmAccountUsername, state.accountProvider),
						fmt.Sprintf("Please remove account %s from %s.", llmAccountUsername, state.accountProvider),
					)
				}
			}
		} else {
			addSkip("llm.fallback.all", "llm fallback skill suite", "skipped to avoid extra LLM routing cost; rerun with routing enabled")
		}

		if options.IncludeChat {
			addPassFail("chat.chat", "chat reply with exactly SMOKE_CHAT_OK", expectNonEmpty())
			addLLMFallbackExecCheck(&report, runtime, harness, options.ProgressWriter, ctx, "llm.fallback.chat", expectDecisionSkill("chat", nil), expectContains("SMOKE_CHAT_LLM_OK"),
				"Please answer in one short line and include token SMOKE_CHAT_LLM_OK.",
				"Let's just chat for a moment. Reply in one short line and include token SMOKE_CHAT_LLM_OK.",
				"This is a normal chat reply, not a tool request. Answer in one short line and include token SMOKE_CHAT_LLM_OK.",
			)
		} else {
			addSkip("chat.chat", "chat reply with exactly SMOKE_CHAT_OK", "skipped to avoid LLM cost; rerun with chat enabled")
			addSkip("llm.fallback.chat", "Please answer in one short line and include token SMOKE_CHAT_LLM_OK.", "skipped to avoid LLM cost; rerun with chat enabled")
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
	watchID         string
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

func allAllowedServices(cfg config.Config) []string {
	names, err := serviceskills.AllowedServiceNames(cfg.Services.Allowed)
	if err != nil {
		return nil
	}
	return names
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

func allAccountProviders(cfg config.Config) []string {
	if len(cfg.Accounts.Providers) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Accounts.Providers))
	for name := range cfg.Accounts.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func smokeAccountUsername(base, provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	provider = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(provider)
	provider = strings.Trim(provider, "_")
	if provider == "" {
		return base
	}
	return base + "_" + provider
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

func addLLMFallbackExecCheck(
	report *SmokeReport,
	runtime app.Runtime,
	harness *Harness,
	progress io.Writer,
	ctx context.Context,
	check string,
	validateDecision func(router.Decision) error,
	validateResponse func(string) error,
	prompts ...string,
) {
	start := time.Now()
	row := SmokeRow{
		Check:   check,
		Command: firstString(prompts),
		Status:  SmokePass,
	}
	writeSmokeProgressStart(progress, row.Check, row.Command)
	if runtime.Classifier == nil {
		row.Status = SmokeSkip
		row.Summary = "llm classifier not configured"
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(progress, row)
		return
	}

	var failures []string
	for _, prompt := range prompts {
		row.Command = prompt

		// Query the classifier directly so fallback checks are not preempted
		// by slash, explicit, or rule-based routing before the LLM runs.
		decision, ok, err := runtime.Classifier.Classify(ctx, prompt)
		if err != nil {
			failures = append(failures, summarizeSmokeText(prompt+": classify failed: "+err.Error()))
			continue
		}
		if !ok {
			failures = append(failures, summarizeSmokeText(prompt+": classifier returned no executable decision"))
			continue
		}
		if !decision.Matched() || decision.Mode != router.ModeLLM {
			failures = append(failures, summarizeSmokeText(prompt+": expected llm fallback route, got "+summarizeSmokeDecision(decision)))
			continue
		}
		if validateDecision != nil {
			if validateErr := validateDecision(decision); validateErr != nil {
				failures = append(failures, summarizeSmokeText(prompt+": "+validateErr.Error()))
				continue
			}
		}

		response, err := harness.ExecDecision(ctx, prompt, decision)
		if err != nil {
			failures = append(failures, summarizeSmokeText(prompt+": exec failed: "+err.Error()))
			continue
		}
		if isSmokeFrameworkFailure(response) {
			failures = append(failures, summarizeSmokeText(prompt+": framework failure: "+response))
			continue
		}
		if validateResponse != nil {
			if validateErr := validateResponse(response); validateErr != nil {
				failures = append(failures, summarizeSmokeText(prompt+": "+validateErr.Error()))
				continue
			}
		}

		row.Duration = time.Since(start)
		row.Summary = summarizeSmokeText(response)
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(progress, row)
		return
	}

	row.Duration = time.Since(start)
	row.Status = SmokeFail
	if len(failures) == 0 {
		row.Summary = "no fallback prompts configured"
	} else {
		row.Summary = summarizeSmokeText(strings.Join(failures, " || "))
	}
	report.Rows = append(report.Rows, row)
	writeSmokeProgressDone(progress, row)
}

func expectDecisionSkill(skillName string, expectedArgs map[string]string) func(router.Decision) error {
	return func(decision router.Decision) error {
		if decision.SkillName != skillName {
			return fmt.Errorf("expected %s skill, got %s", skillName, decision.SkillName)
		}
		for key, expected := range expectedArgs {
			if strings.TrimSpace(decision.Args[key]) != strings.TrimSpace(expected) {
				return fmt.Errorf("expected %s arg %q, got %q", key, expected, decision.Args[key])
			}
		}
		return nil
	}
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

func smokeExecCodeSnippet(runtime, token string) string {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "python", "python3":
		return "print('" + token + "')"
	case "node", "js", "javascript":
		return "console.log('" + token + "')"
	default:
		return "printf '" + token + "\\n'"
	}
}

func smokeExecCodePrompt(runtime, token string) string {
	return fmt.Sprintf("Please execute this %s snippet and show me the output: %s", runtime, smokeExecCodeSnippet(runtime, token))
}

func expectContains(fragment string) func(string) error {
	return func(response string) error {
		if !strings.Contains(response, fragment) {
			return fmt.Errorf("expected response to contain %q, got %q", fragment, summarizeSmokeText(response))
		}
		return nil
	}
}

func expectContainsAll(fragments ...string) func(string) error {
	return func(response string) error {
		for _, fragment := range fragments {
			if strings.TrimSpace(fragment) == "" {
				continue
			}
			if !strings.Contains(response, fragment) {
				return fmt.Errorf("expected response to contain %q, got %q", fragment, summarizeSmokeText(response))
			}
		}
		return nil
	}
}

func expectContainsAllFold(fragments ...string) func(string) error {
	return func(response string) error {
		responseFolded := strings.ToLower(response)
		for _, fragment := range fragments {
			fragment = strings.TrimSpace(fragment)
			if fragment == "" {
				continue
			}
			if !strings.Contains(responseFolded, strings.ToLower(fragment)) {
				return fmt.Errorf("expected response to contain %q (case-insensitive), got %q", fragment, summarizeSmokeText(response))
			}
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
var smokeWatchIDPattern = regexp.MustCompile(`(?m)^#([0-9]+)\b`)

func parseSmokeNoteID(response string) (string, error) {
	matches := smokeNoteIDPattern.FindStringSubmatch(response)
	if len(matches) != 2 {
		return "", fmt.Errorf("failed to parse note id from %q", summarizeSmokeText(response))
	}
	return matches[1], nil
}

func parseSmokeWatchID(response string) (string, error) {
	matches := smokeWatchIDPattern.FindStringSubmatch(response)
	if len(matches) != 2 {
		return "", fmt.Errorf("failed to parse watch id from %q", summarizeSmokeText(response))
	}
	return matches[1], nil
}

func addWatchRunnerCheck(
	report *SmokeReport,
	runtime app.Runtime,
	harness *Harness,
	progress io.Writer,
	ctx context.Context,
	check string,
	command string,
	validate func(string) error,
) {
	start := time.Now()
	row := SmokeRow{
		Check:   check,
		Command: command,
		Status:  SmokePass,
	}
	writeSmokeProgressStart(progress, row.Check, row.Command)
	if runtime.Watch == nil {
		row.Status = SmokeSkip
		row.Summary = "watch subsystem not configured"
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(progress, row)
		return
	}

	harness.transport.reset()
	time.Sleep(5 * time.Millisecond)
	err := runtime.Watch.RunOnce(ctx)
	response := harness.transport.output()
	harness.transport.reset()
	row.Duration = time.Since(start)
	row.Summary = summarizeSmokeText(response)

	if err != nil {
		row.Status = SmokeFail
		row.Summary = summarizeSmokeText(err.Error())
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(progress, row)
		return
	}
	if isSmokeFrameworkFailure(response) {
		row.Status = SmokeFail
		report.Rows = append(report.Rows, row)
		writeSmokeProgressDone(progress, row)
		return
	}
	if validate != nil {
		if validateErr := validate(response); validateErr != nil {
			row.Status = SmokeFail
			row.Summary = summarizeSmokeText(validateErr.Error())
		}
	}
	report.Rows = append(report.Rows, row)
	writeSmokeProgressDone(progress, row)
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

func buildSmokeLatencySummary(rows []SmokeRow, include func(SmokeRow) bool) (SmokeLatencySummary, bool) {
	durations := make([]time.Duration, 0, len(rows))
	for _, row := range rows {
		if row.Duration <= 0 {
			continue
		}
		if include != nil && !include(row) {
			continue
		}
		durations = append(durations, row.Duration)
	}
	if len(durations) == 0 {
		return SmokeLatencySummary{}, false
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	return SmokeLatencySummary{
		Count: len(durations),
		Min:   durations[0],
		P50:   smokeLatencyPercentile(durations, 0.50),
		P95:   smokeLatencyPercentile(durations, 0.95),
		Max:   durations[len(durations)-1],
	}, true
}

func smokeLatencyPercentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	if percentile >= 1 {
		return values[len(values)-1]
	}

	index := int(percentile*float64(len(values)-1) + 0.5)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func isSmokeLLMCheck(check string) bool {
	check = strings.ToLower(strings.TrimSpace(check))
	return strings.HasPrefix(check, "llm.") || strings.HasPrefix(check, "chat.")
}

func formatSmokeSlowestRows(rows []SmokeRow) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.Check+"="+formatSmokeDuration(row.Duration))
	}
	return strings.Join(parts, " | ")
}

func writeSmokeProgressStart(writer io.Writer, check, command string) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, "[RUN ] %s | %s\n", check, summarizeSmokeCommand(command))
}

func writeSmokeProgressDone(writer io.Writer, row SmokeRow) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, "[%s] %s | %s | %s\n", progressStatusLabel(row.Status), row.Check, formatSmokeDuration(row.Duration), summarizeSmokeText(row.Summary))
}

func progressStatusLabel(status SmokeStatus) string {
	switch status {
	case SmokePass:
		return "PASS"
	case SmokeFail:
		return "FAIL"
	case SmokeSkip:
		return "SKIP"
	default:
		return "INFO"
	}
}

func summarizeSmokeCommand(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(none)"
	}
	return summarizeSmokeText(value)
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
