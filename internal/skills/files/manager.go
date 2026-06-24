package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"openlight/internal/skills"
)

const defaultFilePerm = 0o644

type Entry struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
}

type ListResult struct {
	Path      string
	Entries   []Entry
	Truncated bool
}

type ReadResult struct {
	Path      string
	Content   string
	Truncated bool
}

type SearchMatch struct {
	Path    string
	Line    int
	Preview string
}

type SearchResult struct {
	Query     string
	Path      string
	Matches   []SearchMatch
	Truncated bool
}

type StatResult struct {
	Path       string
	Size       int64
	IsDir      bool
	Mode       string
	ModifiedAt time.Time
}

type WriteResult struct {
	Path    string
	Bytes   int
	Created bool
}

type ReplaceResult struct {
	Path         string
	Replacements int
}

type Manager interface {
	Enabled() bool
	Roots() []string
	List(ctx context.Context, path string) (ListResult, error)
	Read(ctx context.Context, path string) (ReadResult, error)
	Search(ctx context.Context, pattern, path string) (SearchResult, error)
	Stat(ctx context.Context, path string) (StatResult, error)
	Write(ctx context.Context, path, content string) (WriteResult, error)
	Replace(ctx context.Context, path, oldText, newText string) (ReplaceResult, error)
}

type LocalManager struct {
	enabled            bool
	roots              []string
	defaultDir         string
	maxReadBytes       int
	listLimit          int
	allowWrite         bool
	redactSecrets      bool
	allowSensitiveRead bool
}

func NewLocalManager(enabled bool, allowed []string, maxReadBytes, listLimit int, allowWrite, redactSecrets, allowSensitiveRead bool) (*LocalManager, error) {
	return NewLocalManagerWithDefault(enabled, allowed, "", maxReadBytes, listLimit, allowWrite, redactSecrets, allowSensitiveRead)
}

func NewLocalManagerWithDefault(enabled bool, allowed []string, defaultDir string, maxReadBytes, listLimit int, allowWrite, redactSecrets, allowSensitiveRead bool) (*LocalManager, error) {
	roots := make([]string, 0, len(allowed))
	for _, root := range allowed {
		normalized, err := normalizeRoot(root)
		if err != nil {
			return nil, err
		}
		if normalized == "" {
			continue
		}
		roots = append(roots, normalized)
	}

	sort.Strings(roots)

	var normDefault string
	if d := strings.TrimSpace(defaultDir); d != "" {
		expanded, err := expandPath(d)
		if err == nil {
			normDefault = filepath.Clean(expanded)
		}
	}

	return &LocalManager{
		enabled:            enabled,
		roots:              dedupeStrings(roots),
		defaultDir:         normDefault,
		maxReadBytes:       maxReadBytes,
		listLimit:          listLimit,
		allowWrite:         allowWrite,
		redactSecrets:      redactSecrets,
		allowSensitiveRead: allowSensitiveRead,
	}, nil
}

func (m *LocalManager) Enabled() bool {
	return m.enabled
}

func (m *LocalManager) Roots() []string {
	return append([]string(nil), m.roots...)
}

func (m *LocalManager) List(ctx context.Context, path string) (ListResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return ListResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}

	if strings.TrimSpace(path) == "" {
		if len(m.roots) == 0 {
			return ListResult{}, nil
		}
		if len(m.roots) > 1 {
			return ListResult{}, fmt.Errorf("%w: directory path is required", skills.ErrInvalidArguments)
		}
		path = m.roots[0]
	}

	target, err := m.resolveExistingPath(path)
	if err != nil {
		return ListResult{}, err
	}

	info, err := os.Stat(target)
	if err != nil {
		return ListResult{}, normalizePathError(path, err)
	}
	if !info.IsDir() {
		return ListResult{}, fmt.Errorf("%w: %s is not a directory", skills.ErrInvalidArguments, path)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return ListResult{}, normalizePathError(path, err)
	}

	result := make([]Entry, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		if len(result) >= m.listLimit {
			truncated = true
			break
		}

		size := int64(0)
		info, err := entry.Info()
		if err == nil && !entry.IsDir() {
			size = info.Size()
		}

		name := entry.Name()
		result = append(result, Entry{
			Name:  name,
			Path:  filepath.Join(target, name),
			IsDir: entry.IsDir(),
			Size:  size,
		})
	}

	return ListResult{
		Path:      target,
		Entries:   result,
		Truncated: truncated,
	}, nil
}

