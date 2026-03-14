package workbench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"openlight/internal/skills"
)

var execCommandContext = exec.CommandContext

type RunResult struct {
	Path      string
	Runtime   string
	Output    string
	Truncated bool
}

type CleanResult struct {
	Workspace string
	Removed   int
}

type ExecutionError struct {
	Result   RunResult
	ExitCode int
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.ExitCode)
}

type Manager interface {
	Workspace() string
	AllowedRuntimes() []string
	AllowedFiles() []string
	ExecCode(ctx context.Context, runtime, code string) (RunResult, error)
	ExecFile(ctx context.Context, path string) (RunResult, error)
	CleanWorkspace(ctx context.Context) (CleanResult, error)
}

type LocalManager struct {
	workspace          string
	allowedRuntimes    map[string]runtimeSpec
	allowedRuntimeList []string
	allowedFiles       map[string]struct{}
	allowedFileList    []string
	maxOutputBytes     int
}

type runtimeSpec struct {
	Name      string
	Command   string
	Extension string
}

var runtimeCatalog = map[string]runtimeSpec{
	"python": {Name: "python", Command: "python3", Extension: ".py"},
	"sh":     {Name: "sh", Command: "sh", Extension: ".sh"},
	"bash":   {Name: "bash", Command: "bash", Extension: ".sh"},
	"node":   {Name: "node", Command: "node", Extension: ".js"},
}

func NewLocalManager(workspace string, allowedRuntimes, allowedFiles []string, maxOutputBytes int) (*LocalManager, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workbench workspace is required")
	}
	if maxOutputBytes <= 0 {
		return nil, fmt.Errorf("workbench max output bytes must be greater than zero")
	}

	normalizedWorkspace, err := normalizeAllowedPath(workspace)
	if err != nil {
		return nil, fmt.Errorf("normalize workbench workspace: %w", err)
	}

	runtimes := make(map[string]runtimeSpec)
	runtimeList := make([]string, 0, len(allowedRuntimes))
	for _, runtime := range allowedRuntimes {
		spec, ok := lookupRuntime(runtime)
		if !ok {
			return nil, fmt.Errorf("unsupported workbench runtime %q", runtime)
		}
		if _, exists := runtimes[spec.Name]; exists {
			continue
		}
		runtimes[spec.Name] = spec
		runtimeList = append(runtimeList, spec.Name)
	}
	sort.Strings(runtimeList)

	allowedFileSet := make(map[string]struct{})
	allowedFileList := make([]string, 0, len(allowedFiles))
	for _, path := range allowedFiles {
		normalized, err := normalizeAllowedPath(path)
		if err != nil {
			return nil, fmt.Errorf("normalize workbench allowed file %q: %w", path, err)
		}
		if normalized == "" {
			continue
		}
		if _, exists := allowedFileSet[normalized]; exists {
			continue
		}
		allowedFileSet[normalized] = struct{}{}
		allowedFileList = append(allowedFileList, normalized)
	}
	sort.Strings(allowedFileList)

	return &LocalManager{
		workspace:          normalizedWorkspace,
		allowedRuntimes:    runtimes,
		allowedRuntimeList: runtimeList,
		allowedFiles:       allowedFileSet,
		allowedFileList:    allowedFileList,
		maxOutputBytes:     maxOutputBytes,
	}, nil
}

func (m *LocalManager) Workspace() string {
	return m.workspace
}

func (m *LocalManager) AllowedRuntimes() []string {
	return append([]string(nil), m.allowedRuntimeList...)
}

func (m *LocalManager) AllowedFiles() []string {
	return append([]string(nil), m.allowedFileList...)
}

func (m *LocalManager) ExecCode(ctx context.Context, runtime, code string) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}

	spec, ok := lookupRuntime(runtime)
	if !ok {
		return RunResult{}, fmt.Errorf("%w: unsupported runtime %q", skills.ErrInvalidArguments, strings.TrimSpace(runtime))
	}
	if _, allowed := m.allowedRuntimes[spec.Name]; !allowed {
		return RunResult{}, fmt.Errorf("%w: runtime %s", skills.ErrAccessDenied, spec.Name)
	}
	if strings.TrimSpace(code) == "" {
		return RunResult{}, fmt.Errorf("%w: code is required", skills.ErrInvalidArguments)
	}
	if err := m.ensureWorkspace(); err != nil {
		return RunResult{}, err
	}

	file, err := os.CreateTemp(m.workspace, "run-*"+spec.Extension)
	if err != nil {
		return RunResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	path := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		return RunResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, closeErr)
	}

	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		return RunResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}

	return m.runCommand(ctx, []string{spec.Command, path}, path, spec.Name)
}

