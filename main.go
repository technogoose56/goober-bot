package main

import (
	"log"
	"os"

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

	// Create weather sender callback for the scheduler
	sender := func(chatID int64) error {
		return weather.SendWeatherToChat(chatID, bot)
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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		text := update.Message.Text
		if text == "" {
			continue
		}

		chatID := update.Message.Chat.ID

		// Try scheduling commands first
		if commands.HandleCommand(text, chatID, deps) {
			continue
		}

		// Existing commands
		switch {
		case text == "/weather":
			msg := weather.SendWeather(chatID, bot)
			bot.Send(msg)
		case text == "/hi" || text == "hi" || text == "Hi" ||
			text == "Hola" || text == "Bonjour" || text == "Ciao":
			msg := tgbotapi.NewMessage(chatID, "Hi, I'm Goober Bot!")
			bot.Send(msg)
		}
	}
}
