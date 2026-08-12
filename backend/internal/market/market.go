package market

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Quote struct {
	Symbol     string    `json:"symbol"`
	AssetClass string    `json:"asset_class"`
	Price      float64   `json:"price"`
	PriceType  string    `json:"price_type,omitempty"` // last | ask | mid
	Bid        *float64  `json:"bid,omitempty"`
	Ask        *float64  `json:"ask,omitempty"`
	Currency   string    `json:"currency"`
	Unit       string    `json:"unit,omitempty"`
	Source     string    `json:"source"`
	AsOf       time.Time `json:"as_of"`
	Stale      bool      `json:"stale"`
}
type Provider interface {
	Name() string
	Quote(context.Context, string, string) (Quote, error)
}
type cached struct {
	q       Quote
	expires time.Time
}
type Service struct {
	mu        sync.Mutex
	providers map[string]Provider
	cache     map[string]cached
	TTL       time.Duration
	Now       func() time.Time
}

func New(ttl time.Duration, ps ...Provider) *Service {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	s := &Service{providers: map[string]Provider{}, cache: map[string]cached{}, TTL: ttl, Now: time.Now}
	for _, p := range ps {
		if p != nil {
			s.providers[p.Name()] = p
		}
	}
	return s
}
func (s *Service) Quote(ctx context.Context, provider, symbol, currency string) (Quote, error) {
	p := s.providers[provider]
	if p == nil {
		return Quote{}, fmt.Errorf("market provider %q unavailable", provider)
	}
	key := provider + "|" + symbol + "|" + currency
	now := s.Now()
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && now.Before(c.expires) {
		q := c.q
		s.mu.Unlock()
		return q, nil
	}
	s.mu.Unlock()
	q, e := p.Quote(ctx, symbol, currency)
	if e != nil {
		return Quote{}, e
	}
	if q.AsOf.IsZero() {
		q.AsOf = now
	}
	s.mu.Lock()
	s.cache[key] = cached{q: q, expires: now.Add(s.TTL)}
	s.mu.Unlock()
	return q, nil
}
func (s *Service) Providers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := make([]string, 0, len(s.providers))
	for k := range s.providers {
		o = append(o, k)
	}
	return o
}
