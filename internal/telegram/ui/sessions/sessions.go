package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// InputFlow tracks a per-chat conversational input session for a skill that
// declares one or more InputFields.
type InputFlow struct {
	SkillName  string
	StepIndex  int
	Collected  map[string]string
	StartedAt  time.Time
	BackTarget string // where the user lands on cancel/finish (e.g. "g:browser")
}

// PendingMutation captures a mutating skill call awaiting confirmation.
type PendingMutation struct {
	SkillName string
	Args      map[string]string
	UserID    int64
	StoredAt  time.Time
}

// Store keeps in-memory ephemeral UI state with a fixed TTL.
type Store struct {
	mu      sync.Mutex
	inputs  map[int64]*InputFlow
	args    map[string]map[string]string
	muts    map[string]PendingMutation
	ttl     time.Duration
	nowFunc func() time.Time
}

func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &Store{
		inputs:  make(map[int64]*InputFlow),
		args:    make(map[string]map[string]string),
		muts:    make(map[string]PendingMutation),
		ttl:     ttl,
		nowFunc: time.Now,
	}
}

// StartInput begins a new input flow for chatID, replacing any prior pending flow.
func (s *Store) StartInput(chatID int64, skillName, backTarget string) *InputFlow {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow := &InputFlow{
		SkillName:  skillName,
		Collected:  make(map[string]string),
		StartedAt:  s.now(),
		BackTarget: backTarget,
	}
	s.inputs[chatID] = flow
	return flow
}

// Pending returns the active input flow for chatID, if any.
func (s *Store) Pending(chatID int64) (*InputFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.inputs[chatID]
	if !ok {
		return nil, false
	}
	if s.now().Sub(flow.StartedAt) > s.ttl {
		delete(s.inputs, chatID)
		return nil, false
	}
	return flow, true
}

// AdvanceInput records an input value and bumps StepIndex.
func (s *Store) AdvanceInput(chatID int64, fieldName, value string) (*InputFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.inputs[chatID]
	if !ok {
		return nil, false
	}
	flow.Collected[fieldName] = value
	flow.StepIndex++
	flow.StartedAt = s.now()
	return flow, true
}

// ClearInput drops any pending input flow for chatID.
func (s *Store) ClearInput(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inputs, chatID)
}

// StoreArgs saves a map of arguments under an opaque token, suitable for
// embedding in a callback that would otherwise exceed 64 bytes.
func (s *Store) StoreArgs(args map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := newToken()
	copyArgs := make(map[string]string, len(args))
	for k, v := range args {
		copyArgs[k] = v
	}
	s.args[token] = copyArgs
	return token
}

// LoadArgs returns args previously stored under token. The args remain
// available until the TTL elapses.
func (s *Store) LoadArgs(token string) (map[string]string, bool) {
	if token == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	args, ok := s.args[token]
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out, true
}

// StoreMutation persists a pending mutating call, returning a one-shot token.
func (s *Store) StoreMutation(p PendingMutation) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := newToken()
	p.StoredAt = s.now()
	s.muts[token] = p
	return token
}

// ClaimMutation removes and returns a stored mutation if it belongs to userID
// and has not expired. A mismatched user does NOT consume the entry — that
// would let a third party silently void another user's pending action.
func (s *Store) ClaimMutation(token string, userID int64) (PendingMutation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.muts[token]
	if !ok {
		return PendingMutation{}, false
	}
	if p.UserID != userID {
		return PendingMutation{}, false
	}
	if s.now().Sub(p.StoredAt) > s.ttl {
		delete(s.muts, token)
		return PendingMutation{}, false
	}
	delete(s.muts, token)
	return p, true
}

// CancelMutation drops a pending mutation without executing it.
func (s *Store) CancelMutation(token string, userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.muts[token]
	if !ok {
		return false
	}
	if p.UserID != userID {
		return false
	}
	delete(s.muts, token)
	return true
}

// GC reaps expired entries. Safe to call periodically; not required for
// correctness because Pending/Claim already check TTL on read.
func (s *Store) GC() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for chatID, flow := range s.inputs {
		if now.Sub(flow.StartedAt) > s.ttl {
			delete(s.inputs, chatID)
		}
	}
	for token, p := range s.muts {
		if now.Sub(p.StoredAt) > s.ttl {
			delete(s.muts, token)
		}
	}
}

func (s *Store) now() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
}

func newToken() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
