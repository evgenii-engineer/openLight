package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"openlight/internal/config"
	"openlight/internal/logging"
	"openlight/internal/memory"
	"openlight/internal/runtime"
	"openlight/internal/utils"
)

// runMemory implements `openlight memory <subcommand>`: the diagnostic
// and maintenance surface for long-term memory.
//
// These commands open the same databases the agent uses. Reads are safe
// to run alongside a live agent (SQLite WAL). `reindex` and `retry` only
// enqueue work — the running agent's workers pick it up, and when no
// agent is running the jobs simply wait, which is the same durable
// behaviour as any other queued ingestion.
func runMemory(args []string) error {
	if len(args) == 0 {
		memoryUsage()
		return errors.New("memory: subcommand required")
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "help", "-h", "--help":
		memoryUsage()
		return nil
	case "status", "stats", "pending", "retry", "reindex", "search", "facts":
	default:
		memoryUsage()
		return fmt.Errorf("memory: unknown subcommand %q", sub)
	}

	fs := flag.NewFlagSet("memory "+sub, flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	all := fs.Bool("all", false, "reindex: rebuild the whole index from RAW storage")
	sourceID := fs.String("source", "", "reindex: rebuild a single source by id")
	failed := fs.Bool("failed", false, "reindex: re-queue only failed and skipped sources")
	limit := fs.Int("limit", 20, "search/pending/facts: maximum rows to show")
	// Go's flag package stops at the first positional argument, which
	// would make `memory search "query" --config x` silently ignore the
	// flag. Loop so flags and the query can appear in any order.
	positional, err := parseInterspersed(fs, rest)
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(positional, " "))

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		return err
	}
	if !cfg.Memory.RAG.Enabled {
		return errors.New("memory: memory.rag.enabled is false in the active config")
	}

	logger := logging.New(cfg.Log.Level)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The CLI must not start the brain server or any background worker —
	// a running agent already owns those.
	rt, err := runtime.BuildRuntimeWithOptions(ctx, cfg, logger, runtime.BuildOptions{StartBrainServer: false})
	if err != nil {
		return err
	}
	defer runtime.CloseRuntime(rt)

	if rt.Memory == nil {
		return errors.New("memory: subsystem failed to initialise; check the log above")
	}

	switch sub {
	case "status":
		return printMemoryStatus(ctx, rt.Memory, cfg)
	case "stats":
		return printMemoryStats(ctx, rt.Memory, *limit)
	case "pending":
		return printMemoryPending(ctx, rt.Memory, *limit)
	case "retry":
		return retryMemoryJobs(ctx, rt.Memory)
	case "reindex":
		return reindexMemory(ctx, rt.Memory, memory.ReindexOptions{
			All:      *all || (!*failed && strings.TrimSpace(*sourceID) == ""),
			SourceID: strings.TrimSpace(*sourceID),
			Failed:   *failed,
		})
	case "facts":
		return printMemoryFacts(ctx, rt.Memory, query, *limit)
	case "search":
		if query == "" {
			return errors.New(`memory search: a query is required, e.g. openlight memory search "какой диск"`)
		}
		return searchMemory(ctx, rt.Memory, query, *limit)
	}
	return nil
}

func printMemoryStatus(ctx context.Context, service *memory.Service, cfg config.Config) error {
	status := service.Status(ctx)

	state := "ONLINE"
	if !status.VectorOnline || !status.EmbeddingsOnline {
		state = "DEGRADED"
	}

	lines := []string{
		"Memory:      " + state,
		"Qdrant:      " + onlineText(status.VectorOnline, status.VectorError),
		"Embeddings:  " + onlineText(status.EmbeddingsOnline, status.EmbeddingsError),
		fmt.Sprintf("Model:       %s", cfg.Memory.RAG.Embeddings.Model),
		fmt.Sprintf("Collection:  %s", cfg.Memory.RAG.Vector.Collection),
		"",
		fmt.Sprintf("Sources:     %d", status.Sources),
		fmt.Sprintf("Chunks:      %d", status.Chunks),
		fmt.Sprintf("Facts:       %d", status.Facts),
		fmt.Sprintf("Episodes:    %d", status.Episodes),
		fmt.Sprintf("Pending:     %d", status.PendingJobs),
		fmt.Sprintf("Failed:      %d", status.FailedJobs),
		"",
		fmt.Sprintf("Raw storage: %s (%s)", utils.FormatBytes(uint64(status.RawBytes)), cfg.Memory.RAG.Storage.Root),
		fmt.Sprintf("DB size:     %s (%s)", utils.FormatBytes(uint64(status.DBBytes)), cfg.Memory.RAG.SQLite.Path),
		fmt.Sprintf("Vector pts:  %d", status.VectorCount),
	}
	if !status.LastIngestAt.IsZero() {
		lines = append(lines, "Last ingest: "+status.LastIngestAt.Local().Format(time.RFC3339))
	}
	if strings.TrimSpace(status.LastError) != "" {
		lines = append(lines, "Last error:  "+status.LastError)
	}

	fmt.Fprintln(os.Stdout, strings.Join(lines, "\n"))
	return nil
}

func onlineText(online bool, detail string) string {
	if online {
		return "ONLINE"
	}
	if strings.TrimSpace(detail) == "" {
		return "OFFLINE"
	}
	return "OFFLINE (" + detail + ")"
}

