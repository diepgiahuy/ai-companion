package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedding(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer h.Close()
	v, e := (OpenAIEmbedding{BaseURL: h.URL, Model: "embed", Client: h.Client()}).Embed(context.Background(), "xin chao")
	if e != nil || len(v) != 3 {
		t.Fatalf("%v %v", v, e)
	}
}
