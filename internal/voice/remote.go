package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"openlight/internal/skills"
)

// RemoteTranscriber implements Transcriber by forwarding audio to the brain node's
// POST /voice/transcribe endpoint. Used on edge nodes that have no local whisper.
type RemoteTranscriber struct {
	BrainURL string
	Timeout  time.Duration
}

func (t RemoteTranscriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return "", fmt.Errorf("%w: read audio: %v", skills.ErrUnavailable, err)
	}

	ext := "wav"
	if idx := strings.LastIndex(audioPath, "."); idx >= 0 {
		ext = audioPath[idx+1:]
	}

	payload, _ := json.Marshal(map[string]string{
		"data": base64.StdEncoding.EncodeToString(data),
		"ext":  ext,
	})

	timeout := t.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	client := &http.Client{Timeout: timeout}
	url := strings.TrimRight(t.BrainURL, "/") + "/voice/transcribe"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%w: %v", skills.ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", skills.NewUserError(skills.ErrUnavailable, "brain voice transcription unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var msg [256]byte
		n, _ := resp.Body.Read(msg[:])
		return "", skills.NewUserError(skills.ErrUnavailable,
			"brain voice transcription failed: "+strings.TrimSpace(string(msg[:n])))
	}

	var result struct {
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("%w: decode response: %v", skills.ErrUnavailable, err)
	}
	return strings.TrimSpace(result.Transcript), nil
}
