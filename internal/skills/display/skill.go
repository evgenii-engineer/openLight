// Package display provides a skill that shows a temporary overlay message on
// the local framebuffer display (MHS35 / /dev/fb0 on Raspberry Pi).
package display

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"openlight/internal/skills"
)

const (
	MessageFile   = "/tmp/openlight-display-msg.json"
	DefaultExpiry = 30 * time.Second
)

// Message is the JSON written to MessageFile.
type Message struct {
	Text      string `json:"text"`
	ExpiresAt int64  `json:"expires_at"` // unix seconds
}

// WriteMessage writes msg to MessageFile and signals the display process.
func WriteMessage(text string, ttl time.Duration) error {
	m := Message{
		Text:      text,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(MessageFile, data, 0644); err != nil {
		return err
	}
	signalDisplay()
	return nil
}

// ReadActiveMessage returns the current message if it hasn't expired yet.
func ReadActiveMessage() (string, bool) {
	data, err := os.ReadFile(MessageFile)
	if err != nil {
		return "", false
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return "", false
	}
	if time.Now().Unix() > m.ExpiresAt {
		_ = os.Remove(MessageFile)
		return "", false
	}
	return m.Text, true
}

// signalDisplay sends SIGUSR1 to openlight-display so it redraws immediately.
func signalDisplay() {
	out, err := exec.Command("systemctl", "show", "openlight-display",
		"--property=MainPID", "--value").Output()
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Signal(os.Signal(sigUSR1()))
}

type displaySkill struct{}

func New() skills.Skill { return &displaySkill{} }

func (s *displaySkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "text", Prompt: "What to show on the screen?", Placeholder: "Привет ❤️"},
		},
	}
}

func (s *displaySkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "display_message",
		Group:       skills.Group{Key: "system"},
		Description: "Show a custom message on the Pi's LCD screen for 10 minutes.",
		Aliases:     []string{"display message", "show on screen", "screen message", "show message"},
		Usage:       "/display_message <text>",
		Examples:    []string{"display_message Привет!", "show on screen Я тебя люблю ❤️"},
		Mutating:    false,
	}
}

func (s *displaySkill) Execute(_ context.Context, input skills.Input) (skills.Result, error) {
	text := strings.TrimSpace(input.Args["text"])
	if text == "" {
		text = strings.TrimSpace(input.RawText)
		for _, pfx := range []string{"/display_message", "display_message", "show on screen", "screen message", "show message"} {
			text = strings.TrimPrefix(text, pfx)
		}
		text = strings.TrimSpace(text)
	}
	if text == "" {
		return skills.Result{}, fmt.Errorf("text is required")
	}

	if err := WriteMessage(text, DefaultExpiry); err != nil {
		return skills.Result{}, fmt.Errorf("write display message: %w", err)
	}
	return skills.Result{
		Text: fmt.Sprintf("Message shown on display for 30 seconds:\n\"%s\"", text),
	}, nil
}
