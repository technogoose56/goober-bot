package commands

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
)

// Deps bundles the dependencies that command handlers need.
type Deps struct {
	Bot       *tgbotapi.BotAPI
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
	case text == "/weather-schedules":
		handleWeatherSchedules(chatID, deps)
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

func handleWeatherSchedules(chatID int64, deps Deps) {
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

func reply(chatID int64, bot *tgbotapi.BotAPI, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message to chat %d: %v", chatID, err)
	}
}
