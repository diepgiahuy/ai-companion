package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
	"companion-server/internal/weather"
)

type testWeatherProvider struct {
	name     string
	forecast weather.Forecast
	err      error
}

func (p testWeatherProvider) Name() string {
	if p.name == "" {
		return "open-meteo"
	}
	return p.name
}

func (p testWeatherProvider) GetWeather(ctx context.Context, location string) (weather.Forecast, error) {
	if p.err != nil {
		return weather.Forecast{}, p.err
	}
	f := p.forecast
	f.Location = location
	f.Source = p.Name()
	return f, nil
}

func TestWeatherCurrentToolExecution(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	provider := testWeatherProvider{
		name: "open-meteo",
		forecast: weather.Forecast{
			TemperatureC: 32.0,
			Condition:    "Partly cloudy",
			WeatherCode:  2,
			Humidity:     70,
			WindSpeedKPH: 14.5,
			AsOf:         now,
		},
	}

	weatherService := weather.New(10*time.Minute, provider)
	weatherService.Now = func() time.Time { return now }

	registry := capability.NewToolRegistry()
	if err := RegisterPlatform(registry, PlatformDependencies{
		Weather: weatherService,
		Now:     func() time.Time { return now },
	}); err != nil {
		t.Fatalf("register platform tools: %v", err)
	}

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})
	def, ok := registry.Definition("weather.current")
	if !ok {
		t.Fatal("weather.current tool not found in registered tools")
	}
	if def.Pack != "weather" {
		t.Errorf("expected pack 'weather', got %s", def.Pack)
	}
	if def.Risk != "external" {
		t.Errorf("expected risk 'external', got %s", def.Risk)
	}

	req := capability.ToolRequest{
		Key:       "weather-req-1",
		Arguments: `{"location":"Hà Nội"}`,
	}
	res := registry.Execute(ctx, "weather.current", req)

	var payload struct {
		OK         bool             `json:"ok"`
		Error      string           `json:"error"`
		Forecast   weather.Forecast `json:"forecast"`
		Provenance string           `json:"provenance"`
		AsOf       time.Time        `json:"as_of"`
	}
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("unmarshal result (%s): %v", res.Content, err)
	}
	if !payload.OK {
		t.Fatalf("tool execution failed: %s", payload.Error)
	}
	if payload.Forecast.Location != "Hà Nội" || payload.Forecast.TemperatureC != 32.0 {
		t.Fatalf("unexpected forecast data: %+v", payload.Forecast)
	}
	if payload.Provenance != "open-meteo" {
		t.Errorf("expected provenance 'open-meteo', got %s", payload.Provenance)
	}

	// Verify presentation
	if res.Presentation == nil {
		t.Fatal("expected presentation, got nil")
	}
	if res.Presentation.Kind != "weather_card" {
		t.Errorf("expected kind 'weather_card', got %s", res.Presentation.Kind)
	}
	if res.Presentation.Title != "Hà Nội" {
		t.Errorf("expected title 'Hà Nội', got %s", res.Presentation.Title)
	}
	if !strings.Contains(res.Presentation.Primary, "32.0°C") {
		t.Errorf("expected primary presentation to include '32.0°C', got %s", res.Presentation.Primary)
	}
}

func TestWeatherCurrentToolProviderFailure(t *testing.T) {
	provider := testWeatherProvider{
		name: "open-meteo",
		err:  errors.New("weather service unavailable"),
	}

	weatherService := weather.New(10*time.Minute, provider)
	registry := capability.NewToolRegistry()
	if err := RegisterPlatform(registry, PlatformDependencies{Weather: weatherService}); err != nil {
		t.Fatal(err)
	}

	ctx := pipeline.WithTurnContext(context.Background(), pipeline.TurnContext{UserID: "user-a", TenantID: "tenant-a", DeviceID: "device-a"})
	req := capability.ToolRequest{
		Key:       "weather-fail-req",
		Arguments: `{"location":"Atlantis"}`,
	}
	res := registry.Execute(ctx, "weather.current", req)

	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("unmarshal failure result: %v", err)
	}
	if payload.OK {
		t.Fatal("expected failure on provider error, got OK=true")
	}
	if payload.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}
