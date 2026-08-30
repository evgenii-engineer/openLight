package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Conversation capture: turns accumulate into episodes, idle episodes are
// distilled by the smart model into a summary plus durable facts.

// RecordTurn queues one conversation turn for persistence. It never
// blocks: the write happens on a dedicated goroutine, and a full buffer
// drops the turn rather than delaying the user's reply. Losing one "ага"
// under load is strictly better than adding latency to every message.
func (s *Service) RecordTurn(chatID int64, role, text string) {
	if s == nil || !s.opts.Conversations.AutoMemory || !s.running.Load() {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" || chatID == 0 {
		return
	}
	select {
	case s.turns <- turnEvent{chatID: chatID, role: role, text: text, at: time.Now().UTC()}:
	default:
		s.logger.Warn("memory.turn.dropped", "chat_id", chatID, "reason", "buffer full")
	}
}

func (s *Service) turnWriterLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.turns:
			s.writeTurn(ctx, event)
		}
	}
}

func (s *Service) writeTurn(ctx context.Context, event turnEvent) {
	episode, err := s.deps.Store.OpenEpisode(ctx, event.chatID, newID(), event.at)
	if err != nil {
		s.logger.Warn("memory.turn.failed", "chat_id", event.chatID, "error", err)
		return
	}
	if err := s.deps.Store.AppendTurn(ctx, episode.ID, event.role, event.text, event.at); err != nil {
		s.logger.Warn("memory.turn.failed", "episode_id", episode.ID, "error", err)
		return
	}
	if episode.TurnCount+1 >= s.opts.Conversations.MaxTurns {
		// A long-running conversation gets an intermediate summary
		// instead of growing an unbounded episode.
		s.closeEpisode(ctx, episode.ID, event.at)
	}
}

func (s *Service) consolidatorLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Conversations.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.consolidateIdle(ctx)
		}
	}
}

// consolidateIdle closes episodes that have gone quiet and queues them
// for distillation. Summarisation itself runs on the ingestion workers,
// bounded by Ingestion.Workers — never inline here, and never one
// goroutine per episode.
func (s *Service) consolidateIdle(ctx context.Context) {
	now := time.Now().UTC()
	cutoff := now.Add(-s.opts.Conversations.IdleTimeout)
	episodes, err := s.deps.Store.ListIdleEpisodes(ctx, cutoff, s.opts.Conversations.MinTurns, 10)
	if err != nil {
		s.logger.Warn("memory.episode.scan_failed", "error", err)
		return
	}
	for _, episode := range episodes {
		s.closeEpisode(ctx, episode.ID, now)
	}
}

func (s *Service) closeEpisode(ctx context.Context, episodeID string, now time.Time) {
	if err := s.deps.Store.CloseEpisode(ctx, episodeID, EpisodeClosed, "", now); err != nil {
		s.logger.Warn("memory.episode.close_failed", "episode_id", episodeID, "error", err)
		return
	}
	s.logger.Debug("memory.episode.closed", "episode_id", episodeID)

	if !s.opts.Conversations.Summarize {
		return
	}
	if err := s.deps.Store.EnqueueJob(ctx, episodeID, JobSummarize); err != nil {
		s.logger.Warn("memory.episode.enqueue_failed", "episode_id", episodeID, "error", err)
	}
}

