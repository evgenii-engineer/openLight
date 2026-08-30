package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"openlight/internal/config"
	"openlight/internal/core"
	basellm "openlight/internal/llm"
	"openlight/internal/memory"
	"openlight/internal/memory/vectorstore"
	qdrantstore "openlight/internal/memory/vectorstore/qdrant"
	memoryskills "openlight/internal/skills/memory"
	ocrskills "openlight/internal/skills/ocr"

	systemskills "openlight/internal/skills/system"
	visionskills "openlight/internal/skills/vision"
)

// TranscriberHolder lets the agent entrypoint plug the voice transcriber
// into the memory subsystem after the runtime is built. Same indirection
// pattern as TelegramHealthHolder: the transcriber's construction
// depends on node role and lives in cmd, while memory's extractor
// registry is assembled here.
//
// It only matters for reindexing archived audio — on the normal path the
// agent hands the transcript to memory as metadata, so no second whisper
// run happens.
type TranscriberHolder struct {
	mu         sync.RWMutex
	transcribe memory.Transcriber
}

func NewTranscriberHolder() *TranscriberHolder { return &TranscriberHolder{} }

func (h *TranscriberHolder) Bind(transcribe memory.Transcriber) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.transcribe = transcribe
	h.mu.Unlock()
}

// Transcribe satisfies memory.Transcriber; it reports unavailable until
// something is bound.
func (h *TranscriberHolder) Transcribe(ctx context.Context, path string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("%w: no transcriber bound", memory.ErrUnsupportedSource)
	}
	h.mu.RLock()
	transcribe := h.transcribe
	h.mu.RUnlock()
	if transcribe == nil {
		return "", fmt.Errorf("%w: no transcriber bound", memory.ErrUnsupportedSource)
	}
	return transcribe(ctx, path)
}

// buildMemory constructs the long-term memory subsystem, or returns
// (nil, nil) when it is disabled.
//
// Nothing here touches the network. An unreachable Qdrant or brain node
// must degrade memory, never block agent startup — that is the whole
// point of a memory that the agent can run without.
func buildMemory(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	visionManager visionskills.Manager,
	ocrManager ocrskills.Manager,
	transcriber memory.Transcriber,
	chatter memory.Chatter,
) (*memory.Service, error) {
	rag := cfg.Memory.RAG
	if !rag.Enabled {
		return nil, nil
	}

	memLogger := logger.With("component", "memory")

	raw, err := memory.NewRawStore(rag.Storage.Root)
	if err != nil {
		return nil, fmt.Errorf("memory: raw storage: %w", err)
	}

	store, err := memory.OpenStore(ctx, rag.SQLite.Path)
	if err != nil {
		return nil, fmt.Errorf("memory: sqlite: %w", err)
	}

	vectors := buildVectorStore(rag, memLogger)
	embedder := buildEmbedder(cfg, memLogger)

	extractors := buildExtractors(rag, raw, visionManager, ocrManager, transcriber)

	service, err := memory.New(memory.Options{
		Root:       rag.Storage.Root,
		DBPath:     rag.SQLite.Path,
		Collection: rag.Vector.Collection,
		Chunking: memory.ChunkOptions{
			TargetTokens:  rag.Chunking.TargetTokens,
			OverlapTokens: rag.Chunking.OverlapTokens,
		},
		Retrieval: memory.RetrievalOptions{
			Mode:             memory.ParseRetrievalMode(rag.Retrieval.Mode),
			Candidates:       rag.Retrieval.Candidates,
			MaxResults:       rag.Retrieval.MaxResults,
			MaxContextTokens: rag.Retrieval.MaxContextTokens,
			MaxFacts:         rag.Retrieval.MaxFacts,
		},
		Ingestion: memory.IngestionOptions{
			Workers:          rag.Ingestion.Workers,
			PollInterval:     rag.Ingestion.PollInterval,
			RetryBase:        rag.Ingestion.RetryBase,
			RetryMaxInterval: rag.Ingestion.RetryMaxInterval,
			MaxAttempts:      rag.Ingestion.MaxAttempts,
		},
		Conversations: memory.ConversationOptions{
			AutoMemory:    rag.Conversations.AutoMemory,
			Summarize:     rag.Conversations.Summarize && chatter != nil,
			IdleTimeout:   rag.Conversations.IdleTimeout,
			MaxTurns:      rag.Conversations.MaxTurns,
			MinTurns:      rag.Conversations.MinTurns,
			CheckInterval: rag.Conversations.CheckInterval,
		},
		FactsEnabled: rag.Facts.Enabled,
		// Self-provisioning: pull the embedding model and create the
		// collection on first start, so a fresh Pi needs no manual
		// `ollama pull` or collection setup.
		AutoProvision: rag.Embeddings.ShouldAutoPull(),
	}, memory.Deps{
		Store:      store,
		Raw:        raw,
		Vectors:    vectors,
		Embedder:   embedder,
		Extractors: extractors,
		Chatter:    chatter,
		Logger:     memLogger,
	})
	if err != nil {
		_ = store.Close()
		_ = vectors.Close()
		return nil, err
	}

	memLogger.Info("memory.enabled",
		"root", rag.Storage.Root,
		"db", rag.SQLite.Path,
		"vector", rag.Vector.Provider,
		"collection", rag.Vector.Collection,
		"embeddings_model", rag.Embeddings.Model,
		"retrieval_mode", rag.Retrieval.Mode,
	)
	return service, nil
}

