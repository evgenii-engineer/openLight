// Package qdrant adapts a local Qdrant instance to the memory
// subsystem's vectorstore.Store interface.
//
// This is the only file in openLight that imports the Qdrant client.
// Everything else speaks vectorstore.Point / vectorstore.Hit, so
// replacing Qdrant means adding a sibling package, not editing
// retrieval or ingestion.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	qc "github.com/qdrant/go-client/qdrant"

	"openlight/internal/memory/vectorstore"
)

// Options configures the Qdrant connection.
type Options struct {
	// URL is the gRPC endpoint, e.g. "http://127.0.0.1:6334". A bare
	// "host:port" is also accepted.
	URL string

	// Collection is the collection name.
	Collection string

	// APIKey is optional; local instances usually run without one.
	APIKey string

	// Timeout bounds each individual request.
	Timeout time.Duration

	// OnDisk stores vectors on disk rather than in RAM. Default true —
	// the Pi has 8 GiB shared with everything else, and the SSD is the
	// whole point of putting memory there.
	OnDisk bool
}

// Store is a Qdrant-backed vectorstore.Store.
type Store struct {
	client     *qc.Client
	collection string
	timeout    time.Duration
	onDisk     bool
}

// New dials Qdrant. Dialling is lazy in the gRPC client, so this does
// not fail when Qdrant is down — the caller finds out on the first
// Health or Upsert, which is exactly the degraded-mode behaviour the
// ingestion queue expects.
func New(opts Options) (*Store, error) {
	host, port, err := parseEndpoint(opts.URL)
	if err != nil {
		return nil, err
	}
	collection := strings.TrimSpace(opts.Collection)
	if collection == "" {
		return nil, fmt.Errorf("qdrant: collection name is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	client, err := qc.NewClient(&qc.Config{
		Host:                   host,
		Port:                   port,
		APIKey:                 strings.TrimSpace(opts.APIKey),
		SkipCompatibilityCheck: true,
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: %w", err)
	}

	return &Store{
		client:     client,
		collection: collection,
		timeout:    timeout,
		onDisk:     opts.OnDisk,
	}, nil
}

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *Store) EnsureCollection(ctx context.Context, dimensions int) error {
	if dimensions <= 0 {
		return fmt.Errorf("qdrant: vector dimensions must be positive")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return unavailable("collection exists", err)
	}
	if exists {
		return nil
	}

	onDisk := s.onDisk
	if err := s.client.CreateCollection(ctx, &qc.CreateCollection{
		CollectionName: s.collection,
		VectorsConfig: qc.NewVectorsConfig(&qc.VectorParams{
			Size:     uint64(dimensions),
			Distance: qc.Distance_Cosine,
			OnDisk:   &onDisk,
		}),
	}); err != nil {
		// A concurrent creator (another process, a racing reindex) is
		// not an error — the collection exists either way.
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil
		}
		return unavailable("create collection", err)
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, points []vectorstore.Point) error {
	if len(points) == 0 {
		return nil
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	converted := make([]*qc.PointStruct, 0, len(points))
	for _, point := range points {
		id, err := pointID(point.ID)
		if err != nil {
			return err
		}
		converted = append(converted, &qc.PointStruct{
			Id:      id,
			Vectors: qc.NewVectors(point.Vector...),
			Payload: qc.NewValueMap(point.Payload),
		})
	}

	wait := true
	if _, err := s.client.Upsert(ctx, &qc.UpsertPoints{
		CollectionName: s.collection,
		Points:         converted,
		Wait:           &wait,
	}); err != nil {
		return unavailable("upsert", err)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, vector []float32, limit int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	if len(vector) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	request := &qc.QueryPoints{
		CollectionName: s.collection,
		Query:          qc.NewQuery(vector...),
		Limit:          ptr(uint64(limit)),
		WithPayload:    qc.NewWithPayload(true),
	}
	if len(filter.Types) > 0 {
		request.Filter = &qc.Filter{
			Must: []*qc.Condition{qc.NewMatchKeywords("source_type", filter.Types...)},
		}
	}

	scored, err := s.client.Query(ctx, request)
	if err != nil {
		return nil, unavailable("query", err)
	}

	hits := make([]vectorstore.Hit, 0, len(scored))
	for _, point := range scored {
		hits = append(hits, vectorstore.Hit{
			ID:      pointIDString(point.GetId()),
			Score:   point.GetScore(),
			Payload: decodePayload(point.GetPayload()),
		})
	}
	return hits, nil
}

func (s *Store) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	converted := make([]*qc.PointId, 0, len(ids))
	for _, id := range ids {
		pid, err := pointID(id)
		if err != nil {
			return err
		}
		converted = append(converted, pid)
	}

	wait := true
	if _, err := s.client.Delete(ctx, &qc.DeletePoints{
		CollectionName: s.collection,
		Points:         qc.NewPointsSelectorIDs(converted),
		Wait:           &wait,
	}); err != nil {
		return unavailable("delete", err)
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if err := s.client.DeleteCollection(ctx, s.collection); err != nil {
		return unavailable("delete collection", err)
	}
	return nil
}

func (s *Store) Count(ctx context.Context) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return 0, unavailable("collection exists", err)
	}
	if !exists {
		return 0, nil
	}

	exact := false
	count, err := s.client.Count(ctx, &qc.CountPoints{
		CollectionName: s.collection,
		Exact:          &exact,
	})
	if err != nil {
		return 0, unavailable("count", err)
	}
	return int64(count), nil
}

