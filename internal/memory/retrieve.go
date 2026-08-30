package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"openlight/internal/memory/vectorstore"
)

// The read path: vector search, the assembled memory context, the
// structured-fact surface, and reindexing.

// Search runs the vector half of retrieval and joins the hits back to
// their chunk text and source provenance.
func (s *Service) Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error) {
	if s == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if opts.Candidates <= 0 {
		opts.Candidates = s.opts.Retrieval.Candidates
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = s.opts.Retrieval.MaxResults
	}

	start := time.Now()

	vectors, err := s.deps.Embedder.Embed(ctx, []string{query})
	if err != nil || len(vectors) == 0 {
		if err == nil {
			err = fmt.Errorf("%w: query embedding returned nothing", ErrEmbeddingsUnavailable)
		}
		s.recordError(err)
		return nil, err
	}

	hits, err := s.deps.Vectors.Search(ctx, vectors[0], opts.Candidates, vectorstore.Filter{Types: opts.Types})
	if err != nil {
		s.recordError(err)
		return nil, err
	}
	if len(hits) == 0 {
		s.logger.Debug("memory.search.results", "query_tokens", EstimateTokens(query), "results", 0,
			"duration_ms", time.Since(start).Milliseconds())
		return nil, nil
	}

	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	chunks, err := s.deps.Store.ChunksByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	sources := map[string]Source{}
	results := make([]Result, 0, len(hits))
	for _, hit := range hits {
		chunk, ok := chunks[hit.ID]
		if !ok {
			// The index has a point SQLite does not know about — a stale
			// leftover from a partial reindex. Skip it; the next full
			// reindex clears it.
			continue
		}
		source, ok := sources[chunk.SourceID]
		if !ok {
			loaded, srcErr := s.deps.Store.Source(ctx, chunk.SourceID)
			if srcErr != nil {
				continue
			}
			sources[chunk.SourceID] = loaded
			source = loaded
		}
		score := float64(hit.Score)
		if opts.ChatID != 0 && source.ChatID == opts.ChatID {
			// Same-chat provenance is a weak but real relevance signal.
			score += 0.02
		}
		results = append(results, Result{
			ChunkID:    chunk.ID,
			SourceID:   source.ID,
			SourceType: source.Type,
			Source:     source.Source,
			ExternalID: source.ExternalID,
			Title:      source.Title,
			Path:       source.RawPath,
			Text:       chunk.Text,
			Score:      score,
			Timestamp:  source.CreatedAt,
			Metadata:   chunk.Metadata,
		})
	}

	s.logger.Debug("memory.search.duration", "duration_ms", time.Since(start).Milliseconds())
	s.logger.Debug("memory.search.results", "results", len(results), "candidates", len(hits))
	return results, nil
}

// ContextFor is the full read pipeline used before a smart-model call:
// gate → structured facts + vector search → rank/dedup → context
// builder. It never returns an error to the caller's detriment: a
// degraded memory yields an empty context and the agent answers as it
// always did.
func (s *Service) ContextFor(ctx context.Context, chatID int64, query string) MemoryContext {
	if s == nil {
		return MemoryContext{}
	}
	if !ShouldRetrieve(s.opts.Retrieval.Mode, query) {
		return MemoryContext{}
	}

	var facts []Fact
	if s.opts.FactsEnabled {
		terms := factQueryTerms(query, 6)
		found, err := s.deps.Store.SearchFacts(ctx, terms, s.opts.Retrieval.MaxFacts*2)
		if err != nil {
			s.logger.Warn("memory.recall.failed", "error", err)
		} else {
			facts = found
		}
	}

	results, err := s.Search(ctx, query, SearchOptions{
		Candidates: s.opts.Retrieval.Candidates,
		MaxResults: s.opts.Retrieval.MaxResults,
		ChatID:     chatID,
	})
	if err != nil {
		// Degraded, not broken: structured facts alone may still answer.
		s.logger.Warn("memory.search.failed", "error", err)
	}

	built := BuildContext(facts, results, ContextOptions{
		MaxResults: s.opts.Retrieval.MaxResults,
		MaxTokens:  s.opts.Retrieval.MaxContextTokens,
		MaxFacts:   s.opts.Retrieval.MaxFacts,
	})
	if !built.Empty() {
		s.logger.Debug("memory.context.built",
			"facts", len(built.Facts), "chunks", len(built.Results),
			"tokens", built.Tokens, "dropped", built.Dropped)
	}
	return built
}

