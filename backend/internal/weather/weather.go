package weather

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Forecast struct {
	Location                 string    `json:"location"`
	Latitude                 float64   `json:"latitude"`
	Longitude                float64   `json:"longitude"`
	TemperatureC             float64   `json:"temperature_c"`
	Condition                string    `json:"condition"`
	WeatherCode              int       `json:"weather_code"`
	Humidity                 int       `json:"humidity_percent"`
	WindSpeedKPH             float64   `json:"wind_speed_kph"`
	PrecipitationProbability int       `json:"precipitation_probability,omitempty"`
	HighC                    *float64  `json:"high_c,omitempty"`
	LowC                     *float64  `json:"low_c,omitempty"`
	Summary                  string    `json:"summary"`
	Source                   string    `json:"source"`
	AsOf                     time.Time `json:"as_of"`
	Stale                    bool      `json:"stale"`
}

type Provider interface {
	Name() string
	GetWeather(ctx context.Context, location string) (Forecast, error)
}

type cachedForecast struct {
	f       Forecast
	expires time.Time
}

type Service struct {
	mu        sync.Mutex
	providers map[string]Provider
	primary   string
	cache     map[string]cachedForecast
	TTL       time.Duration
	Now       func() time.Time
}

func New(ttl time.Duration, ps ...Provider) *Service {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	s := &Service{
		providers: make(map[string]Provider),
		cache:     make(map[string]cachedForecast),
		TTL:       ttl,
		Now:       time.Now,
	}
	for _, p := range ps {
		if p != nil {
			name := strings.ToLower(strings.TrimSpace(p.Name()))
			s.providers[name] = p
			if s.primary == "" {
				s.primary = name
			}
		}
	}
	return s
}

func (s *Service) Providers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.providers))
	for k := range s.providers {
		out = append(out, k)
	}
	return out
}

func (s *Service) GetWeather(ctx context.Context, provider, location string) (Forecast, error) {
	loc := strings.TrimSpace(location)
	if loc == "" {
		loc = "Hà Nội"
	}
	provName := strings.ToLower(strings.TrimSpace(provider))
	if provName == "" {
		provName = s.primary
	}
	p := s.providers[provName]
	if p == nil {
		return Forecast{}, fmt.Errorf("weather provider %q unavailable", provName)
	}

	key := provName + "|" + strings.ToLower(loc)
	now := s.Now()

	s.mu.Lock()
	if c, ok := s.cache[key]; ok && now.Before(c.expires) {
		f := c.f
		s.mu.Unlock()
		return f, nil
	}
	s.mu.Unlock()

	f, err := p.GetWeather(ctx, loc)
	if err != nil {
		// If cache has expired item, return stale with flag
		s.mu.Lock()
		if c, ok := s.cache[key]; ok {
			stale := c.f
			stale.Stale = true
			s.mu.Unlock()
			return stale, nil
		}
		s.mu.Unlock()
		return Forecast{}, err
	}

	f.Source = p.Name()
	if f.AsOf.IsZero() {
		f.AsOf = now.UTC()
	}

	s.mu.Lock()
	s.cache[key] = cachedForecast{f: f, expires: now.Add(s.TTL)}
	s.mu.Unlock()

	return f, nil
}

func WMOCondition(code int) (condition string, summaryVN string) {
	switch code {
	case 0:
		return "Clear sky", "Trời quang, không mây"
	case 1:
		return "Mainly clear", "Trời trong, ít mây"
	case 2:
		return "Partly cloudy", "Có mây rải rác"
	case 3:
		return "Overcast", "Trời nhiều mây, âm u"
	case 45, 48:
		return "Foggy", "Có sương mù"
	case 51, 53, 55:
		return "Drizzle", "Mưa phùn nhẹ"
	case 56, 57:
		return "Freezing Drizzle", "Mưa phùn buốt giá"
	case 61:
		return "Slight rain", "Mưa nhỏ"
	case 63:
		return "Moderate rain", "Mưa vừa"
	case 65:
		return "Heavy rain", "Mưa to, mưa lớn"
	case 66, 67:
		return "Freezing rain", "Mưa băng giá"
	case 71, 73, 75:
		return "Snow fall", "Tuyết rơi"
	case 77:
		return "Snow grains", "Mưa tuyết hạt"
	case 80:
		return "Slight rain showers", "Mưa rào nhẹ"
	case 81:
		return "Moderate rain showers", "Mưa rào vừa"
	case 82:
		return "Violent rain showers", "Mưa rào xối xả"
	case 85, 86:
		return "Snow showers", "Mưa tuyết rào"
	case 95:
		return "Thunderstorm", "Có dông sét"
	case 96, 99:
		return "Thunderstorm with hail", "Dông kèm mưa đá"
	default:
		return "Clear", "Thời tiết ổn định"
	}
}