func (m *LocalManager) Read(ctx context.Context, path string) (ReadResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return ReadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}

	target, err := m.resolveExistingPath(path)
	if err != nil {
		return ReadResult{}, err
	}
	if err := m.ensureReadableFile(target); err != nil {
		return ReadResult{}, err
	}

	info, err := os.Stat(target)
	if err != nil {
		return ReadResult{}, normalizePathError(path, err)
	}
	if info.IsDir() {
		return ReadResult{}, fmt.Errorf("%w: %s is a directory", skills.ErrInvalidArguments, path)
	}

	file, err := os.Open(target)
	if err != nil {
		return ReadResult{}, normalizePathError(path, err)
	}
	defer file.Close()

	limited := io.LimitReader(file, int64(m.maxReadBytes)+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return ReadResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return ReadResult{}, fmt.Errorf("%w: binary files are not supported", skills.ErrUnavailable)
	}

	truncated := false
	if len(content) > m.maxReadBytes {
		content = content[:m.maxReadBytes]
		truncated = true
	}

	text := string(content)
	if m.redactSecrets {
		text = redactSecrets(text)
	}

	return ReadResult{
		Path:      target,
		Content:   text,
		Truncated: truncated,
	}, nil
}

func (m *LocalManager) Search(ctx context.Context, pattern, path string) (SearchResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return SearchResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}

	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return SearchResult{}, fmt.Errorf("%w: search pattern is required", skills.ErrInvalidArguments)
	}

	roots, resultPath, err := m.searchRoots(path)
	if err != nil {
		return SearchResult{}, err
	}

	loweredPattern := strings.ToLower(pattern)
	result := SearchResult{
		Query: pattern,
		Path:  resultPath,
	}

	for _, root := range roots {
		walkErr := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(result.Matches) >= m.listLimit {
				result.Truncated = true
				return fs.SkipAll
			}
			if entry.IsDir() {
				return nil
			}
			if m.redactSecrets && !m.allowSensitiveRead && isSensitivePath(current) {
				return nil
			}

			content, err := os.ReadFile(current)
			if err != nil || bytes.IndexByte(content, 0) >= 0 {
				return nil
			}

			lines := strings.Split(string(content), "\n")
			for idx, line := range lines {
				if !strings.Contains(strings.ToLower(line), loweredPattern) {
					continue
				}
				preview := line
				if m.redactSecrets {
					preview = redactSecrets(preview)
				}
				result.Matches = append(result.Matches, SearchMatch{
					Path:    current,
					Line:    idx + 1,
					Preview: truncatePreview(preview, m.maxReadBytes),
				})
				if len(result.Matches) >= m.listLimit {
					result.Truncated = true
					return fs.SkipAll
				}
			}

			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
			return SearchResult{}, fmt.Errorf("%w: %v", skills.ErrUnavailable, walkErr)
		}
		if result.Truncated {
			break
		}
	}

	return result, nil
}

func (m *LocalManager) Stat(ctx context.Context, path string) (StatResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return StatResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return StatResult{}, err
	}

	target, err := m.resolveExistingPath(path)
	if err != nil {
		return StatResult{}, err
	}

	info, err := os.Stat(target)
	if err != nil {
		return StatResult{}, normalizePathError(path, err)
	}

	return StatResult{
		Path:       target,
		Size:       info.Size(),
		IsDir:      info.IsDir(),
		Mode:       info.Mode().String(),
		ModifiedAt: info.ModTime(),
	}, nil
}

func (m *LocalManager) Write(ctx context.Context, path, content string) (WriteResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return WriteResult{}, err
	}
	if err := m.ensureWriteAllowed(); err != nil {
		return WriteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}

	target, created, perm, err := m.resolveWritablePath(path)
	if err != nil {
		return WriteResult{}, err
	}

	if err := os.WriteFile(target, []byte(content), perm); err != nil {
		return WriteResult{}, normalizePathError(path, err)
	}

	return WriteResult{
		Path:    target,
		Bytes:   len(content),
		Created: created,
	}, nil
}

func (m *LocalManager) Replace(ctx context.Context, path, oldText, newText string) (ReplaceResult, error) {
	if err := m.ensureEnabled(); err != nil {
		return ReplaceResult{}, err
	}
	if err := m.ensureWriteAllowed(); err != nil {
		return ReplaceResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReplaceResult{}, err
	}

	if strings.TrimSpace(oldText) == "" {
		return ReplaceResult{}, fmt.Errorf("%w: text to find is required", skills.ErrInvalidArguments)
	}

	target, err := m.resolveExistingPath(path)
	if err != nil {
		return ReplaceResult{}, err
	}
	if err := m.ensureReadableFile(target); err != nil {
		return ReplaceResult{}, err
	}

	info, err := os.Stat(target)
	if err != nil {
		return ReplaceResult{}, normalizePathError(path, err)
	}
	if info.IsDir() {
		return ReplaceResult{}, fmt.Errorf("%w: %s is a directory", skills.ErrInvalidArguments, path)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		return ReplaceResult{}, normalizePathError(path, err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return ReplaceResult{}, fmt.Errorf("%w: binary files are not supported", skills.ErrUnavailable)
	}

	source := string(content)
	replacements := strings.Count(source, oldText)
	if replacements == 0 {
		return ReplaceResult{}, fmt.Errorf("%w: text not found in %s", skills.ErrNotFound, path)
	}

	updated := strings.ReplaceAll(source, oldText, newText)
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = defaultFilePerm
	}

	if err := os.WriteFile(target, []byte(updated), perm); err != nil {
		return ReplaceResult{}, normalizePathError(path, err)
	}

	return ReplaceResult{
		Path:         target,
		Replacements: replacements,
	}, nil
}

