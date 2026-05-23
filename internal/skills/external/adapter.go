package external

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"openlight/internal/skills"
	"openlight/internal/telegram"
)

// adapter is the [skills.Skill] implementation backed by a subprocess
// described by [Manifest]. One adapter is registered per skill; each
// invocation spawns a fresh process, writes a single JSON request to
// stdin, reads a single JSON response from stdout, and exits.
//
// The runtime owns:
//   - the per-invocation timeout (kills the process on expiry),
//   - JSON validation (rejects malformed stdout),
//   - stderr capture (logged, never parsed as protocol output),
//   - process cleanup (we always Wait so we don't leak zombies).
//
// The skill itself does NOT touch openLight's runtime APIs — its only
// channel to the agent is the response envelope.
type adapter struct {
	manifest Manifest
	logger   *slog.Logger
	runner   processRunner
}

// processRunner is the seam tests use to avoid spawning real processes.
// In production it is [execRunner] which calls [exec.CommandContext].
type processRunner interface {
	run(ctx context.Context, cmd []string, env []string, dir string, stdin []byte) (stdout []byte, stderr []byte, err error)
}

// newAdapter constructs a registered adapter. The logger is annotated
// with the skill name so audit lines stay grep-friendly.
func newAdapter(manifest Manifest, logger *slog.Logger, runner processRunner) *adapter {
	if logger == nil {
		logger = slog.Default()
	}
	if runner == nil {
		runner = execRunner{}
	}
	return &adapter{
		manifest: manifest,
		logger:   logger.With("component", "external-skill", "skill", manifest.Name),
		runner:   runner,
	}
}

// Definition exposes the manifest to the registry. Most fields map 1:1
// onto [skills.Definition]; Group falls back to GroupOther when the
// manifest names an unknown group.
func (a *adapter) Definition() skills.Definition {
	return skills.Definition{
		Name:        a.manifest.Name,
		Group:       resolveGroup(a.manifest.Group),
		Description: a.manifest.Description,
		Aliases:     a.manifest.Aliases,
		Usage:       a.manifest.usage(),
		Examples:    a.manifest.Examples,
		Mutating:    a.manifest.Mutating,
		Hidden:      a.manifest.Hidden,
	}
}

// Execute runs the skill subprocess for one user request. The function
// never panics — runner failures, decode errors, and skill-reported
// failures all surface as ordinary Go errors so the agent's existing
// error rendering handles them.
func (a *adapter) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	req := Request{
		APIVersion: APIVersion,
		RequestID:  newRequestID(),
		Skill: RequestSkill{
			Name:    a.manifest.Name,
			Version: a.manifest.Version,
		},
		Input: RequestInput{
			RawText: input.RawText,
			Args:    input.Args,
		},
		Context: RequestContext{
			UserID: formatID(input.UserID),
			ChatID: formatID(input.ChatID),
			Source: input.Source,
		},
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return skills.Result{}, fmt.Errorf("external skill %q: encode request: %w", a.manifest.Name, err)
	}
	// Frame as a single line so skills that read with `gets`/`readline`
	// stop at the boundary without needing length headers.
	payload = append(payload, '\n')

	execCtx, cancel := context.WithTimeout(ctx, a.manifest.Timeout)
	defer cancel()

	started := time.Now()
	stdout, stderr, runErr := a.runner.run(execCtx, a.manifest.CommandLine(), a.manifest.EnvSlice(), a.manifest.Dir, payload)
	elapsed := time.Since(started)

	// Always emit the audit line, even on failure: operators care about
	// stderr from a crashed skill as much as from a successful one.
	logStderr(a.logger, stderr)

	if runErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			a.logger.Warn("external skill timed out",
				"request_id", req.RequestID,
				"timeout", a.manifest.Timeout.String(),
				"elapsed_ms", elapsed.Milliseconds(),
			)
			return skills.Result{}, skills.NewUserError(
				fmt.Errorf("%w: external skill %q timed out", skills.ErrUnavailable, a.manifest.Name),
				fmt.Sprintf("Skill %q did not respond within %s.", a.manifest.Name, a.manifest.Timeout),
			)
		}
		a.logger.Warn("external skill execution failed",
			"request_id", req.RequestID,
			"elapsed_ms", elapsed.Milliseconds(),
			"error", runErr,
		)
		return skills.Result{}, fmt.Errorf("external skill %q: %w", a.manifest.Name, runErr)
	}

	resp, err := decodeResponse(stdout)
	if err != nil {
		a.logger.Warn("external skill returned invalid response",
			"request_id", req.RequestID,
			"elapsed_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return skills.Result{}, fmt.Errorf("external skill %q: %w", a.manifest.Name, err)
	}

	a.logger.Info("external skill ok",
		"request_id", req.RequestID,
		"elapsed_ms", elapsed.Milliseconds(),
		"ok", resp.OK,
	)

	if !resp.OK {
		message := strings.TrimSpace(resp.Error)
		if message == "" {
			message = strings.TrimSpace(resp.Message)
		}
		if message == "" {
			message = "skill reported failure"
		}
		return skills.Result{}, skills.NewUserError(
			fmt.Errorf("%w: %s", skills.ErrUnavailable, message),
			message,
		)
	}

	return skills.Result{
		Text:    strings.TrimSpace(resp.Message),
		Buttons: buttonsFromResponse(resp.Buttons),
	}, nil
}

