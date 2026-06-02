package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"openlight/internal/config"
)

// runDoctor validates the live configuration and probes the dependencies a
// running openlight needs. It is read-only and safe to run in production.
//
// Output is a checklist of OK / WARN / FAIL lines, one per probe. The exit
// code is non-zero only when a FAIL is observed.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report := newDoctorReport(os.Stdout)

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		report.fail("config", "load failed: "+err.Error())
		report.summary()
		return fmt.Errorf("config: %w", err)
	}
	report.ok("config", "loaded and validated")

	for _, msg := range cfg.Deprecations {
		report.warn("config:deprecated", msg)
	}
	checkAuth(report, cfg)
	checkStorage(report, cfg)
	checkTelegram(report, cfg)
	checkLLM(report, cfg)
	checkServices(report, cfg)
	checkNodes(report, cfg)
	checkFiles(report, cfg)
	checkWatch(report, cfg)
	checkOptional(report, cfg)
	checkVoice(report, cfg)
	checkSecurity(report, cfg)

	report.summary()
	if report.failures > 0 {
		return fmt.Errorf("%d check(s) failed", report.failures)
	}
	return nil
}

// ---- Probes ---------------------------------------------------------------

func checkAuth(r *doctorReport, cfg config.Config) {
	if len(cfg.Auth.AllowedUserIDs) == 0 && len(cfg.Auth.AllowedChatIDs) == 0 {
		r.fail("auth", "no allowed_user_ids or allowed_chat_ids — bot would refuse every message")
		return
	}
	r.ok("auth", fmt.Sprintf("%d user(s), %d chat(s) allowlisted",
		len(cfg.Auth.AllowedUserIDs), len(cfg.Auth.AllowedChatIDs)))
}

func checkStorage(r *doctorReport, cfg config.Config) {
	path := strings.TrimSpace(cfg.Storage.SQLitePath)
	if path == "" {
		r.fail("storage", "storage.sqlite_path is empty")
		return
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		r.fail("storage", fmt.Sprintf("data dir %q: %v", dir, err))
		return
	}
	if !info.IsDir() {
		r.fail("storage", fmt.Sprintf("%q is not a directory", dir))
		return
	}
	probe := filepath.Join(dir, ".openlight-doctor-probe")
	f, err := os.Create(probe)
	if err != nil {
		r.fail("storage", fmt.Sprintf("write probe in %q: %v", dir, err))
		return
	}
	_ = f.Close()
	_ = os.Remove(probe)
	r.ok("storage", "sqlite path writable: "+path)
}

func checkTelegram(r *doctorReport, cfg config.Config) {
	token := strings.TrimSpace(cfg.Telegram.BotToken)
	if token == "" {
		r.fail("telegram", "telegram.bot_token is empty")
		return
	}
	base := strings.TrimSpace(cfg.Telegram.APIBaseURL)
	if base == "" {
		base = "https://api.telegram.org"
	}
	url := strings.TrimRight(base, "/") + "/bot" + token + "/getMe"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.warn("telegram", "getMe network error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		r.fail("telegram", fmt.Sprintf("getMe HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	r.ok("telegram", "bot reachable ("+cfg.Telegram.Mode+")")
}

func checkLLM(r *doctorReport, cfg config.Config) {
	if !cfg.LLM.Enabled {
		r.skip("llm", "disabled in config")
		return
	}
	endpoint := strings.TrimSpace(cfg.LLM.Endpoint)
	if endpoint == "" {
		r.warn("llm", "llm.enabled but llm.endpoint is empty")
		return
	}
	provider := strings.ToLower(cfg.LLM.Provider)
	if provider == "ollama" {
		if err := probeHTTP(endpoint+"/api/tags", 3*time.Second); err != nil {
			r.fail("llm", "ollama not reachable at "+endpoint+": "+err.Error())
			return
		}
		r.ok("llm", "ollama reachable at "+endpoint+" (model: "+cfg.LLM.Model+")")
		return
	}
	if err := probeTCP(endpoint, 3*time.Second); err != nil {
		r.warn("llm", provider+" probe: "+err.Error())
		return
	}
	r.ok("llm", provider+" endpoint reachable: "+endpoint)
}

func checkServices(r *doctorReport, cfg config.Config) {
	if len(cfg.Services.Allowed) == 0 {
		r.skip("services", "no services allowlisted")
		return
	}
	r.ok("services", fmt.Sprintf("%d service target(s) allowlisted", len(cfg.Services.Allowed)))
}

func checkNodes(r *doctorReport, cfg config.Config) {
	if len(cfg.Access.Hosts) == 0 {
		r.skip("nodes", "no remote nodes configured")
		return
	}
	for name, host := range cfg.Access.Hosts {
		addr := strings.TrimSpace(host.Address)
		if addr == "" {
			r.fail("node:"+name, "address is empty")
			continue
		}
		if err := probeTCP(addr, 3*time.Second); err != nil {
			r.warn("node:"+name, "tcp "+addr+": "+err.Error())
			continue
		}
		r.ok("node:"+name, "ssh tcp reachable: "+addr+" (user: "+host.User+")")
	}
}

func checkFiles(r *doctorReport, cfg config.Config) {
	merged := cfg.Files
	if len(merged.Allowed)+len(merged.AllowedRoots) == 0 && !merged.Enabled {
		r.skip("filesystem", "disabled / no allowed roots")
		return
	}
	roots := append([]string{}, merged.Allowed...)
	roots = append(roots, merged.AllowedRoots...)
	missing := []string{}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			missing = append(missing, root)
		}
	}
	if len(missing) > 0 {
		r.warn("filesystem", "roots missing on disk: "+strings.Join(missing, ", "))
		return
	}
	r.ok("filesystem", fmt.Sprintf("%d root(s) present", len(roots)))
}

