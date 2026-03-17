package weather

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	UserAgent = "GooberBotWeather/1.0"
	Timeout   = 10 * time.Second
)

// Config holds the weather station and display settings for a chat.
type Config struct {
	StationCode    string
	City           string
	State          string
	TimezoneName   string
	TimezoneOffset int // seconds east of UTC
}

// DefaultConfig returns the default weather configuration (Baltimore, MD).
func DefaultConfig() Config {
	return Config{
		StationCode:    "KBWI",
		City:           "Baltimore",
		State:          "MD",
		TimezoneName:   "ET",
		TimezoneOffset: -4 * 3600,
	}
}

// LocationName returns a display string like "Baltimore, MD Weather (KBWI Airport)".
func (c Config) LocationName() string {
	return c.City + ", " + c.State + " Weather (" + c.StationCode + " Airport)"
}

// APIEndpoint returns the NOAA observation URL for this config's station.
func (c Config) APIEndpoint() string {
	return "https://api.weather.gov/stations/" + c.StationCode + "/observations/latest"
}

type WeatherData struct {
	Properties struct {
		Timestamp   time.Time `json:"timestamp"`
		Temperature struct {
			Value   float64 `json:"value"`
			Unit    string  `json:"unit"`
			Quality string  `json:"qualityControl"`
		} `json:"temperature"`
		Condition string `json:"textDescription"`
		Dewpoint  struct {
			Value float64 `json:"value"`
		} `json:"dewpoint"`
		Humidity struct {
			Value float64 `json:"value"`
		} `json:"relativeHumidity"`
	} `json:"properties"`
}

func CelsiusToFahrenheit(c float64) float64 {
	return c*9/5 + 32
}

// FetchWeather retrieves the latest weather observation from the NOAA API
// for the given config's station.
func FetchWeather(cfg Config) (*WeatherData, error) {
	client := &http.Client{Timeout: Timeout}

	req, err := http.NewRequest("GET", cfg.APIEndpoint(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	var data WeatherData
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("error decoding JSON: %w", err)
	}

	return &data, nil
}

// FormatWeather builds a human-readable weather message string using the given config.
func FormatWeather(data *WeatherData, cfg Config) string {
	tz := time.FixedZone(cfg.TimezoneName, cfg.TimezoneOffset)
	localTime := data.Properties.Timestamp.In(tz)
	temperature := CelsiusToFahrenheit(data.Properties.Temperature.Value)
	dewpoint := CelsiusToFahrenheit(data.Properties.Dewpoint.Value)
	humidity := data.Properties.Humidity.Value

	return fmt.Sprintf("=== %s ===\n"+
		"Time: %s\n"+
		"Temp: %.1f °F\n"+
		"Condition: %s\n"+
		"Dew Point: %.1f °F\n"+
		"Humidity: %.1f %%",
		cfg.LocationName(),
		localTime.Format("Mon Jan 2 3:04 PM"),
		temperature,
		data.Properties.Condition,
		dewpoint,
		humidity)
}

// SendWeather fetches weather and returns a MessageConfig for the /weather command handler.
func SendWeather(chatID int64, bot *tgbotapi.BotAPI, cfg Config) *tgbotapi.MessageConfig {
	data, err := FetchWeather(cfg)
	if err != nil {
		log.Printf("Weather fetch error: %v", err)
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Error fetching weather: %v", err))
		return &msg
	}

	msg := tgbotapi.NewMessage(chatID, FormatWeather(data, cfg))
	return &msg
}

// SendWeatherToChat fetches weather and sends it directly. Used by the scheduler.
func SendWeatherToChat(chatID int64, bot *tgbotapi.BotAPI, cfg Config) error {
	data, err := FetchWeather(cfg)
	if err != nil {
		return fmt.Errorf("failed to fetch weather: %w", err)
	}

	msg := tgbotapi.NewMessage(chatID, FormatWeather(data, cfg))
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("failed to send weather message: %w", err)
	}

	return nil
}

// StationEndpoint returns the NOAA station metadata URL for the given code.
func StationEndpoint(stationCode string) string {
	return "https://api.weather.gov/stations/" + stationCode
}

// ValidateStation checks whether a NOAA station code exists by querying the
// NOAA API. It accepts an optional *http.Client (pass nil to use a default).
// Returns nil if the station is valid, or a descriptive error otherwise.
func ValidateStation(stationCode string, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}

	req, err := http.NewRequest("GET", StationEndpoint(stationCode), nil)
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/geo+json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach NOAA API: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("station %q not found; check the code at https://www.weather.gov", stationCode)
	default:
		return fmt.Errorf("NOAA API returned status %d while validating station %q", resp.StatusCode, stationCode)
	}
}
