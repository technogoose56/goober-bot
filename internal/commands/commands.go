package commands

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
	"goober-bot/internal/weather"
)

// MessageSender is an interface for sending Telegram messages.
// *tgbotapi.BotAPI satisfies this interface.
type MessageSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Deps bundles the dependencies that command handlers need.
type Deps struct {
	Bot        MessageSender
	Scheduler  *scheduler.Scheduler
	DB         *sql.DB
	HTTPClient *http.Client // optional; used for NOAA station validation (nil = default)
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
	case text == "/weather-schedule" || text == "/weather-schedules":
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
				"Cron times are in your configured timezone. Examples:\n"+
				"  /recurring-weather 0 9 * * 1-5   (weekdays at 9:00 AM)\n"+
				"  /recurring-weather 0 8 * * *     (daily at 8:00 AM)\n"+
				"  /recurring-weather 30 6,18 * * *  (daily at 6:30 AM and 6:30 PM)\n\n"+
				"Use /weather-config timezone to set your timezone.\n"+
				"Times will be displayed in your configured timezone.")
		return
	}

	// Validate cron expression before doing anything
	if err := scheduler.ValidateCron(cronExpr); err != nil {
		reply(chatID, deps.Bot, fmt.Sprintf("Invalid cron expression: %v\n\nExample: /recurring-weather 0 9 * * 1-5", err))
		return
	}

	// Determine the UTC expression to schedule based on user's timezone
	hasTimezone, _ := database.HasUserConfig(deps.DB, chatID)
	cfg, _ := database.GetUserConfig(deps.DB, chatID)

	var utcExpr string
	var tzNotice string
	var tzName string
	var tzOffset int

	if !hasTimezone {
		// No timezone configured — treat cron as UTC and notify the user
		utcExpr = cronExpr
		tzName = "UTC"
		tzOffset = 0
		tzNotice = "\n\nNote: No timezone set. Schedule times are in UTC.\nUse /weather-config timezone <name> <offset> to set your timezone."
	} else {
		converted, err := scheduler.ConvertCronToUTC(cronExpr, cfg.TimezoneOffset)
		if err != nil {
			// Expression too complex to convert — fall back to UTC
			utcExpr = cronExpr
			tzName = "UTC"
			tzOffset = 0
			tzNotice = "\n\nNote: Could not convert schedule to your timezone automatically. Times are in UTC."
		} else {
			utcExpr = converted
			tzName = cfg.TimezoneName
			tzOffset = cfg.TimezoneOffset
		}
	}

	// Add schedule (persists to DB and registers cron job)
	if err := deps.Scheduler.AddSchedule(chatID, cronExpr, utcExpr); err != nil {
		log.Printf("Failed to add schedule for chat %d: %v", chatID, err)
		reply(chatID, deps.Bot, fmt.Sprintf("Failed to set schedule: %v", err))
		return
	}

	nextStr, _ := scheduler.FormatNextRun(utcExpr, tzName, tzOffset)

	reply(chatID, deps.Bot, fmt.Sprintf(
		"Schedule set: %q\nNext run: %s%s",
		cronExpr, nextStr, tzNotice))
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
			"No active schedule found.\n\nUse /recurring-weather <cron> to set one.\nExample: /recurring-weather 0 9 * * 1-5"+
				"\nSee https://crontab.guru/ for help with cron syntax.")
		return
	}

	// Determine display timezone
	hasTimezone, _ := database.HasUserConfig(deps.DB, chatID)
	cfg, _ := database.GetUserConfig(deps.DB, chatID)

	var tzName string
	var tzOffset int
	if !hasTimezone {
		tzName = "UTC"
		tzOffset = 0
	} else {
		tzName = cfg.TimezoneName
		tzOffset = cfg.TimezoneOffset
	}

	lastRun := "never"
	if sched.LastRun != nil {
		lastRun = scheduler.FormatTimeInTZ(*sched.LastRun, tzName, tzOffset)
	}

	nextStr, _ := scheduler.FormatNextRun(sched.CronExpressionUTC, tzName, tzOffset)

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
		// Validate station against the NOAA API
		if err := weather.ValidateStation(code, deps.HTTPClient); err != nil {
			reply(chatID, deps.Bot, fmt.Sprintf("Invalid station code: %v", err))
			return
		}
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

		// Re-convert any existing schedule to the new timezone so next-run times stay accurate.
		scheduleNotice := ""
		if sched, err := database.GetSchedule(deps.DB, chatID); err == nil {
			newUTCExpr, convErr := scheduler.ConvertCronToUTC(sched.CronExpression, offsetSecs)
			if convErr == nil {
				if addErr := deps.Scheduler.AddSchedule(chatID, sched.CronExpression, newUTCExpr); addErr == nil {
					nextStr, _ := scheduler.FormatNextRun(newUTCExpr, tzName, offsetSecs)
					scheduleNotice = fmt.Sprintf("\nExisting schedule %q updated. Next run: %s", sched.CronExpression, nextStr)
				}
			}
		}

		reply(chatID, deps.Bot, fmt.Sprintf("Timezone set to %s (UTC%+.0f).%s", tzName, offsetHours, scheduleNotice))

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
