package commands

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
)

// MessageSender is an interface for sending Telegram messages.
// *tgbotapi.BotAPI satisfies this interface.
type MessageSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Deps bundles the dependencies that command handlers need.
type Deps struct {
	Bot       MessageSender
	Scheduler *scheduler.Scheduler
	DB        *sql.DB
}

// HandleCommand checks if the message is a known command and handles it.
// Returns true if the message was handled.
func HandleCommand(text string, chatID int64, deps Deps) bool {
	switch {
	case text == "/recurring-weather" || strings.HasPrefix(text, "/recurring-weather "):
		handleRecurringWeather(text, chatID, deps)
		return true
	case text == "/cancel-weather":
		handleCancelWeather(chatID, deps)
		return true
	case text == "/weather-schedule":
		handleWeatherSchedule(chatID, deps)
		return true
	case text == "/weather-config" || strings.HasPrefix(text, "/weather-config "):
		handleWeatherConfig(text, chatID, deps)
		return true
	default:
		return false
	}
}

func handleRecurringWeather(text string, chatID int64, deps Deps) {
	// Parse cron expression from the message
	rest := strings.TrimPrefix(text, "/recurring-weather")
	cronExpr := strings.TrimSpace(rest)

	if cronExpr == "" {
		reply(chatID, deps.Bot,
			"Usage: /recurring-weather <cron expression>\n\n"+
				"Examples:\n"+
				"  /recurring-weather 0 9 * * 1-5   (weekdays at 9:00 AM UTC)\n"+
				"  /recurring-weather 0 8 * * *     (daily at 8:00 AM UTC)\n"+
				"  /recurring-weather 30 6,18 * * *  (daily at 6:30 AM and 6:30 PM UTC)")
		return
	}

	// Validate cron expression before doing anything
	if err := scheduler.ValidateCron(cronExpr); err != nil {
		reply(chatID, deps.Bot, fmt.Sprintf("Invalid cron expression: %v\n\nExample: /recurring-weather 0 9 * * 1-5", err))
		return
	}

	// Add schedule (persists to DB and registers cron job)
	if err := deps.Scheduler.AddSchedule(chatID, cronExpr); err != nil {
		log.Printf("Failed to add schedule for chat %d: %v", chatID, err)
		reply(chatID, deps.Bot, fmt.Sprintf("Failed to set schedule: %v", err))
		return
	}

	next, err := scheduler.NextRun(cronExpr)
	nextStr := "unknown"
	if err == nil {
		nextStr = next.Format("Mon Jan 2 3:04 PM UTC")
	}

	reply(chatID, deps.Bot, fmt.Sprintf(
		"Schedule set: %q\nNext run: %s",
		cronExpr, nextStr))
}

func handleCancelWeather(chatID int64, deps Deps) {
	if err := deps.Scheduler.RemoveSchedule(chatID); err != nil {
		reply(chatID, deps.Bot, "No active schedule found for this chat.")
		return
	}

	reply(chatID, deps.Bot, "Schedule cancelled. You will no longer receive automatic weather updates.")
}

func handleWeatherSchedule(chatID int64, deps Deps) {
	sched, err := database.GetSchedule(deps.DB, chatID)
	if err != nil {
		reply(chatID, deps.Bot,
			"No active schedule found.\n\nUse /recurring-weather <cron> to set one.\nExample: /recurring-weather 0 9 * * 1-5")
		return
	}

	lastRun := "never"
	if sched.LastRun != nil {
		lastRun = *sched.LastRun
	}

	next, err := scheduler.NextRun(sched.CronExpression)
	nextStr := "unknown"
	if err == nil {
		nextStr = next.Format("Mon Jan 2 3:04 PM UTC")
	}

	reply(chatID, deps.Bot, fmt.Sprintf(
		"Active schedule: %q\nCreated: %s\nLast run: %s\nNext run: %s",
		sched.CronExpression, sched.CreatedAt, lastRun, nextStr))
}

const weatherConfigUsage = `Usage: /weather-config <setting> <value>

Settings:
  station <code>                 NOAA station code (e.g., KJFK, KLAX, KORD)
  city <name>                    City name for display
  state <abbrev>                 State abbreviation (e.g., NY, CA, IL)
  timezone <name> <offset_hours> Timezone label and UTC offset in hours
                                 (e.g., ET -5, CT -6, PT -8)

Examples:
  /weather-config station KJFK
  /weather-config city New York
  /weather-config state NY
  /weather-config timezone ET -5

Send /weather-config with no arguments to view current settings.`

