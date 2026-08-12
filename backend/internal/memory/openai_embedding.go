package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIEmbedding struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
}

func (p OpenAIEmbedding) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(p.BaseURL) == "" || strings.TrimSpace(p.Model) == "" {
		return nil, fmt.Errorf("embedding base URL/model required")
	}
	payload, _ := json.Marshal(map[string]any{"model": p.Model, "input": text})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/embeddings", bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("embedding endpoint %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return nil, e
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding response empty")
	}
	return out.Data[0].Embedding, nil
}
