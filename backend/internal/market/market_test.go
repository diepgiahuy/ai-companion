package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTwelveDataQuoteCache(t *testing.T) {
	calls := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"price":"123.45"}`))
	}))
	defer h.Close()
	p := TwelveData{BaseURL: h.URL, Client: h.Client(), Now: func() time.Time { return time.Unix(1, 0) }}
	s := New(time.Minute, p)
	s.Now = func() time.Time { return time.Unix(1, 0) }
	for i := 0; i < 2; i++ {
		q, e := s.Quote(context.Background(), "twelvedata", "XAU/USD", "USD")
		if e != nil || q.Price != 123.45 {
			t.Fatalf("%+v %v", q, e)
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestPNJGoldParsesRetailBidAskAndTimestamp(t *testing.T) {
	page := `<html><body><div>Cập nhật ngày: 20/07/2026 15:41:48</div><table><tr><td>Vàng miếng SJC 999.9</td><td>14.250</td><td>14.600</td></tr><tr><td>Nhẫn Trơn PNJ 999.9</td><td>14.100</td><td>14.500</td></tr></table></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(page)) }))
	defer ts.Close()
	q, err := (PNJGold{BaseURL: ts.URL, Client: ts.Client()}).Quote(context.Background(), "SJC", "VND")
	if err != nil {
		t.Fatal(err)
	}
	if q.Bid == nil || q.Ask == nil || *q.Bid != 14250000 || *q.Ask != 14600000 || q.Price != 14600000 || q.PriceType != "ask" || q.Unit != "chi" {
		t.Fatalf("quote=%+v", q)
	}
	if q.AsOf.Year() != 2026 || q.AsOf.Month() != 7 || q.AsOf.Day() != 20 {
		t.Fatalf("as_of=%v", q.AsOf)
	}
}