func handleWeatherConfig(text string, chatID int64, deps Deps) {
	rest := strings.TrimPrefix(text, "/weather-config")
	args := strings.TrimSpace(rest)

	// No arguments: show current config
	if args == "" {
		showConfig(chatID, deps)
		return
	}

	fields := strings.Fields(args)
	setting := strings.ToLower(fields[0])

	switch setting {
	case "station":
		if len(fields) < 2 {
			reply(chatID, deps.Bot, "Usage: /weather-config station <code>\nExample: /weather-config station KJFK")
			return
		}
		code := strings.ToUpper(fields[1])
		cfg, err := database.GetUserConfig(deps.DB, chatID)
		if err != nil {
			log.Printf("Failed to get user config for chat %d: %v", chatID, err)
			reply(chatID, deps.Bot, "Failed to read current config.")
			return
		}
		cfg.StationCode = code
		if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Failed to save config: %v", err))
			return
		}
		reply(chatID, deps.Bot, fmt.Sprintf("Station set to %q.\nWeather will now be fetched from https://api.weather.gov/stations/%s/observations/latest", code, code))

	case "city":
		if len(fields) < 2 {
			reply(chatID, deps.Bot, "Usage: /weather-config city <name>\nExample: /weather-config city New York")
			return
		}
		city := strings.Join(fields[1:], " ")
		cfg, err := database.GetUserConfig(deps.DB, chatID)
		if err != nil {
			log.Printf("Failed to get user config for chat %d: %v", chatID, err)
			reply(chatID, deps.Bot, "Failed to read current config.")
			return
		}
		cfg.City = city
		if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Failed to save config: %v", err))
			return
		}
		reply(chatID, deps.Bot, fmt.Sprintf("City set to %q.", city))

	case "state":
		if len(fields) < 2 {
			reply(chatID, deps.Bot, "Usage: /weather-config state <abbrev>\nExample: /weather-config state NY")
			return
		}
		state := strings.ToUpper(fields[1])
		cfg, err := database.GetUserConfig(deps.DB, chatID)
		if err != nil {
			log.Printf("Failed to get user config for chat %d: %v", chatID, err)
			reply(chatID, deps.Bot, "Failed to read current config.")
			return
		}
		cfg.State = state
		if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Failed to save config: %v", err))
			return
		}
		reply(chatID, deps.Bot, fmt.Sprintf("State set to %q.", state))

	case "timezone":
		if len(fields) < 3 {
			reply(chatID, deps.Bot, "Usage: /weather-config timezone <name> <offset_hours>\nExample: /weather-config timezone ET -5")
			return
		}
		tzName := fields[1]
		offsetHours, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Invalid offset %q: must be a number (e.g., -5, 5.5).", fields[2]))
			return
		}
		offsetSecs := int(offsetHours * 3600)

		cfg, err := database.GetUserConfig(deps.DB, chatID)
		if err != nil {
			log.Printf("Failed to get user config for chat %d: %v", chatID, err)
			reply(chatID, deps.Bot, "Failed to read current config.")
			return
		}
		cfg.TimezoneName = tzName
		cfg.TimezoneOffset = offsetSecs
		if err := database.UpsertUserConfig(deps.DB, cfg); err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Failed to save config: %v", err))
			return
		}
		reply(chatID, deps.Bot, fmt.Sprintf("Timezone set to %s (UTC%+.0f).", tzName, offsetHours))

	default:
		reply(chatID, deps.Bot, weatherConfigUsage)
	}
}

func showConfig(chatID int64, deps Deps) {
	cfg, err := database.GetUserConfig(deps.DB, chatID)
	if err != nil {
		log.Printf("Failed to get user config for chat %d: %v", chatID, err)
		reply(chatID, deps.Bot, "Failed to read config.")
		return
	}

	offsetHours := float64(cfg.TimezoneOffset) / 3600.0
	reply(chatID, deps.Bot, fmt.Sprintf(
		"Current weather config:\n"+
			"  Station:  %s\n"+
			"  City:     %s\n"+
			"  State:    %s\n"+
			"  Timezone: %s (UTC%+.0f)",
		cfg.StationCode, cfg.City, cfg.State, cfg.TimezoneName, offsetHours))
}

func reply(chatID int64, bot MessageSender, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message to chat %d: %v", chatID, err)
	}
}