// buildEmbedder picks how this node computes vectors.
//
// On an edge node the answer is "through the brain API": Ollama binds to
// loopback on the brain, so :11434 is simply not reachable from here,
// and opening it to the tailnet to serve embeddings would widen the
// brain's exposure for nothing. That is the same route LLM inference and
// whisper already take.
func buildEmbedder(cfg config.Config, logger *slog.Logger) memory.Embedder {
	embeddings := cfg.Memory.RAG.Embeddings

	if embeddings.EmbeddingsViaBrain() {
		logger.Info("memory.embeddings.remote",
			"brain_url", cfg.Node.BrainURL, "model", embeddings.Model)
		return memory.NewRemoteEmbedder(memory.RemoteEmbedderOptions{
			BrainURL: cfg.Node.BrainURL,
			Model:    embeddings.Model,
			Batch:    embeddings.Batch,
			Timeout:  embeddings.Timeout,
		})
	}

	logger.Info("memory.embeddings.local", "endpoint", embeddings.URL, "model", embeddings.Model)
	return memory.NewOllamaEmbedder(memory.OllamaEmbedderOptions{
		Endpoint:  embeddings.URL,
		Model:     embeddings.Model,
		KeepAlive: embeddings.KeepAlive,
		Batch:     embeddings.Batch,
		Timeout:   embeddings.Timeout,
	})
}

// BuildBrainEmbedder constructs the embedder a brain node serves to edge
// nodes over /embed.
//
// Deliberately independent of memory.rag.enabled: a brain node usually
// does not run memory itself — it just lends its GPU to the edge node
// that does. Gating this on the brain's own memory setting would leave
// the edge permanently unable to index.
// Returns the concrete type: the brain API needs EnsureModel, which is
// a provisioning concern and deliberately not part of memory.Embedder.
func BuildBrainEmbedder(cfg config.Config, logger *slog.Logger) *memory.OllamaEmbedder {
	embeddings := cfg.Memory.RAG.Embeddings

	// The brain always embeds locally. config normalisation already
	// points embeddings.URL at this node's own Ollama; the fallbacks
	// below only matter when the brain is itself configured to route
	// embeddings elsewhere, which is not a normal setup.
	endpoint := strings.TrimSpace(embeddings.URL)
	if endpoint == "" || embeddings.EmbeddingsViaBrain() {
		endpoint = strings.TrimSpace(cfg.LLM.Endpoint)
	}
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}

	model := strings.TrimSpace(embeddings.Model)
	if model == "" {
		model = "bge-m3"
	}

	logger.Info("memory.embeddings.serving", "endpoint", endpoint, "model", model)
	return memory.NewOllamaEmbedder(memory.OllamaEmbedderOptions{
		Endpoint:  endpoint,
		Model:     model,
		KeepAlive: embeddings.KeepAlive,
		Batch:     embeddings.Batch,
		Timeout:   embeddings.Timeout,
	})
}

