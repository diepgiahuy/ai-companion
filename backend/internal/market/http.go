package market

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}
type TwelveData struct {
	BaseURL, APIKey string
	Client          HTTPClient
	Now             func() time.Time
}

func (p TwelveData) Name() string { return "twelvedata" }
func (p TwelveData) Quote(ctx context.Context, symbol, currency string) (Quote, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://api.twelvedata.com"
	}
	u, e := url.Parse(strings.TrimRight(base, "/") + "/price")
	if e != nil {
		return Quote{}, e
	}
	q := u.Query()
	q.Set("symbol", symbol)
	if p.APIKey != "" {
		q.Set("apikey", p.APIKey)
	}
	u.RawQuery = q.Encode()
	var out struct {
		Price   string `json:"price"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if e = getJSON(ctx, p.Client, u.String(), &out); e != nil {
		return Quote{}, e
	}
	if out.Price == "" {
		return Quote{}, fmt.Errorf("twelvedata: %s", out.Message)
	}
	v, e := strconv.ParseFloat(out.Price, 64)
	if e != nil {
		return Quote{}, e
	}
	if currency == "" {
		currency = "USD"
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	return Quote{Symbol: symbol, AssetClass: "market", Price: v, PriceType: "last", Currency: currency, Source: p.Name(), AsOf: now}, nil
}

type CoinGecko struct {
	BaseURL, APIKey string
	Client          HTTPClient
	Now             func() time.Time
}

func (p CoinGecko) Name() string { return "coingecko" }
func (p CoinGecko) Quote(ctx context.Context, symbol, currency string) (Quote, error) {
	if currency == "" {
		currency = "usd"
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.coingecko.com/api/v3"
	}
	u, _ := url.Parse(strings.TrimRight(base, "/") + "/simple/price")
	q := u.Query()
	q.Set("ids", symbol)
	q.Set("vs_currencies", strings.ToLower(currency))
	q.Set("include_last_updated_at", "true")
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return Quote{}, e
	}
	if p.APIKey != "" {
		req.Header.Set("x-cg-pro-api-key", p.APIKey)
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, e := c.Do(req)
	if e != nil {
		return Quote{}, e
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return Quote{}, fmt.Errorf("coingecko %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var root map[string]map[string]any
	if e = json.Unmarshal(b, &root); e != nil {
		return Quote{}, e
	}
	row := root[symbol]
	raw, ok := row[strings.ToLower(currency)]
	if !ok {
		return Quote{}, fmt.Errorf("coingecko quote missing")
	}
	v, e := asFloat(raw)
	if e != nil {
		return Quote{}, e
	}
	now := time.Now()
	if ts, ok := row["last_updated_at"]; ok {
		if sec, e := asFloat(ts); e == nil {
			now = time.Unix(int64(sec), 0)
		}
	}
	return Quote{Symbol: symbol, AssetClass: "crypto", Price: v, PriceType: "last", Currency: strings.ToUpper(currency), Source: p.Name(), AsOf: now}, nil
}

type AlphaVantageGold struct {
	BaseURL, APIKey string
	Client          HTTPClient
	Now             func() time.Time
}

func (p AlphaVantageGold) Name() string { return "alphavantage_gold" }
func (p AlphaVantageGold) Quote(ctx context.Context, symbol, currency string) (Quote, error) {
	if symbol == "" {
		symbol = "GOLD"
	}
	base := p.BaseURL
	if base == "" {
		base = "https://www.alphavantage.co/query"
	}
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("function", "GOLD_SILVER_SPOT")
	q.Set("symbol", symbol)
	q.Set("apikey", p.APIKey)
	u.RawQuery = q.Encode()
	var root any
	if e := getJSON(ctx, p.Client, u.String(), &root); e != nil {
		return Quote{}, e
	}
	v, ok := findNumeric(root, []string{"price", "spot_price", "price_usd", "value"})
	if !ok {
		return Quote{}, fmt.Errorf("alpha vantage gold price missing")
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	if currency == "" {
		currency = "USD"
	}
	return Quote{Symbol: "XAU/USD", AssetClass: "commodity", Price: v, PriceType: "last", Currency: currency, Unit: "troy_ounce", Source: p.Name(), AsOf: now}, nil
}
func getJSON(ctx context.Context, c HTTPClient, u string, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return e
	}
	if c == nil {
		c = http.DefaultClient
	}
	resp, e := c.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("market API %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, out)
}
func asFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case json.Number:
		return x.Float64()
	case string:
		return strconv.ParseFloat(strings.ReplaceAll(x, ",", ""), 64)
	}
	return 0, fmt.Errorf("not numeric")
}
func findNumeric(v any, keys []string) (float64, bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, z := range x {
			lk := strings.ToLower(k)
			for _, want := range keys {
				if strings.Contains(lk, want) {
					if n, e := asFloat(z); e == nil {
						return n, true
					}
				}
			}
			if n, ok := findNumeric(z, keys); ok {
				return n, true
			}
		}
	case []any:
		for _, z := range x {
			if n, ok := findNumeric(z, keys); ok {
				return n, true
			}
		}
	}
	return 0, false
}

type PNJGold struct {
	BaseURL string
	Client  HTTPClient
}

func (p PNJGold) Name() string { return "pnj_gold" }
func (p PNJGold) Quote(ctx context.Context, symbol, currency string) (Quote, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://www.pnj.com.vn/giavang/index.html?zone=00"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return Quote{}, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		return Quote{}, fmt.Errorf("pnj gold %s", resp.Status)
	}
	raw := string(b)
	upper := strings.ToUpper(strings.TrimSpace(symbol))
	target := "Nhẫn Trơn PNJ 999.9"
	normalized := "PNJ_RING"
	switch upper {
	case "SJC", "SJC-PNJ", "SJC_9999":
		target = "Vàng miếng SJC 999.9"
		normalized = "SJC"
	case "PNJ", "PNJ_RING", "RING", "NHAN", "NHẪN":
	}
	rowRE := regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	tagRE := regexp.MustCompile(`(?is)<[^>]+>`)
	numberRE := regexp.MustCompile(`\d{1,3}(?:[\.,]\d{3})+|\d+`)
	var buy, sell float64
	found := false
	for _, m := range rowRE.FindAllStringSubmatch(raw, -1) {
		text := strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(m[1], " "))), " ")
		idx := strings.Index(text, target)
		if idx < 0 {
			continue
		}
		nums := numberRE.FindAllString(text[idx+len(target):], -1)
		if len(nums) < 2 {
			continue
		}
		buy, err = parsePNJPrice(nums[0])
		if err != nil {
			continue
		}
		sell, err = parsePNJPrice(nums[1])
		if err != nil {
			continue
		}
		found = true
		break
	}
	if !found {
		return Quote{}, fmt.Errorf("pnj gold symbol %q not found", symbol)
	}
	asOf := time.Now()
	plain := strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(raw, " "))), " ")
	timeRE := regexp.MustCompile(`(?:Giá vàng ngày|Cập nhật ngày):?\s*(\d{2}/\d{2}/\d{4}\s+\d{2}:\d{2}:\d{2})`)
	if m := timeRE.FindStringSubmatch(plain); len(m) == 2 {
		if loc, e := time.LoadLocation("Asia/Ho_Chi_Minh"); e == nil {
			if t, e := time.ParseInLocation("02/01/2006 15:04:05", m[1], loc); e == nil {
				asOf = t
			}
		}
	}
	if currency == "" {
		currency = "VND"
	}
	return Quote{Symbol: normalized, AssetClass: "gold_vn", Price: sell, PriceType: "ask", Bid: &buy, Ask: &sell, Currency: strings.ToUpper(currency), Unit: "chi", Source: p.Name(), AsOf: asOf}, nil
}
func parsePNJPrice(v string) (float64, error) {
	cleaned := strings.NewReplacer(".", "", ",", "").Replace(v)
	n, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, err
	}
	return n * 1000, nil
}
