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
	StationCode    = "KBWI"
	CityName       = "Baltimore"
	CityState      = "MD"
	UserAgent      = "BaltimoreWeatherGoScript/1.0"
	Timeout        = 10 * time.Second
	APIEndpoint    = "https://api.weather.gov/stations/" + StationCode + "/observations/latest"
	LocationName   = CityName + ", " + CityState + " Weather (" + StationCode + " Airport)"
	TimezoneName   = "ET"
	TimezoneOffset = -4 * 3600
)

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

// FetchWeather retrieves the latest weather observation from the NOAA API.
func FetchWeather() (*WeatherData, error) {
	client := &http.Client{Timeout: Timeout}

	req, err := http.NewRequest("GET", APIEndpoint, nil)
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

// FormatWeather builds a human-readable weather message string.
func FormatWeather(data *WeatherData) string {
	et := time.FixedZone(TimezoneName, TimezoneOffset)
	etTime := data.Properties.Timestamp.In(et)
	temperature := CelsiusToFahrenheit(data.Properties.Temperature.Value)
	dewpoint := CelsiusToFahrenheit(data.Properties.Dewpoint.Value)
	humidity := data.Properties.Humidity.Value

	return fmt.Sprintf("=== "+LocationName+" ===\n"+
		"Time: %s\n"+
		"Temp: %.1f °F\n"+
		"Condition: %s\n"+
		"Dew Point: %.1f °F\n"+
		"Humidity: %.1f %%",
		etTime.Format("Mon Jan 2 3:04 PM"),
		temperature,
		data.Properties.Condition,
		dewpoint,
		humidity)
}

// SendWeather fetches weather and returns a MessageConfig for the /weather command handler.
func SendWeather(chatID int64, bot *tgbotapi.BotAPI) *tgbotapi.MessageConfig {
	data, err := FetchWeather()
	if err != nil {
		log.Printf("Weather fetch error: %v", err)
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Error fetching weather: %v", err))
		return &msg
	}

	msg := tgbotapi.NewMessage(chatID, FormatWeather(data))
	return &msg
}

// SendWeatherToChat fetches weather and sends it directly. Used by the scheduler.
func SendWeatherToChat(chatID int64, bot *tgbotapi.BotAPI) error {
	data, err := FetchWeather()
	if err != nil {
		return fmt.Errorf("failed to fetch weather: %w", err)
	}

	msg := tgbotapi.NewMessage(chatID, FormatWeather(data))
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("failed to send weather message: %w", err)
	}

	return nil
}