// buildVectorStore returns a Qdrant-backed store, or a Noop when the
// provider is "none" or construction fails. A bad vector config
// degrades memory to archive-and-queue rather than failing startup.
func buildVectorStore(rag config.MemoryRAGConfig, logger *slog.Logger) vectorstore.Store {
	if !strings.EqualFold(strings.TrimSpace(rag.Vector.Provider), "qdrant") {
		logger.Info("memory.vector.disabled", "provider", rag.Vector.Provider)
		return vectorstore.Noop{}
	}
	onDisk := true
	if rag.Vector.OnDisk != nil {
		onDisk = *rag.Vector.OnDisk
	}
	store, err := qdrantstore.New(qdrantstore.Options{
		URL:        rag.Vector.URL,
		Collection: rag.Vector.Collection,
		APIKey:     rag.Vector.APIKey,
		Timeout:    rag.Vector.Timeout,
		OnDisk:     onDisk,
	})
	if err != nil {
		logger.Warn("memory.vector.unavailable", "url", rag.Vector.URL, "error", err)
		return vectorstore.Noop{}
	}
	return store
}

// buildExtractors assembles the extraction chain. Order matters: the
// specific handlers claim their MIME types first, and TextExtractor is
// last because it accepts an empty MIME type as a catch-all.
func buildExtractors(
	rag config.MemoryRAGConfig,
	raw *memory.RawStore,
	visionManager visionskills.Manager,
	ocrManager ocrskills.Manager,
	transcriber memory.Transcriber,
) memory.Extractors {
	reader := raw.Read

	imageExtractor := memory.ImageExtractor{}
	if rag.Extraction.Vision && visionManager != nil && visionManager.Enabled() {
		imageExtractor.Describe = func(ctx context.Context, path, prompt string) (string, error) {
			result, err := visionManager.Analyze(ctx, path, prompt)
			if err != nil {
				return "", err
			}
			return result.Description, nil
		}
	}
	if rag.Extraction.OCR && ocrManager != nil && ocrManager.Enabled() {
		imageExtractor.OCR = func(ctx context.Context, path string) (string, error) {
			result, err := ocrManager.Extract(ctx, path)
			if err != nil {
				return "", err
			}
			return result.Text, nil
		}
	}

	audioExtractor := memory.AudioExtractor{}
	if rag.Extraction.Voice && transcriber != nil {
		audioExtractor.Transcribe = transcriber
	}

	return memory.Extractors{
		memory.PDFExtractor{BinaryPath: rag.Extraction.PDFToTextPath, Reader: reader},
		imageExtractor,
		audioExtractor,
		memory.TextExtractor{Reader: reader, MaxBytes: rag.Extraction.MaxTextBytes},
	}
}

// --- adapters ------------------------------------------------------------

// memoryChatter adapts an LLM provider to the narrow Chat-only
// interface the memory subsystem asks for.
type memoryChatter struct {
	provider basellm.Provider
}

// distillNumPredict is the output budget for one distillation.
//
// The provider's chat default is 64 tokens, sized for a short Telegram
// reply — far too small for the JSON object distillation asks for, which
// silently truncated mid-object and failed to parse. A summary plus a
// handful of facts fits comfortably in this.
const distillNumPredict = 640

// distillNumCtx widens the window for this one call. The smart profile
// runs at a small context to keep VRAM free on the Mac mini; the prompt
// plus the JSON answer does not fit in it, and Ollama would silently
// drop the head of the prompt to make room.
const distillNumCtx = 4096

func (c memoryChatter) Chat(ctx context.Context, messages []memory.ChatMessage) (string, error) {
	converted := make([]basellm.ChatMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, basellm.ChatMessage{Role: message.Role, Content: message.Content})
	}
	return basellm.ChatWith(ctx, c.provider, converted, basellm.ChatOptions{
		NumPredict: distillNumPredict,
		NumCtx:     distillNumCtx,
	})
}

// memoryPromptAdapter satisfies chat.MemoryProvider.
type memoryPromptAdapter struct {
	service *memory.Service
	timeout time.Duration
}

// MemoryPrompt runs retrieval under its own deadline. Retrieval is an
// enhancement: if it cannot finish quickly the answer goes out without
// it rather than waiting.
func (a memoryPromptAdapter) MemoryPrompt(ctx context.Context, chatID int64, query string) string {
	if a.service == nil {
		return ""
	}
	timeout := a.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	retrieveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return a.service.ContextFor(retrieveCtx, chatID, query).Prompt()
}

