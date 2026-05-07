package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

	checkAuth(report, cfg)
	checkStorage(report, cfg)
	checkTelegram(report, cfg)
	checkLLM(report, cfg)
	checkServices(report, cfg)
	checkNodes(report, cfg)
	checkFiles(report, cfg)
	checkWatch(report, cfg)
	checkOptional(report, cfg)

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
	if cfg.Voice.Enabled {
		r.ok("optional:voice", cfg.Voice.Provider)
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

// ---- Helpers --------------------------------------------------------------

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
