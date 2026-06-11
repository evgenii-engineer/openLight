// Package brain implements the HTTP API server that the brain node exposes
// to edge nodes. Edge nodes forward all LLM requests here; the brain node
// runs the local inference and returns structured responses.
//
// Wire protocol for /llm/generate is task-based JSON, identical to the
// format openlight/internal/llm.HTTPProvider sends. This means edge nodes
// can use HTTPProvider (via RemoteLLMProvider) without a custom protocol.
package brain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"openlight/internal/llm"
	"openlight/internal/skills"
	"openlight/internal/voice"
)

// Server is the brain node's HTTP API. It wraps a local LLM provider and
// exposes it over HTTP so edge nodes can forward inference requests.
type Server struct {
	provider     llm.Provider // smart (default)
	fastProvider llm.Provider // fast profile; falls back to provider when nil
	deepProvider llm.Provider // deep profile (think=true); falls back to provider when nil
	registry     *skills.Registry
	transcriber  voice.Transcriber
	listenAddr   string
	nodeID       string
	model        string
	fastModel    string
	deepModel    string
	logger       *slog.Logger
	startTime    time.Time
	httpServer   *http.Server
	// last observed inference latency per profile, stored as microseconds (atomic int64)
	smartLatencyUs atomic.Int64
	fastLatencyUs  atomic.Int64
	deepLatencyUs  atomic.Int64
}

// NewServer creates a brain API server. provider must be the brain node's
// local LLM provider (Ollama, OpenAI, etc.). listenAddr is the TCP address
// to bind (e.g. ":8787"). nodeID and model are metadata returned by /health.
func NewServer(provider llm.Provider, listenAddr, nodeID, model string, logger *slog.Logger) *Server {
	s := &Server{
		provider:   provider,
		listenAddr: listenAddr,
		nodeID:     nodeID,
		model:      model,
		logger:     logger,
		startTime:  time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /system", s.handleSystem)
	mux.HandleFunc("GET /network/status", s.handleNetworkStatus)
	mux.HandleFunc("GET /skills", s.handleSkillsList)
	mux.HandleFunc("POST /llm/generate", s.handleLLMGenerate)
	mux.HandleFunc("POST /chat", s.handleChat)
	mux.HandleFunc("POST /skills/invoke", s.handleSkillsInvoke)
	mux.HandleFunc("POST /voice/transcribe", s.handleVoiceTranscribe)
	mux.HandleFunc("POST /tasks", s.handleTasks)

	s.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}
	return s
}

// Start begins listening and blocks until the context is cancelled or an
// error occurs. It shuts down gracefully when ctx is done.
//
// SO_REUSEPORT is set on the socket so rapid restarts (launchd, systemd)
// don't hit "address already in use" before the OS clears the old socket.
func (s *Server) Start(ctx context.Context) error {
	lc := newListenConfig()
	ln, err := lc.Listen(ctx, "tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("brain server listen: %w", err)
	}

	s.logger.Info("brain API server starting", "addr", s.listenAddr, "node_id", s.nodeID)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutCtx)
	}()
	if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("brain server: %w", err)
	}
	return nil
}

// handleHealth returns basic brain node status. Edge nodes poll this to
// determine whether the brain is reachable and which model is active.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"status":   "ok",
		"node_id":  s.nodeID,
		"role":     "brain",
		"model":    s.model,
		"uptime_s": int64(time.Since(s.startTime).Seconds()),
	}
	if s.fastModel != "" {
		resp["fast_model"] = s.fastModel
	}
	if s.deepModel != "" {
		resp["deep_model"] = s.deepModel
	}
	if us := s.smartLatencyUs.Load(); us > 0 {
		resp["smart_latency_ms"] = float64(us) / 1000.0
	}
	if us := s.fastLatencyUs.Load(); us > 0 {
		resp["fast_latency_ms"] = float64(us) / 1000.0
	}
	if us := s.deepLatencyUs.Load(); us > 0 {
		resp["deep_latency_ms"] = float64(us) / 1000.0
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSystem returns host resource metrics (CPU, memory, uptime).
// Polled by edge-node dashboards to display brain node stats.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	stats := CollectStats()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

// handleNetworkStatus returns the known network topology. Extend as needed.
func (s *Server) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"brain": map[string]any{
			"node_id": s.nodeID,
			"role":    "brain",
			"status":  "online",
			"model":   s.model,
		},
	})
}

