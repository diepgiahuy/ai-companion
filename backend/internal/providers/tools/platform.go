package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/market"
	"companion-server/internal/memory"
)

type PlatformDependencies struct {
	Memory        *memory.Service
	Market        *market.Service
	MarketWatches market.WatchRepository
	Now           func() time.Time
}

func RegisterPlatform(r *capability.ToolRegistry, d PlatformDependencies) error {
	if r == nil {
		return fmt.Errorf("registry required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	obj := func(p map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": p, "required": req, "additionalProperties": false}
	}
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc, "minLength": 1, "maxLength": 500}
	}
	reg := func(t capability.Tool) error { return r.Register(t) }
	if d.Memory != nil {
		if err := reg(capability.FunctionTool{ToolName: "memory.remember", ToolDefinition: &capability.ToolDefinition{Name: "memory.remember", Description: "Ghi nhớ một fact hoặc preference bền vững của user; dùng key ổn định để fact mới supersede fact cũ", Pack: "memory", Risk: "write", FeatureKey: "memory.long_term", Parameters: obj(map[string]any{"key": str("stable snake_case key"), "kind": map[string]any{"type": "string", "enum": []string{"semantic", "episodic", "temporal"}}, "value": str("fact to remember"), "valid_from": map[string]any{"type": "string", "description": "RFC3339, optional"}}, "key", "kind", "value")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				Key       string      `json:"key"`
				Kind      memory.Kind `json:"kind"`
				Value     string      `json:"value"`
				ValidFrom string      `json:"valid_from"`
			}
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			key := strings.TrimSpace(a.Key)
			value := strings.TrimSpace(a.Value)
			at := d.Now()
			validFromHash := "auto"
			if strings.TrimSpace(a.ValidFrom) != "" {
				var e error
				at, e = time.Parse(time.RFC3339, strings.TrimSpace(a.ValidFrom))
				if e != nil {
					return capability.Failure(e)
				}
				validFromHash = at.UTC().Format(time.RFC3339Nano)
			}
			request, e := durableMutationRequest(ctx, "memory.remember", q.Key, map[string]any{"key": key, "kind": a.Kind, "value": value, "valid_from": validFromHash})
			if e != nil {
				return capability.Failure(e)
			}
			m, e := d.Memory.RememberMutation(ctx, request, currentUser(ctx), key, a.Kind, value, "user_explicit", 1, at)
			if e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"memory": m})
		}}); err != nil {
			return err
		}
		if err := reg(capability.FunctionTool{ToolName: "memory.recall", ToolDefinition: &capability.ToolDefinition{Name: "memory.recall", Description: "Tìm fact cá nhân liên quan bằng hybrid temporal + semantic retrieval", Pack: "memory", Risk: "read", FeatureKey: "memory.long_term", Parameters: obj(map[string]any{"query": str("what to recall"), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10}}, "query")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			xs, e := d.Memory.Recall(ctx, currentUser(ctx), a.Query, a.Limit)
			if e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"memories": xs, "provenance": "personal_memory", "instructional": false})
		}}); err != nil {
			return err
		}
		if err := reg(capability.FunctionTool{ToolName: "memory.forget", ToolDefinition: &capability.ToolDefinition{Name: "memory.forget", Description: "Quên/deactivate một memory theo key", Pack: "memory", Risk: "destructive", FeatureKey: "memory.long_term", Parameters: obj(map[string]any{"key": str("memory key")}, "key")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				Key string `json:"key"`
			}
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			key := strings.TrimSpace(a.Key)
			request, e := durableMutationRequest(ctx, "memory.forget", q.Key, map[string]any{"key": key})
			if e != nil {
				return capability.Failure(e)
			}
			if e := d.Memory.ForgetMutation(ctx, request, currentUser(ctx), key); e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"forgotten": key})
		}}); err != nil {
			return err
		}
	}
	if d.Market != nil {
		providers := d.Market.Providers()
		if len(providers) == 0 {
			return nil
		}
		enum := make([]any, 0, len(providers))
		for _, p := range providers {
			enum = append(enum, p)
		}
		if err := reg(capability.FunctionTool{ToolName: "market.quote", ToolDefinition: &capability.ToolDefinition{Name: "market.quote", Description: "Lấy giá market hiện tại từ live provider; không được tự đoán giá", Pack: "market", Risk: "external", FeatureKey: "market.live", Parameters: obj(map[string]any{"provider": map[string]any{"type": "string", "enum": enum}, "symbol": str("e.g. XAU/USD, AAPL, bitcoin"), "currency": map[string]any{"type": "string", "minLength": 3, "maxLength": 8}}, "provider", "symbol")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct{ Provider, Symbol, Currency string }
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			quote, e := d.Market.Quote(ctx, a.Provider, strings.TrimSpace(a.Symbol), strings.TrimSpace(a.Currency))
			if e != nil {
				return capability.Failure(e)
			}
			res := capability.Success(map[string]any{"quote": quote, "provenance": quote.Source, "as_of": quote.AsOf, "instructional": false})
			res.Presentation = &capability.Presentation{Kind: "market_price", Title: quote.Symbol, Primary: fmt.Sprintf("%.4f %s", quote.Price, quote.Currency), Secondary: "as of " + quote.AsOf.Format(time.RFC3339)}
			return res
		}}); err != nil {
			return err
		}
	}
	if d.MarketWatches != nil && d.Market != nil {
		if err := registerMarketWatchTools(r, d); err != nil {
			return err
		}
	}
	return nil
}