func (m Manifest) usage() string {
	// Default to `/name` so /help has something to render. Authors can
	// override by setting a custom example in their manifest; the
	// `usage` field on Definition is intentionally minimal — anything
	// fancier belongs in `examples`.
	return "/" + m.Name
}

// decodeResponse rejects empty output and trailing garbage so a broken
// skill never produces a "success" response by accident.
func decodeResponse(stdout []byte) (Response, error) {
	stdout = bytes.TrimSpace(stdout)
	if len(stdout) == 0 {
		return Response{}, errors.New("empty response")
	}
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

func buttonsFromResponse(in []ResponseButton) [][]telegram.Button {
	if len(in) == 0 {
		return nil
	}
	// One row of buttons is enough for v1; richer layouts can come
	// later. Telegram squashes wider rows anyway on narrow screens.
	row := make([]telegram.Button, 0, len(in))
	for _, b := range in {
		text := strings.TrimSpace(b.Text)
		action := strings.TrimSpace(b.Action)
		if text == "" || action == "" {
			continue
		}
		row = append(row, telegram.Button{Text: text, CallbackData: action})
	}
	if len(row) == 0 {
		return nil
	}
	return [][]telegram.Button{row}
}

func logStderr(logger *slog.Logger, stderr []byte) {
	for _, line := range bytes.Split(bytes.TrimRight(stderr, "\n"), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		logger.Info("external skill stderr", "line", string(line))
	}
}

func formatID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// newRequestID produces an opaque, sortable request identifier so each
// invocation can be correlated across logs. Falls back to a timestamp
// if crypto/rand is unavailable, which should never happen on the
// platforms we support but keeps the failure path harmless.
func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "req_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "req_" + hex.EncodeToString(buf[:])
}

// execRunner is the production [processRunner]. It runs the command
// synchronously, feeds stdin, and captures stdout+stderr separately so
// stderr can never poison the protocol.
type execRunner struct{}

func (execRunner) run(ctx context.Context, command []string, env []string, dir string, stdin []byte) ([]byte, []byte, error) {
	if len(command) == 0 {
		return nil, nil, errors.New("external skill: empty command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		// Subprocesses inherit the parent's PATH etc. and add the
		// manifest-declared env on top, so authors don't need to
		// re-export PATH=... themselves.
		cmd.Env = append(append([]string(nil), parentEnv()...), env...)
	}

	cmd.Stdin = bytes.NewReader(stdin)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), err
}

// parentEnv is split out so tests can stub it if they need to assert
// on env propagation. Production reads the live os.Environ once per
// invocation; that is cheap and avoids cached-environment surprises if
// the operator rotates secrets between calls.
var parentEnv = func() []string {
	return os.Environ()
}
