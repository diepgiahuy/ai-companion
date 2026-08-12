package speech

import (
	"fmt"
	"strings"
)

type Voice struct {
	Key           string   `json:"key"`
	Provider      string   `json:"provider"`
	ProviderVoice string   `json:"provider_voice"`
	Locales       []string `json:"locales"`
	Streaming     bool     `json:"streaming"`
	SampleRate    int      `json:"sample_rate"`
}
type Catalog struct{ voices map[string]Voice }

func NewCatalog(xs []Voice) (*Catalog, error) {
	c := &Catalog{voices: map[string]Voice{}}
	for _, v := range xs {
		if v.Key == "" || v.Provider == "" {
			return nil, fmt.Errorf("voice key/provider required")
		}
		c.voices[v.Key] = v
	}
	return c, nil
}
func (c *Catalog) Resolve(key, locale string) (Voice, bool) {
	v, ok := c.voices[key]
	if !ok {
		return Voice{}, false
	}
	for _, l := range v.Locales {
		if strings.EqualFold(l, locale) || strings.EqualFold(strings.Split(l, "-")[0], strings.Split(locale, "-")[0]) {
			return v, true
		}
	}
	return Voice{}, false
}
func ValidLocale(tag string) bool {
	p := strings.Split(tag, "-")
	if len(p) < 1 || len(p[0]) < 2 || len(p[0]) > 3 {
		return false
	}
	for _, r := range p[0] {
		if r < 'A' || r > 'z' {
			return false
		}
	}
	return true
}