func printMemoryStats(ctx context.Context, service *memory.Service, limit int) error {
	store := service.Store()
	sources, err := store.ListSources(ctx, "", limit)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Fprintln(os.Stdout, "No sources archived yet.")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tTYPE\tSTATUS\tCHUNKS\tSIZE\tCREATED\tTITLE")
	for _, source := range sources {
		chunks, _ := store.ChunksBySource(ctx, source.ID)
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			shortID(source.ID),
			source.Type,
			source.Status,
			len(chunks),
			utils.FormatBytes(uint64(source.Bytes)),
			source.CreatedAt.Local().Format("2006-01-02 15:04"),
			truncateCell(source.Title, 40),
		)
	}
	return writer.Flush()
}

func printMemoryPending(ctx context.Context, service *memory.Service, limit int) error {
	jobs, err := service.Store().ListJobs(ctx, limit)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Fprintln(os.Stdout, "Queue is empty.")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "JOB\tKIND\tSTATUS\tATTEMPTS\tNEXT RETRY\tSOURCE\tLAST ERROR")
	for _, job := range jobs {
		next := "-"
		if job.Status == memory.JobPending {
			next = job.NextRetryAt.Local().Format("15:04:05")
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%d\t%s\t%s\t%s\n",
			job.ID, job.Kind, job.Status, job.Attempts, next,
			shortID(job.SourceID), truncateCell(job.LastError, 60),
		)
	}
	return writer.Flush()
}

func retryMemoryJobs(ctx context.Context, service *memory.Service) error {
	requeued, err := service.Store().RetryFailedJobs(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Re-queued %d failed job(s).\n", requeued)
	if requeued > 0 {
		fmt.Fprintln(os.Stdout, "The running agent picks them up on its next queue poll.")
	}
	return nil
}

func reindexMemory(ctx context.Context, service *memory.Service, opts memory.ReindexOptions) error {
	queued, err := service.Reindex(ctx, opts)
	if err != nil {
		return err
	}
	scope := "all sources"
	switch {
	case opts.SourceID != "":
		scope = "source " + shortID(opts.SourceID)
	case opts.Failed:
		scope = "failed sources"
	}
	fmt.Fprintf(os.Stdout, "Queued %d job(s) for reindex (%s).\n", queued, scope)
	fmt.Fprintln(os.Stdout, "Indexing runs in the agent's background workers; it is resumable across restarts.")
	fmt.Fprintln(os.Stdout, "Watch progress with: openlight memory pending")
	return nil
}

func searchMemory(ctx context.Context, service *memory.Service, query string, limit int) error {
	results, err := service.Search(ctx, query, memory.SearchOptions{
		Candidates: limit * 2,
		MaxResults: limit,
	})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stdout, "No matches.")
		return nil
	}
	for i, result := range results {
		fmt.Fprintf(os.Stdout, "%d. [%.3f] %s · %s · %s\n",
			i+1, result.Score, result.SourceType,
			firstNonEmpty(result.Title, result.Source, shortID(result.SourceID)),
			result.Timestamp.Local().Format("2006-01-02 15:04"),
		)
		fmt.Fprintf(os.Stdout, "   source_id=%s chunk_id=%s\n", shortID(result.SourceID), shortID(result.ChunkID))
		fmt.Fprintf(os.Stdout, "   %s\n\n", indentLines(truncateCell(result.Text, 400)))
	}
	return nil
}

func printMemoryFacts(ctx context.Context, service *memory.Service, query string, limit int) error {
	facts, err := service.Recall(ctx, query, limit)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		fmt.Fprintln(os.Stdout, "No current facts.")
		return nil
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSUBJECT\tPREDICATE\tVALUE\tCATEGORY\tCONF\tSINCE")
	for _, fact := range facts {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\n",
			shortID(fact.ID), fact.Subject, fact.Predicate,
			truncateCell(fact.Value, 40), fact.Category, fact.Confidence,
			fact.ValidFrom.Local().Format("2006-01-02"),
		)
	}
	return writer.Flush()
}

func memoryUsage() {
	fmt.Fprint(os.Stderr, `openlight memory — inspect and maintain long-term memory

Usage:
  openlight memory <subcommand> [flags]

Subcommands:
  status              Health of the memory subsystem and its backends
  stats               Recently archived sources and their chunk counts
  pending             Ingestion queue: pending, running, and parked jobs
  retry               Re-queue every parked (failed) job
  reindex             Rebuild the vector index from RAW storage
  search "<query>"    Run a vector search and print matches with provenance
  facts ["<query>"]   List current structured facts

Reindex flags:
  --all               Rebuild everything (default when no other flag is given)
  --source <id>       Rebuild one source
  --failed            Re-queue only failed and skipped sources

Common flags:
  --config <path>     Config file (defaults to $OPENLIGHT_CONFIG or /etc/openlight/agent.yaml)
  --limit <n>         Row limit for list-style output
`)
}

// parseInterspersed parses flags that may appear before, after, or
// between positional arguments, returning the positionals in order.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func truncateCell(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func indentLines(text string) string {
	return strings.ReplaceAll(text, "\n", "\n   ")
}
