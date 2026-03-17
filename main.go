package main

import (
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"goober-bot/internal/commands"
	"goober-bot/internal/database"
	"goober-bot/internal/scheduler"
	"goober-bot/internal/weather"
)

const dbPath = "./data/schedules.db"

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

	// Initialize database
	db, err := database.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close(db)

	// Create weather sender callback for the scheduler.
	// Looks up per-chat config from the database before fetching weather.
	sender := func(chatID int64) error {
		cfg := configForChat(db, chatID)
		return weather.SendWeatherToChat(chatID, bot, cfg)
	}

	// Initialize and start scheduler
	sched := scheduler.New(db, sender)
	if err := sched.LoadFromDB(); err != nil {
		log.Printf("Warning: failed to load existing schedules: %v", err)
	}
	sched.Start()
	defer sched.Stop()

	// Command dependencies
	deps := commands.Deps{
		Bot:       bot,
		Scheduler: sched,
		DB:        db,
	}

	log.Println("Bot started and listening for updates")

	// Set up graceful shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case sig := <-sigCh:
			log.Printf("Received signal %v, shutting down...", sig)
			bot.StopReceivingUpdates()
			// deferred sched.Stop() and database.Close(db) will run
			return

		case update, ok := <-updates:
			if !ok {
				log.Println("Update channel closed, shutting down...")
				return
			}

			if update.Message == nil {
				continue
			}

			text := update.Message.Text
			if text == "" {
				continue
			}

			chatID := update.Message.Chat.ID

			// Try scheduling and config commands first
			if commands.HandleCommand(text, chatID, deps) {
				continue
			}

			// Existing commands
			switch {
			case text == "/weather":
				cfg := configForChat(db, chatID)
				msg := weather.SendWeather(chatID, bot, cfg)
				bot.Send(msg)
			case text == "/hi" || text == "hi" || text == "Hi":
				msg := tgbotapi.NewMessage(chatID, "Hi, I'm Goober Bot!")
				bot.Send(msg)
			}
		}
	}
}

// configForChat looks up the user's weather config from the database,
// falling back to defaults if not found.
func configForChat(db *sql.DB, chatID int64) weather.Config {
	userCfg, err := database.GetUserConfig(db, chatID)
	if err != nil {
		log.Printf("Failed to get user config for chat %d, using defaults: %v", chatID, err)
		return weather.DefaultConfig()
	}
	return weather.Config{
		StationCode:    userCfg.StationCode,
		City:           userCfg.City,
		State:          userCfg.State,
		TimezoneName:   userCfg.TimezoneName,
		TimezoneOffset: userCfg.TimezoneOffset,
	}
}
