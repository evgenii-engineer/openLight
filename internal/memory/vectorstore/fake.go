package vectorstore

import (
	"context"
	"math"
	"sort"
	"sync"
)

// Fake is an in-memory Store used by tests and by `--dry-run` style
// tooling. It implements exact cosine search over its points, so tests
// can assert on real ranking behaviour without a Qdrant instance.
//
// Failure injection: set Down to make every operation report
// ErrUnavailable, which is how the graceful-degradation tests simulate a
// Qdrant outage.
type Fake struct {
	mu         sync.Mutex
	points     map[string]Point
	dimensions int
	created    bool

	// Down makes all operations fail with ErrUnavailable.
	Down bool
}

func NewFake() *Fake {
	return &Fake{points: map[string]Point{}}
}

func (f *Fake) EnsureCollection(_ context.Context, dimensions int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrUnavailable
	}
	f.dimensions = dimensions
	f.created = true
	return nil
}

func (f *Fake) Upsert(_ context.Context, points []Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrUnavailable
	}
	if f.points == nil {
		f.points = map[string]Point{}
	}
	for _, point := range points {
		f.points[point.ID] = point
	}
	return nil
}

func (f *Fake) Search(_ context.Context, vector []float32, limit int, filter Filter) ([]Hit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return nil, ErrUnavailable
	}

	allowed := map[string]struct{}{}
	for _, t := range filter.Types {
		allowed[t] = struct{}{}
	}

	hits := make([]Hit, 0, len(f.points))
	for _, point := range f.points {
		if len(allowed) > 0 {
			sourceType, _ := point.Payload["source_type"].(string)
			if _, ok := allowed[sourceType]; !ok {
				continue
			}
		}
		hits = append(hits, Hit{
			ID:      point.ID,
			Score:   cosine(vector, point.Vector),
			Payload: point.Payload,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (f *Fake) Delete(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrUnavailable
	}
	for _, id := range ids {
		delete(f.points, id)
	}
	return nil
}

func (f *Fake) DeleteCollection(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrUnavailable
	}
	f.points = map[string]Point{}
	f.created = false
	return nil
}

func (f *Fake) Count(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return 0, ErrUnavailable
	}
	return int64(len(f.points)), nil
}

func (f *Fake) Health(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrUnavailable
	}
	return nil
}

func (f *Fake) Close() error { return nil }

// Len reports how many points are stored.
func (f *Fake) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.points)
}

// Created reports whether EnsureCollection has run since the last drop.
func (f *Fake) Created() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func cosine(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