func registerMarketWatchTools(r *capability.ToolRegistry, d PlatformDependencies) error {
	providers := d.Market.Providers()
	enum := make([]any, 0, len(providers))
	for _, p := range providers {
		enum = append(enum, p)
	}
	obj := func(p map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": p, "required": req, "additionalProperties": false}
	}
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc, "minLength": 1, "maxLength": 200}
	}
	defs := []capability.Tool{
		capability.FunctionTool{ToolName: "market.watch.create", ToolDefinition: &capability.ToolDefinition{Name: "market.watch.create", Description: "Tạo cảnh báo khi giá vượt/ngắt một ngưỡng; host worker kiểm tra, không dùng LLM polling", Pack: "market", Risk: "write", FeatureKey: "market.live", Parameters: obj(map[string]any{"provider": map[string]any{"type": "string", "enum": enum}, "symbol": str("instrument"), "currency": map[string]any{"type": "string", "minLength": 3, "maxLength": 8}, "operator": map[string]any{"type": "string", "enum": []string{"<", "<=", ">", ">="}}, "threshold": map[string]any{"type": "number", "minimum": 0.0000001}}, "provider", "symbol", "operator", "threshold")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				Provider  string  `json:"provider"`
				Symbol    string  `json:"symbol"`
				Currency  string  `json:"currency"`
				Operator  string  `json:"operator"`
				Threshold float64 `json:"threshold"`
			}
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			provider := strings.TrimSpace(a.Provider)
			symbol := strings.TrimSpace(a.Symbol)
			currency := strings.ToUpper(strings.TrimSpace(a.Currency))
			if currency == "" {
				currency = "USD"
			}
			operator := strings.TrimSpace(a.Operator)
			deviceID := currentDevice(ctx)
			request, e := durableMutationRequest(ctx, "market.watch.create", q.Key, map[string]any{"provider": provider, "symbol": symbol, "currency": currency, "operator": operator, "threshold": a.Threshold, "device_id": deviceID})
			if e != nil {
				return capability.Failure(e)
			}
			durable, ok := d.MarketWatches.(durableMarketWatchRepository)
			if !ok {
				return capability.Failure(fmt.Errorf("durable market watch repository is unavailable"))
			}
			w, e := durable.CreateMarketWatchMutation(ctx, request, currentUser(ctx), deviceID, provider, symbol, currency, operator, a.Threshold)
			if e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"watch": w})
		}},
		capability.FunctionTool{ToolName: "market.watch.list", ToolDefinition: &capability.ToolDefinition{Name: "market.watch.list", Description: "Liệt kê cảnh báo market", Pack: "market", Risk: "read", FeatureKey: "market.live", Parameters: obj(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}})}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal([]byte(q.Arguments), &a)
			xs, e := d.MarketWatches.ListMarketWatches(ctx, currentUser(ctx), currentDevice(ctx), a.Limit)
			if e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"watches": xs})
		}},
		capability.FunctionTool{ToolName: "market.watch.delete", ToolDefinition: &capability.ToolDefinition{Name: "market.watch.delete", Description: "Xóa cảnh báo market theo id", Pack: "market", Risk: "destructive", FeatureKey: "market.live", Parameters: obj(map[string]any{"id": map[string]any{"type": "integer", "minimum": 1}}, "id")}, Handler: func(ctx context.Context, q capability.ToolRequest) capability.ToolResult {
			var a struct {
				ID int64 `json:"id"`
			}
			if e := json.Unmarshal([]byte(q.Arguments), &a); e != nil {
				return capability.Failure(e)
			}
			request, e := durableMutationRequest(ctx, "market.watch.delete", q.Key, map[string]any{"id": a.ID})
			if e != nil {
				return capability.Failure(e)
			}
			durable, ok := d.MarketWatches.(durableMarketWatchRepository)
			if !ok {
				return capability.Failure(fmt.Errorf("durable market watch repository is unavailable"))
			}
			if e := durable.DeleteMarketWatchMutation(ctx, request, currentUser(ctx), a.ID); e != nil {
				return capability.Failure(e)
			}
			return capability.Success(map[string]any{"deleted": "market_watch", "id": a.ID})
		}},
	}
	for _, t := range defs {
		if e := r.Register(t); e != nil {
			return e
		}
	}
	return nil
}