func (m *LocalManager) ensureEnabled() error {
	if m.enabled {
		return nil
	}
	return skills.NewUserError(skills.ErrUnavailable, "filesystem is disabled")
}

func (m *LocalManager) ensureWriteAllowed() error {
	if m.allowWrite {
		return nil
	}
	return skills.NewUserError(skills.ErrAccessDenied, "filesystem write is disabled")
}

func (m *LocalManager) ensureReadableFile(path string) error {
	if m.redactSecrets && !m.allowSensitiveRead && isSensitivePath(path) {
		return skills.NewUserError(skills.ErrAccessDenied, "reading sensitive files is disabled")
	}
	return nil
}

func (m *LocalManager) searchRoots(path string) ([]string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if len(m.roots) == 0 {
			return nil, "", fmt.Errorf("%w: no allowed roots configured", skills.ErrNotFound)
		}
		return append([]string(nil), m.roots...), strings.Join(m.roots, ", "), nil
	}

	target, err := m.resolveExistingPath(path)
	if err != nil {
		return nil, "", err
	}

	info, err := os.Stat(target)
	if err != nil {
		return nil, "", normalizePathError(path, err)
	}
	if !info.IsDir() {
		return []string{target}, target, nil
	}
	return []string{target}, target, nil
}

func (m *LocalManager) resolveExistingPath(path string) (string, error) {
	resolved, err := resolveInputPathWithDefault(path, m.defaultDir)
	if err != nil {
		return "", err
	}

	target, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", normalizePathError(path, err)
	}
	if !m.isAllowed(target) {
		return "", fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	}
	return target, nil
}

func (m *LocalManager) resolveWritablePath(path string) (string, bool, os.FileMode, error) {
	resolved, err := resolveInputPathWithDefault(path, m.defaultDir)
	if err != nil {
		return "", false, 0, err
	}

	info, err := os.Stat(resolved)
	switch {
	case err == nil:
		target, err := filepath.EvalSymlinks(resolved)
		if err != nil {
			return "", false, 0, normalizePathError(path, err)
		}
		if !m.isAllowed(target) {
			return "", false, 0, fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
		}
		if info.IsDir() {
			return "", false, 0, fmt.Errorf("%w: %s is a directory", skills.ErrInvalidArguments, path)
		}
		perm := info.Mode().Perm()
		if perm == 0 {
			perm = defaultFilePerm
		}
		return target, false, perm, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", false, 0, normalizePathError(path, err)
	}

	parent := filepath.Dir(resolved)
	parentTarget, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", false, 0, normalizePathError(parent, err)
	}
	if !m.isAllowed(parentTarget) {
		return "", false, 0, fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	}

	target := filepath.Join(parentTarget, filepath.Base(resolved))
	if !m.isAllowed(target) {
		return "", false, 0, fmt.Errorf("%w: %s", skills.ErrAccessDenied, path)
	}

	return target, true, defaultFilePerm, nil
}

func (m *LocalManager) isAllowed(target string) bool {
	if len(m.roots) == 0 {
		return false
	}

	for _, root := range m.roots {
		if isWithinRoot(root, target) {
			return true
		}
	}
	return false
}

func isWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func normalizeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}

	expanded, err := expandPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve file root %q: %w", root, err)
	}

	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve file root %q: %w", root, err)
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	switch {
	case err == nil:
		return filepath.Clean(resolved), nil
	case errors.Is(err, os.ErrNotExist):
		return filepath.Clean(absolute), nil
	default:
		return "", fmt.Errorf("resolve file root %q: %w", root, err)
	}
}

func resolveInputPath(path string) (string, error) {
	return resolveInputPathWithDefault(path, "")
}

func resolveInputPathWithDefault(path, defaultDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: file path is required", skills.ErrInvalidArguments)
	}

	expanded, err := expandPath(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %q: %v", skills.ErrInvalidArguments, path, err)
	}

	// Relative paths are anchored to defaultDir when set.
	if !filepath.IsAbs(expanded) && defaultDir != "" {
		expanded = filepath.Join(defaultDir, expanded)
	}

	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %q: %v", skills.ErrInvalidArguments, path, err)
	}
	return filepath.Clean(absolute), nil
}

func expandPath(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return "", fmt.Errorf("unsupported home path %q", path)
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

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func truncatePreview(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max]) + "..."
}