// runSummarizeJob distils one closed episode: one smart-model call
// produces both the searchable summary and the durable facts.
func (s *Service) runSummarizeJob(ctx context.Context, job Job) error {
	episodeID := job.SourceID

	turns, err := s.deps.Store.EpisodeTurns(ctx, episodeID, 200)
	if err != nil {
		return err
	}
	if len(turns) < s.opts.Conversations.MinTurns {
		_ = s.deps.Store.CloseEpisode(ctx, episodeID, EpisodeSummarized, "", time.Now().UTC())
		return nil
	}

	var transcript strings.Builder
	for _, turn := range turns {
		transcript.WriteString(turn.Role)
		transcript.WriteString(": ")
		transcript.WriteString(turn.Text)
		transcript.WriteByte('\n')
	}

	start := time.Now()
	distillation, err := Distill(ctx, s.deps.Chatter, "Telegram conversation with the openLight owner", transcript.String())
	if err != nil {
		return err
	}
	s.logger.Debug("memory.distill.duration",
		"episode_id", episodeID, "turns", len(turns), "duration_ms", time.Since(start).Milliseconds())

	if distillation.Empty() {
		_ = s.deps.Store.CloseEpisode(ctx, episodeID, EpisodeSummarized, "", time.Now().UTC())
		s.logger.Debug("memory.episode.empty", "episode_id", episodeID)
		return nil
	}

	summary, err := s.Ingest(ctx, Item{
		Type:       TypeConversation,
		Source:     "conversation:" + episodeID,
		ExternalID: episodeID,
		Title:      firstNonEmpty(distillation.Topic, "Conversation summary"),
		Text:       distillation.Text(),
		MIMEType:   "text/markdown",
		Filename:   "episode-" + episodeID + ".md",
	})
	if err != nil {
		return err
	}

	if s.opts.FactsEnabled {
		for _, extracted := range distillation.Facts {
			if _, factErr := s.Remember(ctx, Fact{
				Subject:    extracted.Subject,
				Predicate:  extracted.Predicate,
				Value:      extracted.Value,
				Category:   extracted.Category,
				Confidence: extracted.Confidence,
				SourceID:   summary.ID,
			}); factErr != nil {
				s.logger.Warn("memory.fact.failed",
					"episode_id", episodeID, "subject", extracted.Subject, "error", factErr)
			}
		}
	}

	if err := s.deps.Store.CloseEpisode(ctx, episodeID, EpisodeSummarized, summary.ID, time.Now().UTC()); err != nil {
		return err
	}
	s.logger.Info("memory.episode.summarized",
		"episode_id", episodeID, "source_id", summary.ID, "facts", len(distillation.Facts))
	return nil
}

// RememberText archives a statement the user explicitly asked to be
// remembered and queues structured-fact extraction for it.
//
// Deliberately split from Ingest's automatic path: an explicit
// "запомни X" is a much stronger signal than a passing remark, so the
// text gets its own indexed source AND a dedicated extraction pass,
// rather than waiting for the surrounding episode to go idle.
//
// The archive happens synchronously — the user is told it landed — while
// extraction is queued, so this still works with the brain node asleep.
func (s *Service) RememberText(ctx context.Context, text string, item Item) (Source, error) {
	if s == nil {
		return Source{}, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Source{}, fmt.Errorf("memory: nothing to remember")
	}

	item.Text = text
	if item.Type == "" {
		item.Type = TypeTelegram
	}
	if strings.TrimSpace(item.Title) == "" {
		item.Title = firstLine(text, 60)
	}
	if strings.TrimSpace(item.MIMEType) == "" {
		item.MIMEType = "text/plain"
	}

	source, err := s.Ingest(ctx, item)
	if err != nil {
		return Source{}, err
	}

	if s.opts.FactsEnabled && s.deps.Chatter != nil {
		if err := s.deps.Store.EnqueueJob(ctx, source.ID, JobFacts); err != nil {
			// The text is already archived and searchable; losing the
			// extraction pass is a downgrade, not a failure.
			s.logger.Warn("memory.fact.enqueue_failed", "source_id", source.ID, "error", err)
		}
	}
	return source, nil
}

// runFactsJob extracts structured facts from one explicitly remembered
// statement. Unlike the episode path it ignores the summary: the text is
// already indexed as its own source, so re-indexing a paraphrase of it
// would only add a near-duplicate chunk.
func (s *Service) runFactsJob(ctx context.Context, job Job) error {
	source, err := s.deps.Store.Source(ctx, job.SourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	content, err := s.deps.Raw.Read(source.RawPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedSource, err)
	}

	distillation, err := Distill(ctx, s.deps.Chatter,
		"A statement the owner explicitly asked openLight to remember", string(content))
	if err != nil {
		return err
	}

	for _, extracted := range distillation.Facts {
		if _, factErr := s.Remember(ctx, Fact{
			Subject:    extracted.Subject,
			Predicate:  extracted.Predicate,
			Value:      extracted.Value,
			Category:   extracted.Category,
			Confidence: extracted.Confidence,
			SourceID:   source.ID,
		}); factErr != nil {
			s.logger.Warn("memory.fact.failed", "source_id", source.ID, "error", factErr)
		}
	}
	s.logger.Info("memory.fact.extracted", "source_id", source.ID, "facts", len(distillation.Facts))
	return nil
}

func firstLine(text string, limit int) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