// Remember stores a structured fact.
func (s *Service) Remember(ctx context.Context, fact Fact) (Fact, error) {
	if s == nil {
		return Fact{}, nil
	}
	stored, superseded, err := RememberFact(ctx, s.deps.Store, fact)
	if err != nil {
		return Fact{}, err
	}
	if superseded {
		s.logger.Info("memory.fact.superseded",
			"subject", stored.Subject, "predicate", stored.Predicate, "value", stored.Value)
	} else {
		s.logger.Info("memory.fact.created",
			"subject", stored.Subject, "predicate", stored.Predicate,
			"value", stored.Value, "category", stored.Category)
	}
	return stored, nil
}

// Recall returns current structured facts matching the query.
func (s *Service) Recall(ctx context.Context, query string, limit int) ([]Fact, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = s.opts.Retrieval.MaxFacts
	}
	if strings.TrimSpace(query) == "" {
		return s.deps.Store.ListFacts(ctx, limit)
	}
	return s.deps.Store.SearchFacts(ctx, factQueryTerms(query, 6), limit)
}

// Forget closes a fact or removes a source from the index. RAW archives
// are never touched — "forget" means "stop retrieving this", not
// "destroy the evidence".
func (s *Service) Forget(ctx context.Context, id string) error {
	if s == nil {
		return ErrNotFound
	}
	if err := s.deps.Store.ForgetFact(ctx, id); err == nil {
		s.logger.Info("memory.fact.forgotten", "fact_id", id)
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	source, err := s.deps.Store.Source(ctx, id)
	if err != nil {
		return err
	}
	chunks, err := s.deps.Store.ChunksBySource(ctx, source.ID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
	}
	if err := s.deps.Vectors.Delete(ctx, ids); err != nil && !errors.Is(err, vectorstore.ErrUnavailable) {
		return err
	}
	if err := s.deps.Store.DeleteSource(ctx, source.ID); err != nil {
		return err
	}
	s.logger.Info("memory.source.forgotten", "source_id", source.ID, "chunks", len(ids))
	return nil
}

// Reindex rebuilds the vector index from RAW storage plus SQLite
// metadata. The work itself is done by the normal ingestion queue, so
// the pass is resumable across restarts and never blocks the agent.
func (s *Service) Reindex(ctx context.Context, opts ReindexOptions) (int, error) {
	if s == nil {
		return 0, nil
	}
	switch {
	case strings.TrimSpace(opts.SourceID) != "":
		if _, err := s.deps.Store.Source(ctx, opts.SourceID); err != nil {
			return 0, err
		}
		if err := s.deps.Store.SetSourceStatus(ctx, opts.SourceID, StatusPending); err != nil {
			return 0, err
		}
		if err := s.deps.Store.EnqueueJob(ctx, opts.SourceID, JobIngest); err != nil {
			return 0, err
		}
		s.logger.Info("memory.reindex.queued", "scope", "source", "source_id", opts.SourceID)
		return 1, nil

	case opts.Failed:
		requeued, err := s.deps.Store.RetryFailedJobs(ctx)
		if err != nil {
			return 0, err
		}
		queued := int(requeued)
		for _, status := range []string{StatusFailed, StatusSkipped} {
			sources, listErr := s.deps.Store.ListSources(ctx, status, 1000)
			if listErr != nil {
				return queued, listErr
			}
			for _, source := range sources {
				if err := s.deps.Store.SetSourceStatus(ctx, source.ID, StatusPending); err != nil {
					return queued, err
				}
				if err := s.deps.Store.EnqueueJob(ctx, source.ID, JobIngest); err != nil {
					return queued, err
				}
				queued++
			}
		}
		s.logger.Info("memory.reindex.queued", "scope", "failed", "jobs", queued)
		return queued, nil

	default:
		// Full rebuild: drop the collection so stale points from deleted
		// sources cannot survive, then re-queue everything.
		if err := s.deps.Vectors.DeleteCollection(ctx); err != nil && !errors.Is(err, vectorstore.ErrUnavailable) {
			return 0, err
		}
		if dimensions, err := s.vectorDimensions(ctx); err == nil {
			if err := s.deps.Vectors.EnsureCollection(ctx, dimensions); err != nil && !errors.Is(err, vectorstore.ErrUnavailable) {
				return 0, err
			}
		}

		sources, err := s.deps.Store.ListSources(ctx, "", 100000)
		if err != nil {
			return 0, err
		}
		queued := 0
		for _, source := range sources {
			if err := s.deps.Store.SetSourceStatus(ctx, source.ID, StatusPending); err != nil {
				return queued, err
			}
			if err := s.deps.Store.EnqueueJob(ctx, source.ID, JobIngest); err != nil {
				return queued, err
			}
			queued++
		}
		s.logger.Info("memory.reindex.queued", "scope", "all", "jobs", queued)
		return queued, nil
	}
}
