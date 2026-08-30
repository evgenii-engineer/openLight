package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RawStore is the durable, content-addressed archive that sits underneath
// everything else. Files land here first and are never deleted by the
// ingestion pipeline — the vector index is derived data and can always be
// rebuilt from what is stored here plus the SQLite metadata.
//
// Layout:
//
//	<root>/raw/<type>/<YYYY>/<MM>/<hash><ext>
//	<root>/raw/<type>/<YYYY>/<MM>/<hash><ext>.meta.json
//
// The sidecar duplicates the SQLite metadata row. It is cheap insurance:
// with it, a rebuild is possible even if memory.db itself is lost.
type RawStore struct {
	root string
}

// NewRawStore creates the archive directory tree under root.
func NewRawStore(root string) (*RawStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("memory: storage root is required")
	}
	for _, sub := range []string{TypeTelegram, TypeVoice, TypeImage, TypeDocument, TypeConversation} {
		if err := os.MkdirAll(filepath.Join(root, "raw", sub), 0o755); err != nil {
			return nil, fmt.Errorf("create raw storage dir: %w", err)
		}
	}
	return &RawStore{root: filepath.Clean(root)}, nil
}

// RawDir returns the top-level raw directory.
func (r *RawStore) RawDir() string { return filepath.Join(r.root, "raw") }

// Stored is the outcome of archiving one item.
type Stored struct {
	Path  string
	Hash  string
	Bytes int64
	MIME  string
}

// Put archives the item's payload and writes the metadata sidecar. The
// caller's original file is copied, not moved, so ownership stays with
// the caller (Telegram downloads clean up their own temp files).
//
// Writes are atomic: content goes to a temp file in the destination
// directory and is renamed into place, so a crash mid-write can never
// leave a half-file that a later reindex would treat as real content.
func (r *RawStore) Put(item Item) (Stored, error) {
	payload, err := r.readPayload(item)
	if err != nil {
		return Stored{}, err
	}

	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	mimeType := resolveMIME(item)
	ext := extensionFor(item, mimeType)

	created := item.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	dir := filepath.Join(r.RawDir(), sourceTypeDir(item.Type), created.Format("2006"), created.Format("01"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Stored{}, fmt.Errorf("create raw dir: %w", err)
	}
	path := filepath.Join(dir, hash+ext)

	// Identical content already archived: reuse it. This is what makes
	// re-sending the same PDF a no-op rather than a second copy on disk.
	if info, statErr := os.Stat(path); statErr == nil && info.Size() == int64(len(payload)) {
		return Stored{Path: path, Hash: hash, Bytes: info.Size(), MIME: mimeType}, nil
	}

	if err := writeFileAtomic(path, payload, 0o644); err != nil {
		return Stored{}, err
	}

	sidecar := map[string]any{
		"id":          item.ExternalID,
		"type":        sourceTypeDir(item.Type),
		"source":      item.Source,
		"title":       item.Title,
		"timestamp":   created.UTC().Format(time.RFC3339),
		"mime_type":   mimeType,
		"hash":        hash,
		"bytes":       len(payload),
		"chat_id":     item.ChatID,
		"user_id":     item.UserID,
		"metadata":    item.Metadata,
		"original":    filepath.Base(strings.TrimSpace(item.Path)),
		"stored_path": path,
	}
	if encoded, mErr := json.MarshalIndent(sidecar, "", "  "); mErr == nil {
		// A failed sidecar is not fatal — the SQLite row is authoritative
		// while the database is intact.
		_ = writeFileAtomic(path+".meta.json", encoded, 0o644)
	}

	return Stored{Path: path, Hash: hash, Bytes: int64(len(payload)), MIME: mimeType}, nil
}

// Read returns the archived bytes for a stored path.
func (r *RawStore) Read(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read raw object: %w", err)
	}
	return content, nil
}

// Bytes reports the total size of the archive. It walks the tree, so
// callers must not put it on a request path — the status snapshot caches
// the result.
func (r *RawStore) Bytes() int64 {
	var total int64
	_ = filepath.WalkDir(r.RawDir(), func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (r *RawStore) readPayload(item Item) ([]byte, error) {
	path := strings.TrimSpace(item.Path)
	text := item.Text

	switch {
	case path != "" && strings.TrimSpace(text) != "":
		return nil, fmt.Errorf("memory: item has both path and text")
	case path != "":
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open source file: %w", err)
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			return nil, fmt.Errorf("read source file: %w", err)
		}
		return content, nil
	case strings.TrimSpace(text) != "":
		return []byte(text), nil
	default:
		return nil, fmt.Errorf("memory: item has neither path nor text")
	}
}

func writeFileAtomic(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp raw file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := temp.Write(content); err != nil {
		return fmt.Errorf("write temp raw file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temp raw file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp raw file: %w", err)
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return fmt.Errorf("chmod temp raw file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename raw file: %w", err)
	}
	return nil
}

func sourceTypeDir(sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case TypeTelegram, TypeVoice, TypeImage, TypeDocument, TypeConversation:
		return strings.ToLower(strings.TrimSpace(sourceType))
	case "image", "photo":
		return TypeImage
	case "conversation", "episode":
		return TypeConversation
	case "":
		return TypeDocument
	default:
		return TypeDocument
	}
}

func resolveMIME(item Item) string {
	if declared := strings.TrimSpace(item.MIMEType); declared != "" {
		return strings.ToLower(declared)
	}
	name := strings.TrimSpace(item.Filename)
	if name == "" {
		name = strings.TrimSpace(item.Path)
	}
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if guessed := mime.TypeByExtension(ext); guessed != "" {
			return strings.ToLower(strings.SplitN(guessed, ";", 2)[0])
		}
		switch ext {
		case ".md", ".markdown":
			return "text/markdown"
		case ".json":
			return "application/json"
		case ".yaml", ".yml":
			return "text/yaml"
		case ".log", ".txt":
			return "text/plain"
		}
	}
	if strings.TrimSpace(item.Text) != "" {
		return "text/plain"
	}
	return "application/octet-stream"
}

func extensionFor(item Item, mimeType string) string {
	name := strings.TrimSpace(item.Filename)
	if name == "" {
		name = strings.TrimSpace(item.Path)
	}
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" && len(ext) <= 8 {
		return ext
	}
	switch {
	case strings.Contains(mimeType, "markdown"):
		return ".md"
	case strings.Contains(mimeType, "json"):
		return ".json"
	case strings.HasPrefix(mimeType, "text/"):
		return ".txt"
	case strings.Contains(mimeType, "pdf"):
		return ".pdf"
	case strings.HasPrefix(mimeType, "image/"):
		if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
			return exts[0]
		}
		return ".img"
	case strings.HasPrefix(mimeType, "audio/"):
		return ".ogg"
	default:
		return ".bin"
	}
}

// newID returns a random 128-bit hex identifier. Used for source, chunk,
// fact, and episode ids. Qdrant point ids must be UUIDs or unsigned
// integers, so chunk ids are rendered as UUIDs by the vector adapter.
func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not recoverable in any useful way; fall
		// back to a timestamp so ingestion still makes progress.
		return fmt.Sprintf("%032x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}
