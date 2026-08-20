package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OpenMeteoProvider struct {
	client *http.Client
}

func NewOpenMeteo(client *http.Client) *OpenMeteoProvider {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &OpenMeteoProvider{client: client}
}

func (p *OpenMeteoProvider) Name() string {
	return "open-meteo"
}

type geocodingResponse struct {
	Results []struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Country   string  `json:"country"`
	} `json:"results"`
}

type openMeteoForecastResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Current   struct {
		Time             string  `json:"time"`
		Temperature2M    float64 `json:"temperature_2m"`
		RelativeHumidity int     `json:"relative_humidity_2m"`
		WeatherCode      int     `json:"weather_code"`
		WindSpeed10M     float64 `json:"wind_speed_10m"`
	} `json:"current"`
	Daily struct {
		Temperature2MMax             []float64 `json:"temperature_2m_max"`
		Temperature2MMin             []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
	} `json:"daily"`
}

var knownCityCoordinates = map[string][2]float64{
	"hà nội":            {21.0285, 105.8542},
	"hanoi":             {21.0285, 105.8542},
	"hồ chí minh":       {10.8231, 106.6297},
	"ho chi minh":       {10.8231, 106.6297},
	"ho chi minh city":  {10.8231, 106.6297},
	"saigon":            {10.8231, 106.6297},
	"sài gòn":           {10.8231, 106.6297},
	"đà nẵng":           {16.0544, 108.2022},
	"da nang":           {16.0544, 108.2022},
	"danang":            {16.0544, 108.2022},
	"hải phòng":         {20.8449, 106.6881},
	"hai phong":         {20.8449, 106.6881},
	"cần thơ":           {10.0452, 105.7469},
	"can tho":           {10.0452, 105.7469},
	"nha trang":         {12.2388, 109.1967},
	"đà lạt":            {11.9404, 108.4583},
	"da lat":            {11.9404, 108.4583},
	"huế":               {16.4637, 107.5909},
	"hue":               {16.4637, 107.5909},
	"vũng tàu":          {10.3460, 107.0843},
	"vung tau":          {10.3460, 107.0843},
	"tokyo":             {35.6762, 139.6503},
	"london":            {51.5074, -0.1278},
	"new york":          {40.7128, -74.0060},
	"singapore":         {1.3521, 103.8198},
	"bangkok":           {13.7563, 100.5018},
	"paris":             {48.8566, 2.3522},
	"sydney":            {-33.8688, 151.2093},
	"seoul":             {37.5665, 126.9780},
}

func (p *OpenMeteoProvider) resolveCoordinates(ctx context.Context, location string) (string, float64, float64, error) {
	norm := strings.ToLower(strings.TrimSpace(location))
	if coords, ok := knownCityCoordinates[norm]; ok {
		return location, coords[0], coords[1], nil
	}

	geoURL := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=en&format=json", url.QueryEscape(location))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geoURL, nil)
	if err != nil {
		return "", 0, 0, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("geocoding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, 0, fmt.Errorf("geocoding HTTP %d", resp.StatusCode)
	}

	var geo geocodingResponse
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return "", 0, 0, fmt.Errorf("decode geocoding response: %w", err)
	}
	if len(geo.Results) == 0 {
		return "", 0, 0, fmt.Errorf("location %q not found", location)
	}

	resolvedName := geo.Results[0].Name
	if geo.Results[0].Country != "" {
		resolvedName = fmt.Sprintf("%s, %s", geo.Results[0].Name, geo.Results[0].Country)
	}
	return resolvedName, geo.Results[0].Latitude, geo.Results[0].Longitude, nil
}

func (p *OpenMeteoProvider) GetWeather(ctx context.Context, location string) (Forecast, error) {
	resolvedLocation, lat, lon, err := p.resolveCoordinates(ctx, location)
	if err != nil {
		return Forecast{}, err
	}

	forecastURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,weather_code,wind_speed_10m&daily=temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=auto",
		lat, lon,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, forecastURL, nil)
	if err != nil {
		return Forecast{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Forecast{}, fmt.Errorf("forecast request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Forecast{}, fmt.Errorf("forecast HTTP %d", resp.StatusCode)
	}

	var data openMeteoForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return Forecast{}, fmt.Errorf("decode forecast response: %w", err)
	}

	condition, summaryVN := WMOCondition(data.Current.WeatherCode)
	summary := fmt.Sprintf("%s. Nhiệt độ %.1f°C, độ ẩm %d%%, gió %.1f km/h.", summaryVN, data.Current.Temperature2M, data.Current.RelativeHumidity, data.Current.WindSpeed10M)

	out := Forecast{
		Location:     resolvedLocation,
		Latitude:     data.Latitude,
		Longitude:    data.Longitude,
		TemperatureC: data.Current.Temperature2M,
		Condition:    condition,
		WeatherCode:  data.Current.WeatherCode,
		Humidity:     data.Current.RelativeHumidity,
		WindSpeedKPH: data.Current.WindSpeed10M,
		Summary:      summary,
		Source:       p.Name(),
		AsOf:         time.Now().UTC(),
		Stale:        false,
	}

	if len(data.Daily.Temperature2MMax) > 0 {
		high := data.Daily.Temperature2MMax[0]
		out.HighC = &high
	}
	if len(data.Daily.Temperature2MMin) > 0 {
		low := data.Daily.Temperature2MMin[0]
		out.LowC = &low
	}
	if len(data.Daily.PrecipitationProbabilityMax) > 0 {
		out.PrecipitationProbability = data.Daily.PrecipitationProbabilityMax[0]
	}

	return out, nil
}