func checkWatch(r *doctorReport, cfg config.Config) {
	if !cfg.Watch.Enabled {
		r.skip("watch", "disabled")
		return
	}
	r.ok("watch", fmt.Sprintf("enabled (poll %s, ask_ttl %s)",
		cfg.Watch.PollInterval, cfg.Watch.AskTTL))
}

func checkOptional(r *doctorReport, cfg config.Config) {
	if cfg.Vision.Enabled {
		r.ok("optional:vision", cfg.Vision.Provider+" / "+cfg.Vision.Model)
	}
	if cfg.OCR.Enabled {
		r.ok("optional:ocr", cfg.OCR.Provider)
	}
	if cfg.Browser.Enabled {
		r.ok("optional:browser", cfg.Browser.HelperPath)
	}
	if cfg.Workbench.Enabled {
		r.ok("optional:workbench", cfg.Workbench.WorkspaceDir)
	}
	if cfg.VisualWatch.Enabled {
		r.ok("optional:visual_watch", "enabled")
	}
}

// checkSecurity surfaces non-fatal security smells that would otherwise be
// invisible until something breaks. None of these are config errors per se
// — config validation has already passed — but each one materially weakens
// the deployment's posture and we want operators to see them on every run.
func checkSecurity(r *doctorReport, cfg config.Config) {
	emitted := false

	for name, host := range cfg.Access.Hosts {
		if host.InsecureIgnoreHostKey {
			r.warn("security:ssh", fmt.Sprintf("node %q has insecure_ignore_host_key=true (vulnerable to MITM); set known_hosts_path instead", name))
			emitted = true
		}
		if strings.TrimSpace(host.Password) != "" {
			r.warn("security:ssh", fmt.Sprintf("node %q stores password inline; use password_env or private_key_path", name))
			emitted = true
		}
		if strings.TrimSpace(host.PrivateKeyPassphrase) != "" {
			r.warn("security:ssh", fmt.Sprintf("node %q stores private_key_passphrase inline; use private_key_passphrase_env", name))
			emitted = true
		}
	}

	if cfg.LLM.Enabled && strings.TrimSpace(cfg.LLM.APIKey) != "" {
		r.warn("security:llm", "llm.api_key is set inline; consider OPENAI_API_KEY env or a profile-scoped secret store")
		emitted = true
	}
	if cfg.Vision.Enabled && strings.TrimSpace(cfg.Vision.APIKey) != "" {
		r.warn("security:vision", "vision.api_key is set inline")
		emitted = true
	}

	if cfg.Workbench.Enabled {
		dangerous := []string{}
		for _, runtime := range cfg.Workbench.AllowedRuntimes {
			switch strings.ToLower(strings.TrimSpace(runtime)) {
			case "sh", "bash":
				dangerous = append(dangerous, runtime)
			}
		}
		if len(dangerous) > 0 {
			r.warn("security:workbench", fmt.Sprintf("shell runtimes enabled (%s) — they bypass per-language sandboxing and let any allowed user run arbitrary host commands", strings.Join(dangerous, ", ")))
			emitted = true
		}
	}

	if cfg.Browser.Enabled && cfg.Browser.AllowAllDomains {
		r.warn("security:browser", "browser.allow_all_domains=true removes the domain allowlist; consider scoping allowed_domains")
		emitted = true
	}
	if cfg.Browser.Enabled && cfg.Browser.AllowPrivateNetwork {
		r.warn("security:browser", "browser.allow_private_network=true lets the helper hit RFC1918, link-local, and loopback hosts")
		emitted = true
	}

	if !emitted {
		r.ok("security", "no obvious posture warnings")
	}
}

