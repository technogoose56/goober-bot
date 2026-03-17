Goober Bot - Telegram Bot

A Telegram bot built with Go that provides weather updates and scheduled weather reports for Baltimore, MD.

## Setup

1. Get a bot token from [@BotFather](https://t.me/BotFather) on Telegram
2. Set your token as an environment variable:
   ```
   $env:TELEGRAM_BOT_TOKEN="your_bot_token_here"
   ```
   or
   ```bash
   export TELEGRAM_BOT_TOKEN="your_bot_token_here"
   ```
3. Run the bot:
   ```bash
   go run main.go
   ```

## Features

- Responds to "hi", "Hi", "/hi", and various greetings in different languages
- Shows current weather information for Baltimore, MD via `/weather`
- Fetches weather data from the NOAA Weather API
- Scheduled recurring weather updates with cron expressions
- Schedules persist across bot restarts (SQLite database)

## Commands

| Command | Description |
|---|---|
| `/hi` | Bot responds with a greeting |
| `/weather` | Shows current Baltimore weather conditions |
| `/recurring-weather <cron>` | Set a recurring weather update schedule |
| `/cancel-weather` | Cancel your scheduled weather updates |
| `/weather-schedules` | View your active weather schedule |

### Recurring Weather Examples

```
/recurring-weather 0 9 * * 1-5      Weekdays at 9:00 AM UTC
/recurring-weather 0 8 * * *        Daily at 8:00 AM UTC
/recurring-weather 30 6,18 * * *    Daily at 6:30 AM and 6:30 PM UTC
```

Cron expressions use the standard 5-field format: `minute hour day-of-month month day-of-week`.

## Project Structure

```
main.go                           Entry point and update loop
internal/
  weather/weather.go              Weather API client and formatting
  database/db.go                  SQLite persistence layer
  scheduler/scheduler.go          Cron job management
  commands/commands.go            Command parsing and handlers
```

## Running Tests

```bash
go test ./...
```
