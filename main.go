package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	stationCode    = "KBWI"
	cityName       = "Baltimore"
	cityState      = "MD"
	userAgent      = "BaltimoreWeatherGoScript/1.0"
	timeout        = 10 * time.Second
	apiEndpoint    = "https://api.weather.gov/stations/" + stationCode + "/observations/latest"
	locationName   = cityName + ", " + cityState + " Weather (" + stationCode + " Airport)"
	timezoneName   = "ET"
	timezoneOffset = -4 * 3600
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

func celsiusToFahrenheit(c float64) float64 {
	return c*9/5 + 32
}

func sendWeather(chatID int64, bot *tgbotapi.BotAPI) *tgbotapi.MessageConfig {
	et := time.FixedZone(timezoneName, timezoneOffset)
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		msg := tgbotapi.NewMessage(chatID, "Error creating weather request")
		return &msg
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error fetching data: %v", err)
		msg := tgbotapi.NewMessage(chatID, "Error fetching weather data")
		return &msg
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Received non-200 status code: %d", resp.StatusCode)
		msg := tgbotapi.NewMessage(chatID, "Error: Received non-OK status from weather API")
		return &msg
	}

	var data WeatherData
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&data); err != nil {
		log.Printf("Error decoding JSON: %v", err)
		msg := tgbotapi.NewMessage(chatID, "Error: Could not parse weather data")
		return &msg
	}

	etTime := data.Properties.Timestamp.In(et)
	temperature := celsiusToFahrenheit(data.Properties.Temperature.Value)
	dewpoint := celsiusToFahrenheit(data.Properties.Dewpoint.Value)
	humidity := data.Properties.Humidity.Value

	weatherMessage := fmt.Sprintf("=== "+locationName+" ===\n"+
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

	msg := tgbotapi.NewMessage(chatID, weatherMessage)
	return &msg
}

func main() {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is not set")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		text := update.Message.Text
		if len(text) > 7 && text == "/weather" {
			msg := sendWeather(update.Message.Chat.ID, bot)
			bot.Send(msg)
		} else {
			switch text {
			case "/hi", "hi", "Hi", "Hola", "Bonjour", "Ciao":
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Hi, I'm Goober Bot!")
				bot.Send(msg)
			}
		}
	}
}