// checkVoice probes the Telegram voice-note transcription pipeline: the ffmpeg +
// whisper binaries and the STT model file. This is the place an operator looks
// first when voice notes sent to the bot fail to transcribe.
func checkVoice(r *doctorReport, cfg config.Config) {
	if !cfg.Voice.Enabled {
		r.skip("voice", "disabled in config")
		return
	}

	ffmpeg := firstNonEmpty(cfg.Voice.FFmpegPath, "ffmpeg")
	if path, err := resolveBinary(ffmpeg); err != nil {
		r.fail("voice:ffmpeg", ffmpeg+": "+err.Error())
	} else {
		r.ok("voice:ffmpeg", path)
	}

	whisper := firstNonEmpty(cfg.Voice.WhisperCLIPath, "whisper-cli")
	if path, err := resolveBinary(whisper); err != nil {
		r.warn("voice:whisper", whisper+": "+err.Error())
	} else {
		r.ok("voice:whisper", path)
	}

	rawModel := strings.TrimSpace(cfg.Voice.ModelPath)
	switch {
	case rawModel == "":
		r.warn("voice:model", "voice.model_path is empty; transcription will fail")
	case strings.HasPrefix(rawModel, "~"):
		// exec does not run a shell, so "~" is passed to whisper-cli literally
		// and never expands. Flag it even if the expanded file exists on disk.
		r.fail("voice:model", "model_path uses \"~\" ("+rawModel+") which is NOT expanded; use an absolute path")
	default:
		if _, err := os.Stat(rawModel); err != nil {
			r.fail("voice:model", "model not found: "+rawModel)
		} else {
			r.ok("voice:model", rawModel)
		}
	}
}

// ---- Helpers --------------------------------------------------------------

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// resolveBinary returns the absolute path of an executable. A bare name is
// looked up on PATH; a path containing a separator is stat'd directly.
func resolveBinary(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("%q is a directory", path)
		}
		return path, nil
	}
	return exec.LookPath(path)
}

func probeHTTP(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func probeTCP(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// ---- Report ---------------------------------------------------------------

type doctorReport struct {
	out      io.Writer
	failures int
	warnings int
	passes   int
	skipped  int
}

func newDoctorReport(w io.Writer) *doctorReport { return &doctorReport{out: w} }

func (r *doctorReport) ok(component, msg string) {
	r.passes++
	fmt.Fprintf(r.out, "  OK    %-22s %s\n", component, msg)
}

func (r *doctorReport) warn(component, msg string) {
	r.warnings++
	fmt.Fprintf(r.out, "  WARN  %-22s %s\n", component, msg)
}

func (r *doctorReport) fail(component, msg string) {
	r.failures++
	fmt.Fprintf(r.out, "  FAIL  %-22s %s\n", component, msg)
}

func (r *doctorReport) skip(component, msg string) {
	r.skipped++
	fmt.Fprintf(r.out, "  SKIP  %-22s %s\n", component, msg)
}

func (r *doctorReport) summary() {
	fmt.Fprintln(r.out)
	fmt.Fprintf(r.out, "%d ok, %d warn, %d fail, %d skipped\n",
		r.passes, r.warnings, r.failures, r.skipped)
}
