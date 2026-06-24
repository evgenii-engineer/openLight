package skills

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// remoteSkillResponse is the wire format returned by brain's POST /skills/invoke.
type remoteSkillResponse struct {
	Text        string              `json:"text"`
	Attachments []remoteAttachment  `json:"attachments,omitempty"`
	Error       string              `json:"error,omitempty"`
}

type remoteAttachment struct {
	Filename string `json:"filename"`
	Caption  string `json:"caption"`
	Kind     string `json:"kind"`
	Data     string `json:"data"` // base64-encoded file contents
}

// RemoteInputField is the wire format for a single UI input field.
type RemoteInputField struct {
	Name        string `json:"name"`
	Prompt      string `json:"prompt"`
	Placeholder string `json:"placeholder"`
}

// RemoteSkillDefinition is the wire format returned by brain's GET /skills.
type RemoteSkillDefinition struct {
	Name        string             `json:"name"`
	Group       string             `json:"group"`
	Description string             `json:"description"`
	Aliases     []string           `json:"aliases"`
	Usage       string             `json:"usage"`
	Examples    []string           `json:"examples"`
	Mutating    bool               `json:"mutating"`
	Hidden      bool               `json:"hidden"`
	Inputs      []RemoteInputField `json:"inputs,omitempty"`
}

// RemoteSkill implements Skill by forwarding Execute to the brain node.
type RemoteSkill struct {
	def    Definition
	inputs []InputField
	client *http.Client
	url    string // {brainURL}/skills/invoke
}

// NewRemoteSkill creates a proxy skill that calls POST invokeURL for execution.
// invokeURL should be "{brainURL}/skills/invoke".
func NewRemoteSkill(rsd RemoteSkillDefinition, invokeURL string, timeout time.Duration) *RemoteSkill {
	inputs := make([]InputField, 0, len(rsd.Inputs))
	for _, f := range rsd.Inputs {
		inputs = append(inputs, InputField{
			Name:        f.Name,
			Prompt:      f.Prompt,
			Placeholder: f.Placeholder,
		})
	}
	return &RemoteSkill{
		def: Definition{
			Name:        rsd.Name,
			Group:       Group{Key: rsd.Group},
			Description: rsd.Description,
			Aliases:     rsd.Aliases,
			Usage:       rsd.Usage,
			Examples:    rsd.Examples,
			Mutating:    rsd.Mutating,
			Hidden:      rsd.Hidden,
		},
		inputs: inputs,
		client: &http.Client{Timeout: timeout},
		url:    invokeURL,
	}
}

func (s *RemoteSkill) Definition() Definition { return s.def }

// UI implements UIHinted so the Telegram input-flow prompts the user for each
// required field before executing the remote skill — identical to local skills.
func (s *RemoteSkill) UI() UIDescriptor {
	return UIDescriptor{Inputs: s.inputs}
}

func (s *RemoteSkill) Execute(ctx context.Context, input Input) (Result, error) {
	payload := map[string]any{
		"skill":    s.def.Name,
		"raw_text": input.RawText,
		"args":     input.Args,
		"user_id":  input.UserID,
		"chat_id":  input.ChatID,
		"source":   input.Source,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("remote skill %q: %w", s.def.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		if isConnErr(err) {
			return Result{Text: "Brain node is offline. Skill is temporarily unavailable."}, nil
		}
		return Result{}, NewUserError(ErrUnavailable, "brain node unreachable")
	}
	defer resp.Body.Close()

	var result remoteSkillResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Result{}, NewUserError(ErrUnavailable, "brain returned invalid response")
	}
	if result.Error != "" {
		return Result{}, NewUserError(ErrUnavailable, result.Error)
	}

	out := Result{Text: result.Text}
	for _, att := range result.Attachments {
		data, err := base64.StdEncoding.DecodeString(att.Data)
		if err != nil {
			continue
		}
		tmp, err := os.CreateTemp("", "remote-skill-*-"+att.Filename)
		if err != nil {
			continue
		}
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			continue
		}
		tmp.Close()
		out.Attachments = append(out.Attachments, Attachment{
			Path:    tmp.Name(),
			Caption: att.Caption,
			Kind:    AttachmentKind(att.Kind),
		})
	}
	return out, nil
}

// FetchRemoteSkillDefinitions calls GET {brainURL}/skills and returns the list.
// Returns nil if the brain is unreachable (edge stays functional without remote skills).
func FetchRemoteSkillDefinitions(brainURL string, timeout time.Duration) ([]RemoteSkillDefinition, error) {
	url := strings.TrimRight(strings.TrimSpace(brainURL), "/") + "/skills"
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch remote skills: %w", err)
	}
	defer resp.Body.Close()

	var defs []RemoteSkillDefinition
	if err := json.NewDecoder(resp.Body).Decode(&defs); err != nil {
		return nil, fmt.Errorf("fetch remote skills: decode: %w", err)
	}
	return defs, nil
}

func isConnErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "dial") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "no such host")
}

// RemoteSkillsBaseDir returns the temp dir where attachment files are written.
// Files are cleaned up by the OS on next reboot.
func RemoteSkillsBaseDir() string {
	return filepath.Join(os.TempDir(), "openlight-remote-skills")
}
