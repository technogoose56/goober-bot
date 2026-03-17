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

func TestFormatWeather(t *testing.T) {
	data := &WeatherData{}
	data.Properties.Timestamp = time.Date(2026, 3, 17, 18, 30, 0, 0, time.UTC)
	data.Properties.Temperature.Value = 20.0 // 68°F
	data.Properties.Condition = "Partly Cloudy"
	data.Properties.Dewpoint.Value = 10.0 // 50°F
	data.Properties.Humidity.Value = 55.0

	result := FormatWeather(data)

	// Check that key elements are present
	if result == "" {
		t.Fatal("FormatWeather returned empty string")
	}

	expectedSubstrings := []string{
		LocationName,
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

func TestFormatWeatherZeroValues(t *testing.T) {
	data := &WeatherData{}
	data.Properties.Timestamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	data.Properties.Temperature.Value = 0
	data.Properties.Condition = "Clear"
	data.Properties.Dewpoint.Value = 0
	data.Properties.Humidity.Value = 0

	result := FormatWeather(data)
	if result == "" {
		t.Fatal("FormatWeather returned empty string for zero values")
	}

	if !containsSubstring(result, "32.0 °F") {
		t.Errorf("Expected 32.0 °F (0°C converted), got: %s", result)
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
