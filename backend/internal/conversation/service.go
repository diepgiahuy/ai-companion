package conversation

import (
	"context"
	"strings"
	"sync"
	"time"
)

type Scope struct {
	UserID   string
	ThreadID string
}

func (s Scope) Key() string {
	user := strings.TrimSpace(s.UserID)
	if user == "" {
		user = "default"
	}
	thread := strings.TrimSpace(s.ThreadID)
	if thread == "" {
		thread = "default"
	}
	return user + ":" + thread
}

type Message struct {
	Role      string
	Content   string
	CreatedAt time.Time
}

type Store interface {
	Append(ctx context.Context, turnKey string, scope Scope, role, content string) error
	Recent(ctx context.Context, scope Scope, limit int) ([]Message, error)
	Clear(ctx context.Context, scope Scope) error
}

type Cache interface {
	Get(scope Scope, limit int) ([]Message, bool)
	Put(scope Scope, messages []Message)
	Append(scope Scope, message Message)
	Invalidate(scope Scope)
}

type Service struct {
	store Store
	cache Cache
}

func New(store Store, cache Cache) *Service { return &Service{store: store, cache: cache} }

func (s *Service) Append(ctx context.Context, turnKey string, scope Scope, role, content string) error {
	if err := s.store.Append(ctx, turnKey, scope, role, content); err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Append(scope, Message{Role: role, Content: strings.TrimSpace(content), CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (s *Service) Clear(ctx context.Context, scope Scope) error {
	if err := s.store.Clear(ctx, scope); err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Invalidate(scope)
	}
	return nil
}

func (s *Service) Recent(ctx context.Context, scope Scope, limit int) ([]Message, error) {
	if s.cache != nil {
		if messages, ok := s.cache.Get(scope, limit); ok {
			return messages, nil
		}
	}
	messages, err := s.store.Recent(ctx, scope, limit)
	if err == nil && s.cache != nil {
		s.cache.Put(scope, messages)
	}
	return messages, err
}

type cacheEntry struct {
	messages []Message
	expires  time.Time
	touched  time.Time
}
type MemoryCache struct {
	mu          sync.Mutex
	ttl         time.Duration
	max         int
	maxMessages int
	entries     map[string]cacheEntry
}

func NewMemoryCache(ttl time.Duration, maxSessions int) *MemoryCache {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if maxSessions <= 0 {
		maxSessions = 100
	}
	return &MemoryCache{ttl: ttl, max: maxSessions, maxMessages: 32, entries: make(map[string]cacheEntry)}
}
func (c *MemoryCache) Get(scope Scope, limit int) ([]Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := scope.Key()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	e.touched = time.Now()
	e.expires = e.touched.Add(c.ttl)
	c.entries[key] = e
	m := e.messages
	if limit > 0 && len(m) > limit {
		m = m[len(m)-limit:]
	}
	return append([]Message(nil), m...), true
}
func (c *MemoryCache) Put(scope Scope, messages []Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfNeeded(scope.Key())
	messages = c.bound(messages)
	now := time.Now()
	c.entries[scope.Key()] = cacheEntry{messages: append([]Message(nil), messages...), expires: now.Add(c.ttl), touched: now}
}
func (c *MemoryCache) Append(scope Scope, message Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := scope.Key()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.entries, key)
		return
	}
	e.messages = c.bound(append(e.messages, message))
	e.touched = time.Now()
	e.expires = e.touched.Add(c.ttl)
	c.entries[key] = e
}
func (c *MemoryCache) Invalidate(scope Scope) {
	c.mu.Lock()
	delete(c.entries, scope.Key())
	c.mu.Unlock()
}
func (c *MemoryCache) bound(messages []Message) []Message {
	if len(messages) > c.maxMessages {
		messages = messages[len(messages)-c.maxMessages:]
	}
	return messages
}
func (c *MemoryCache) evictIfNeeded(incoming string) {
	if _, exists := c.entries[incoming]; exists || len(c.entries) < c.max {
		return
	}
	var oldest string
	var touched time.Time
	for k, v := range c.entries {
		if oldest == "" || v.touched.Before(touched) {
			oldest, touched = k, v.touched
		}
	}
	delete(c.entries, oldest)
}

type NoopCache struct{}

func (NoopCache) Get(Scope, int) ([]Message, bool) { return nil, false }
func (NoopCache) Put(Scope, []Message)             {}
func (NoopCache) Append(Scope, Message)            {}
func (NoopCache) Invalidate(Scope)                 {}
