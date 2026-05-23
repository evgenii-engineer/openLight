package external

import "encoding/json"

// Request is the v1 envelope sent to the skill's stdin as a single line
// of JSON. The shape is intentionally narrow — extending it requires
// bumping APIVersion so older skills cannot misinterpret new fields.
//
// `input.raw_text` is the user's original message (sans the leading
// slash command if any). `input.args` is the named-argument map the
// router or UI populated. Skills should prefer `args` over re-parsing
// `raw_text` when possible.
type Request struct {
	APIVersion string         `json:"api_version"`
	RequestID  string         `json:"request_id"`
	Skill      RequestSkill   `json:"skill"`
	Input      RequestInput   `json:"input"`
	Context    RequestContext `json:"context"`
}

// RequestSkill carries identifying metadata for the skill being invoked.
// Skills that ship multiple subcommands behind one entrypoint can switch
// on Name.
type RequestSkill struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// RequestInput carries the user message the runtime resolved for this
// invocation.
type RequestInput struct {
	RawText string            `json:"text"`
	Args    map[string]string `json:"args,omitempty"`
}

// RequestContext carries non-secret identity hints the skill may use for
// logging or per-user state. The runtime does NOT pass auth tokens or
// internal Go objects here — external skills are isolated from the
// runtime by design.
type RequestContext struct {
	UserID string `json:"user_id,omitempty"`
	ChatID string `json:"chat_id,omitempty"`
	Source string `json:"source,omitempty"`
}

// Response is the v1 envelope skills write to stdout. Exactly one JSON
// document per invocation; trailing data is ignored.
//
// `ok=false` plus a non-empty `error` is the canonical way for a skill
// to report a user-visible failure. The runtime turns it into a normal
// skill error so it appears in audit logs alongside builtin failures.
type Response struct {
	OK      bool             `json:"ok"`
	Message string           `json:"message,omitempty"`
	Error   string           `json:"error,omitempty"`
	Data    json.RawMessage  `json:"data,omitempty"`
	Buttons []ResponseButton `json:"buttons,omitempty"`
}

// ResponseButton mirrors the small subset of [telegram.Button] external
// skills can produce. Action is the callback data the runtime fires
// when the user taps the button — it is routed back through the agent
// like any other command, so a skill cannot inject arbitrary callbacks.
type ResponseButton struct {
	Text   string `json:"text"`
	Action string `json:"action"`
}
