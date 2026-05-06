package files

import (
	"regexp"
	"strings"
)

var (
	telegramTokenPattern = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{20,}\b`)
	openAIKeyPattern     = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}\b`)
	githubTokenPattern   = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	genericSecretPattern = regexp.MustCompile(`(?i)\b(password|token|secret)\s*[:=]\s*([^\s]+)`)
)

func redactSecrets(text string) string {
	text = telegramTokenPattern.ReplaceAllString(text, "[redacted telegram token]")
	text = openAIKeyPattern.ReplaceAllString(text, "[redacted openai key]")
	text = githubTokenPattern.ReplaceAllString(text, "[redacted github token]")
	text = genericSecretPattern.ReplaceAllString(text, `$1=[redacted]`)
	return text
}

func isSensitivePath(path string) bool {
	lowered := strings.ToLower(strings.TrimSpace(path))
	base := strings.ToLower(strings.TrimSpace(filepathBase(path)))

	switch {
	case base == ".env",
		base == ".env.local",
		base == "id_rsa",
		base == "id_ed25519",
		base == "authorized_keys",
		base == "known_hosts":
		return true
	}

	return strings.Contains(lowered, "/.ssh/") ||
		strings.Contains(lowered, "private_key") ||
		strings.Contains(lowered, "token") ||
		strings.Contains(lowered, "secret")
}

func filepathBase(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
