package weather

import (
	"testing"
	"time"
)

func TestCelsiusToFahrenheit(t *testing.T) {
	tests := []struct {
		name     string
		celsius  float64
		expected float64
	}{
		{"freezing point", 0, 32},
		{"boiling point", 100, 212},
		{"body temperature", 37, 98.6},
		{"negative value", -40, -40},
		{"room temperature", 20, 68},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CelsiusToFahrenheit(tt.celsius)
			if diff := result - tt.expected; diff > 0.1 || diff < -0.1 {
				t.Errorf("CelsiusToFahrenheit(%v) = %v, want %v", tt.celsius, result, tt.expected)
			}
		})
	}
}

func TestFormatWeatherDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	data := &WeatherData{}
	data.Properties.Timestamp = time.Date(2026, 3, 17, 18, 30, 0, 0, time.UTC)
	data.Properties.Temperature.Value = 20.0 // 68°F
	data.Properties.Condition = "Partly Cloudy"
	data.Properties.Dewpoint.Value = 10.0 // 50°F
	data.Properties.Humidity.Value = 55.0

	result := FormatWeather(data, cfg)

	if result == "" {
		t.Fatal("FormatWeather returned empty string")
	}

	expectedSubstrings := []string{
		"Baltimore, MD Weather (KBWI Airport)",
		"68.0 °F",
		"Partly Cloudy",
		"50.0 °F",
		"55.0 %",
	}

	for _, sub := range expectedSubstrings {
		if !containsSubstring(result, sub) {
			t.Errorf("FormatWeather result missing %q\nGot: %s", sub, result)
		}
	}
}

func TestFormatWeatherCustomConfig(t *testing.T) {
	cfg := Config{
		StationCode:    "KJFK",
		City:           "New York",
		State:          "NY",
		TimezoneName:   "ET",
		TimezoneOffset: -5 * 3600,
	}

	data := &WeatherData{}
	data.Properties.Timestamp = time.Date(2026, 3, 17, 18, 30, 0, 0, time.UTC)
	data.Properties.Temperature.Value = 15.0 // 59°F
	data.Properties.Condition = "Clear"
	data.Properties.Dewpoint.Value = 5.0
	data.Properties.Humidity.Value = 40.0

	result := FormatWeather(data, cfg)

	expectedSubstrings := []string{
		"New York, NY Weather (KJFK Airport)",
		"59.0 °F",
		"Clear",
	}

	for _, sub := range expectedSubstrings {
		if !containsSubstring(result, sub) {
			t.Errorf("FormatWeather result missing %q\nGot: %s", sub, result)
		}
	}

	// Should NOT contain Baltimore
	if containsSubstring(result, "Baltimore") {
		t.Errorf("FormatWeather should not contain default city when custom config used\nGot: %s", result)
	}
}

func TestFormatWeatherZeroValues(t *testing.T) {
	cfg := DefaultConfig()

	data := &WeatherData{}
	data.Properties.Timestamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	data.Properties.Temperature.Value = 0
	data.Properties.Condition = "Clear"
	data.Properties.Dewpoint.Value = 0
	data.Properties.Humidity.Value = 0

	result := FormatWeather(data, cfg)
	if result == "" {
		t.Fatal("FormatWeather returned empty string for zero values")
	}

	if !containsSubstring(result, "32.0 °F") {
		t.Errorf("Expected 32.0 °F (0°C converted), got: %s", result)
	}
}

func TestConfigLocationName(t *testing.T) {
	tests := []struct {
		cfg  Config
		want string
	}{
		{DefaultConfig(), "Baltimore, MD Weather (KBWI Airport)"},
		{Config{StationCode: "KJFK", City: "New York", State: "NY"}, "New York, NY Weather (KJFK Airport)"},
		{Config{StationCode: "KLAX", City: "Los Angeles", State: "CA"}, "Los Angeles, CA Weather (KLAX Airport)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.cfg.LocationName()
			if got != tt.want {
				t.Errorf("LocationName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigAPIEndpoint(t *testing.T) {
	cfg := Config{StationCode: "KJFK"}
	want := "https://api.weather.gov/stations/KJFK/observations/latest"
	got := cfg.APIEndpoint()
	if got != want {
		t.Errorf("APIEndpoint() = %q, want %q", got, want)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StationCode != "KBWI" {
		t.Errorf("StationCode = %q, want KBWI", cfg.StationCode)
	}
	if cfg.City != "Baltimore" {
		t.Errorf("City = %q, want Baltimore", cfg.City)
	}
	if cfg.TimezoneOffset != -14400 {
		t.Errorf("TimezoneOffset = %d, want -14400", cfg.TimezoneOffset)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