// coreMemoryAdapter satisfies core.Memory.
type coreMemoryAdapter struct {
	service *memory.Service
}

func (a coreMemoryAdapter) RecordTurn(chatID int64, role, text string) {
	a.service.RecordTurn(chatID, role, text)
}

func (a coreMemoryAdapter) IngestFile(ctx context.Context, file core.MemoryFile) (core.MemoryReceipt, error) {
	source, err := a.service.Ingest(ctx, memory.Item{
		Type:       file.Kind,
		Source:     file.Source,
		ExternalID: file.ExternalID,
		Title:      file.Title,
		Path:       file.Path,
		MIMEType:   file.MIMEType,
		Filename:   file.FileName,
		ChatID:     file.ChatID,
		UserID:     file.UserID,
		Metadata:   file.Metadata,
	})
	if err != nil {
		return core.MemoryReceipt{}, err
	}
	// "Duplicate" here means "already indexed", not merely "already
	// archived". A duplicate whose earlier ingestion never finished is
	// reported as new, because it genuinely is about to be indexed.
	return core.MemoryReceipt{
		SourceID:  source.ID,
		Duplicate: source.Status == memory.StatusCompleted,
	}, nil
}

// NewCoreMemory adapts a memory service to the agent's Memory
// interface. Returns nil when memory is disabled, which the agent reads
// as "no memory" and behaves exactly as it did before.
func NewCoreMemory(service *memory.Service) core.Memory {
	if service == nil {
		return nil
	}
	return coreMemoryAdapter{service: service}
}

// longTermAdapter satisfies memoryskills.LongTerm, bridging the manual
// /remember command into the automatic subsystem.
type longTermAdapter struct {
	service *memory.Service
}

func (a longTermAdapter) RememberText(ctx context.Context, text, source string, chatID, userID int64) error {
	_, err := a.service.RememberText(ctx, text, memory.Item{
		Type:   memory.TypeTelegram,
		Source: firstNonEmpty(source, "telegram"),
		ChatID: chatID,
		UserID: userID,
		Metadata: map[string]string{
			// Marks this as an explicit request rather than something
			// picked up in passing — useful when auditing where a fact
			// came from.
			"explicit": "true",
		},
	})
	return err
}

func (a longTermAdapter) RememberFact(ctx context.Context, subject, predicate, value string) error {
	_, err := a.service.Remember(ctx, memory.Fact{
		Subject:   subject,
		Predicate: predicate,
		Value:     value,
		// A fact the owner typed by hand is worth more than one the
		// model inferred from a passing remark.
		//
		// Category is left unset on purpose: the structured form has no
		// model in the loop to classify with, and guessing from the key
		// would be worse than the honest "other" that normalisation
		// applies.
		Confidence: 0.95,
	})
	return err
}

// NewLongTermMemory adapts the memory service for the /remember skill.
// Returns nil when memory is disabled, which the skill reads as "keep
// the previous note-only behaviour".
func NewLongTermMemory(service *memory.Service) memoryskills.LongTerm {
	if service == nil {
		return nil
	}
	return longTermAdapter{service: service}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// memoryStatusHook renders the /status Memory block from the cached
// snapshot. Cheap by construction — see memory.Service.Status.
func memoryStatusHook(service *memory.Service) func(ctx context.Context) systemskills.MemoryStatusInfo {
	if service == nil {
		return nil
	}
	return func(ctx context.Context) systemskills.MemoryStatusInfo {
		snapshot := service.Status(ctx)
		return systemskills.MemoryStatusInfo{
			Enabled:          snapshot.Enabled,
			VectorOnline:     snapshot.VectorOnline,
			EmbeddingsOnline: snapshot.EmbeddingsOnline,
			Sources:          snapshot.Sources,
			Chunks:           snapshot.Chunks,
			Facts:            snapshot.Facts,
			QueueDepth:       snapshot.PendingJobs,
			FailedJobs:       snapshot.FailedJobs,
			RawBytes:         snapshot.RawBytes,
			Error:            snapshot.LastError,
		}
	}
}