// handleLLMGenerate proxies task-based LLM requests from edge nodes to the
// local provider. The request format is identical to what HTTPProvider sends:
//
//	{"task": "route"|"skill"|"summarize"|"chat", ...task-specific fields}
//
// Responses are the JSON-serialised structs that HTTPProvider deserialises.
func (s *Server) handleLLMGenerate(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	var task, profile string
	if raw, ok := body["task"]; ok {
		_ = json.Unmarshal(raw, &task)
	}
	if raw, ok := body["profile"]; ok {
		_ = json.Unmarshal(raw, &profile)
	}

	provider := s.resolveProvider(profile)
	if provider == nil {
		http.Error(w, "LLM provider not configured on this brain node", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)

	t0 := time.Now()
	var callErr error

	switch task {
	case "route":
		var text string
		var req llm.RouteClassificationRequest
		if raw, ok := body["text"]; ok {
			_ = json.Unmarshal(raw, &text)
		}
		if raw, ok := body["groups"]; ok {
			_ = json.Unmarshal(raw, &req.Groups)
		}
		if raw, ok := body["input_chars"]; ok {
			_ = json.Unmarshal(raw, &req.InputChars)
		}
		if raw, ok := body["num_predict"]; ok {
			_ = json.Unmarshal(raw, &req.NumPredict)
		}
		result, err := provider.ClassifyRoute(ctx, text, req)
		callErr = err
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = enc.Encode(result)

	case "skill":
		var text string
		var req llm.SkillClassificationRequest
		if raw, ok := body["text"]; ok {
			_ = json.Unmarshal(raw, &text)
		}
		if raw, ok := body["skills"]; ok {
			_ = json.Unmarshal(raw, &req.AllowedSkills)
		}
		if raw, ok := body["allowed_services"]; ok {
			_ = json.Unmarshal(raw, &req.AllowedServices)
		}
		if raw, ok := body["allowed_runtimes"]; ok {
			_ = json.Unmarshal(raw, &req.AllowedRuntimes)
		}
		if raw, ok := body["candidate_skills"]; ok {
			_ = json.Unmarshal(raw, &req.CandidateSkills)
		}
		if raw, ok := body["input_chars"]; ok {
			_ = json.Unmarshal(raw, &req.InputChars)
		}
		if raw, ok := body["num_predict"]; ok {
			_ = json.Unmarshal(raw, &req.NumPredict)
		}
		result, err := provider.ClassifySkill(ctx, text, req)
		callErr = err
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = enc.Encode(result)

	case "summarize":
		var text string
		if raw, ok := body["text"]; ok {
			_ = json.Unmarshal(raw, &text)
		}
		result, err := provider.Summarize(ctx, text)
		callErr = err
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = enc.Encode(map[string]string{"summary": result})

	case "chat":
		var messages []llm.ChatMessage
		if raw, ok := body["messages"]; ok {
			_ = json.Unmarshal(raw, &messages)
		}
		result, err := provider.Chat(ctx, messages)
		callErr = err
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = enc.Encode(map[string]string{"response": result})

	default:
		http.Error(w, "unknown task: "+task, http.StatusBadRequest)
		return
	}

	if callErr == nil {
		us := time.Since(t0).Microseconds()
		switch profile {
		case "fast":
			s.fastLatencyUs.Store(us)
		case "deep":
			s.deepLatencyUs.Store(us)
		default:
			s.smartLatencyUs.Store(us)
		}
	}
}

// handleChat is a higher-level endpoint for direct chat requests. Accepts
// either {"messages":[...]} or {"text":"..."} shorthand.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		http.Error(w, "LLM provider not configured on this brain node", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Messages []llm.ChatMessage `json:"messages"`
		Text     string            `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	messages := req.Messages
	if len(messages) == 0 && req.Text != "" {
		messages = []llm.ChatMessage{{Role: "user", Content: req.Text}}
	}
	if len(messages) == 0 {
		http.Error(w, "messages or text required", http.StatusBadRequest)
		return
	}

	result, err := s.provider.Chat(r.Context(), messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"response": result})
}

// handleTasks is a placeholder for future task scheduling support.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// SetRegistry wires the skill registry into the brain server so edge nodes can
// discover and invoke brain-only skills. Call this after BuildRegistryWithWatch.
func (s *Server) SetRegistry(r *skills.Registry) {
	s.registry = r
}

// SetFastProvider wires the fast LLM provider. When set, requests with
// "profile":"fast" are routed here; all others use the smart provider.
func (s *Server) SetFastProvider(p llm.Provider) {
	s.fastProvider = p
}

// SetFastModel stores the fast model name for /health responses.
func (s *Server) SetFastModel(name string) {
	s.fastModel = name
}

// SetDeepProvider wires the deep LLM provider (think=true). When set,
// requests with "profile":"deep" are routed here for reasoning-intensive
// tasks (coding, planning, architecture).
func (s *Server) SetDeepProvider(p llm.Provider) {
	s.deepProvider = p
}

// SetDeepModel stores the deep model name for /health responses.
func (s *Server) SetDeepModel(name string) {
	s.deepModel = name
}

// resolveProvider returns the appropriate provider for the given profile.
// "fast" → fastProvider (falls back to smart), "deep" → deepProvider
// (falls back to smart), everything else → smart provider.
func (s *Server) resolveProvider(profile string) llm.Provider {
	switch profile {
	case "fast":
		if s.fastProvider != nil {
			return s.fastProvider
		}
	case "deep":
		if s.deepProvider != nil {
			return s.deepProvider
		}
	}
	return s.provider
}

// handleSkillsList returns all non-hidden skill definitions from the local
// registry so edge nodes can register remote stubs for them.
func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	defs := s.registry.List()
	type wireSkill struct {
		Name        string   `json:"name"`
		Group       string   `json:"group"`
		Description string   `json:"description"`
		Aliases     []string `json:"aliases"`
		Usage       string   `json:"usage"`
		Examples    []string `json:"examples"`
		Mutating    bool     `json:"mutating"`
		Hidden      bool     `json:"hidden"`
	}
	out := make([]wireSkill, 0, len(defs))
	for _, d := range defs {
		if d.Hidden {
			continue
		}
		out = append(out, wireSkill{
			Name:        d.Name,
			Group:       d.Group.Key,
			Description: d.Description,
			Aliases:     d.Aliases,
			Usage:       d.Usage,
			Examples:    d.Examples,
			Mutating:    d.Mutating,
			Hidden:      d.Hidden,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleSkillsInvoke executes a skill locally and returns the result as JSON.
// Attachment files are base64-encoded inline so edge nodes can re-materialise them.
func (s *Server) handleSkillsInvoke(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		http.Error(w, "skill registry not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Skill   string            `json:"skill"`
		RawText string            `json:"raw_text"`
		Args    map[string]string `json:"args"`
		UserID  int64             `json:"user_id"`
		ChatID  int64             `json:"chat_id"`
		Source  string            `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	skill, ok := s.registry.ResolveIdentifier(req.Skill)
	if !ok {
		http.Error(w, "skill not found: "+req.Skill, http.StatusNotFound)
		return
	}

	result, err := skill.Execute(r.Context(), skills.Input{
		RawText: req.RawText,
		Args:    req.Args,
		UserID:  req.UserID,
		ChatID:  req.ChatID,
		Source:  req.Source,
	})

	type wireAttachment struct {
		Filename string `json:"filename"`
		Caption  string `json:"caption"`
		Kind     string `json:"kind"`
		Data     string `json:"data"`
	}
	type wireResult struct {
		Text        string           `json:"text"`
		Attachments []wireAttachment `json:"attachments,omitempty"`
		Error       string           `json:"error,omitempty"`
	}

	out := wireResult{}
	if err != nil {
		out.Error = err.Error()
	} else {
		out.Text = result.Text
		for _, att := range result.Attachments {
			data, readErr := os.ReadFile(att.Path)
			if readErr != nil {
				s.logger.Warn("brain: reading attachment", "path", att.Path, "err", readErr)
				continue
			}
			out.Attachments = append(out.Attachments, wireAttachment{
				Filename: att.Path[len(att.Path)-min(32, len(att.Path)):],
				Caption:  att.Caption,
				Kind:     string(att.Kind),
				Data:     base64.StdEncoding.EncodeToString(data),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// SetTranscriber wires the voice transcriber so edge nodes can forward audio.
func (s *Server) SetTranscriber(t voice.Transcriber) {
	s.transcriber = t
}

// handleVoiceTranscribe accepts a base64-encoded audio file from an edge node,
// writes it to a temp file, transcribes it, and returns the transcript as JSON.
func (s *Server) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.transcriber == nil {
		http.Error(w, "voice transcription not configured on this brain node", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Data string `json:"data"` // base64-encoded audio (WAV preferred)
		Ext  string `json:"ext"`  // file extension hint, e.g. "wav" or "ogg"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	audioData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64: "+err.Error(), http.StatusBadRequest)
		return
	}
	ext := req.Ext
	if ext == "" {
		ext = "wav"
	}
	tmp, err := os.CreateTemp("", "brain-voice-*."+ext)
	if err != nil {
		http.Error(w, "temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(audioData); err != nil {
		tmp.Close()
		http.Error(w, "write temp: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmp.Close()

	transcript, err := s.transcriber.Transcribe(r.Context(), tmpPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"transcript": transcript})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