func (m *LocalManager) ExecFile(ctx context.Context, path string) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}

	target, err := m.resolveAllowedFile(path)
	if err != nil {
		return RunResult{}, err
	}

	command, runtimeName, err := commandForFile(target)
	if err != nil {
		return RunResult{}, err
	}

	return m.runCommand(ctx, command, target, runtimeName)
}

func (m *LocalManager) CleanWorkspace(ctx context.Context) (CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return CleanResult{}, err
	}
	if err := m.ensureWorkspace(); err != nil {
		return CleanResult{}, err
	}

	entries, err := os.ReadDir(m.workspace)
	if err != nil {
		return CleanResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}

	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return CleanResult{}, err
		}
		if err := os.RemoveAll(filepath.Join(m.workspace, entry.Name())); err != nil {
			return CleanResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
		}
		removed++
	}

	return CleanResult{
		Workspace: m.workspace,
		Removed:   removed,
	}, nil
}

func (m *LocalManager) ensureWorkspace() error {
	if err := os.MkdirAll(m.workspace, 0o755); err != nil {
		return fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	return nil
}

func (m *LocalManager) resolveAllowedFile(path string) (string, error) {
	target, err := resolveInputPath(path)
	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", normalizePathError(path, err)
	}
	resolved = filepath.Clean(resolved)

	if _, ok := m.allowedFiles[resolved]; !ok {
		return "", fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", normalizePathError(path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: %s is a directory", skills.ErrInvalidArguments, path)
	}

	return resolved, nil
}

func (m *LocalManager) runCommand(ctx context.Context, command []string, path, runtimeName string) (RunResult, error) {
	if len(command) == 0 {
		return RunResult{}, fmt.Errorf("%w: command is required", skills.ErrInvalidArguments)
	}

	var output limitedBuffer
	output.limit = m.maxOutputBytes

	cmd := execCommandContext(ctx, command[0], command[1:]...)
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return RunResult{}, ctx.Err()
	}

	result := RunResult{
		Path:      path,
		Runtime:   runtimeName,
		Output:    strings.TrimRight(output.String(), "\n"),
		Truncated: output.truncated,
	}

	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, &ExecutionError{
			Result:   result,
			ExitCode: exitErr.ExitCode(),
		}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return RunResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	if errors.Is(err, os.ErrPermission) {
		return RunResult{}, fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	}
	return RunResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
}

func lookupRuntime(value string) (runtimeSpec, bool) {
	switch normalizeRuntimeName(value) {
	case "python", "python3":
		return runtimeCatalog["python"], true
	case "sh", "shell":
		return runtimeCatalog["sh"], true
	case "bash":
		return runtimeCatalog["bash"], true
	case "node", "javascript", "js":
		return runtimeCatalog["node"], true
	default:
		return runtimeSpec{}, false
	}
}

func normalizeRuntimeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func commandForFile(path string) ([]string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", normalizePathError(path, err)
	}

	if info.Mode()&0o111 != 0 {
		return []string{path}, "file", nil
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".sh":
		return []string{"sh", path}, "sh", nil
	case ".py":
		return []string{"python3", path}, "python", nil
	case ".js":
		return []string{"node", path}, "node", nil
	default:
		return nil, "", fmt.Errorf("%w: %s is not executable", skills.ErrUnavailable, path)
	}
}

func normalizeAllowedPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	switch {
	case err == nil:
		return filepath.Clean(resolved), nil
	case errors.Is(err, os.ErrNotExist):
		return filepath.Clean(absolute), nil
	default:
		return "", err
	}
}

func resolveInputPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: file path is required", skills.ErrInvalidArguments)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %q: %v", skills.ErrInvalidArguments, path, err)
	}
	return filepath.Clean(absolute), nil
}

func normalizePathError(path string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %s", skills.ErrNotFound, path)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	default:
		return fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(p), nil
	}

	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}

	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}

	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