func (s *Store) Health(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if _, err := s.client.HealthCheck(ctx); err != nil {
		return unavailable("health", err)
	}
	return nil
}

func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.timeout)
}

// unavailable wraps every backend failure as vectorstore.ErrUnavailable
// so the queue's retry logic has a single sentinel to match on.
func unavailable(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: qdrant %s: %v", vectorstore.ErrUnavailable, op, err)
}

// parseEndpoint accepts "http://host:6334", "host:6334", or "host".
func parseEndpoint(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, errors.New("qdrant: url is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", 0, fmt.Errorf("qdrant: parse url %q: %w", raw, err)
	}
	host := parsed.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("qdrant: url %q has no host", raw)
	}
	portText := parsed.Port()
	if portText == "" {
		// 6334 is Qdrant's gRPC port; 6333 is REST. Defaulting to gRPC
		// matches the client we use.
		return host, 6334, nil
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		parsedPort, convErr := strconv.Atoi(portText)
		if convErr != nil {
			return "", 0, fmt.Errorf("qdrant: invalid port %q", portText)
		}
		port = parsedPort
	}
	return host, port, nil
}

// pointID renders our internal chunk id as a Qdrant point id. Qdrant
// accepts only UUIDs or unsigned integers, and our ids are 128-bit hex,
// so they map onto the UUID form exactly.
func pointID(id string) (*qc.PointId, error) {
	uuid, err := hexToUUID(id)
	if err != nil {
		return nil, err
	}
	return qc.NewIDUUID(uuid), nil
}

func pointIDString(id *qc.PointId) string {
	if id == nil {
		return ""
	}
	if uuid := id.GetUuid(); uuid != "" {
		return uuidToHex(uuid)
	}
	return strconv.FormatUint(id.GetNum(), 10)
}

func hexToUUID(id string) (string, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(cleaned) != 32 {
		return "", fmt.Errorf("qdrant: point id %q is not a 128-bit hex id", id)
	}
	for _, r := range cleaned {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", fmt.Errorf("qdrant: point id %q is not hexadecimal", id)
		}
	}
	return strings.ToLower(cleaned[0:8] + "-" + cleaned[8:12] + "-" + cleaned[12:16] + "-" + cleaned[16:20] + "-" + cleaned[20:32]), nil
}

func uuidToHex(uuid string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(uuid), "-", ""))
}

// decodePayload converts Qdrant's Value union into plain Go types. Only
// the shapes we actually write (string, int, double, bool) are handled;
// anything else degrades to its string form rather than failing the
// search.
func decodePayload(payload map[string]*qc.Value) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		if value == nil {
			continue
		}
		switch {
		case value.GetStringValue() != "":
			out[key] = value.GetStringValue()
		case value.GetIntegerValue() != 0:
			out[key] = value.GetIntegerValue()
		case value.GetDoubleValue() != 0:
			out[key] = value.GetDoubleValue()
		case value.GetBoolValue():
			out[key] = true
		default:
			out[key] = value.String()
		}
	}
	return out
}

func ptr[T any](value T) *T { return &value }
