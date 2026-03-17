Goober Bot - Telegram Bot

A Telegram bot built with Go that provides weather updates and scheduled weather reports. Users can configure their own weather station, city, state, and timezone.

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

## Commands

| Command | Description |
|---|---|
| `/hi` | Bot responds with a greeting |
| `/weather` | Shows current weather for your configured location |
| `/recurring-weather <cron>` | Set a recurring weather update schedule |
| `/cancel-weather` | Cancel your scheduled weather updates |
| `/weather-schedule` | View your active weather schedule |
| `/weather-config` | View your current weather settings |
| `/weather-config station <code>` | Set NOAA weather station (e.g., KJFK) |
| `/weather-config city <name>` | Set city name for display |
| `/weather-config state <abbrev>` | Set state abbreviation (e.g., NY) |
| `/weather-config timezone <name> <offset>` | Set timezone (e.g., ET -5) |

### Recurring Weather Examples

```
/recurring-weather 0 9 * * 1-5      Weekdays at 9:00 AM UTC
/recurring-weather 0 8 * * *        Daily at 8:00 AM UTC
/recurring-weather 30 6,18 * * *    Daily at 6:30 AM and 6:30 PM UTC
```

Cron expressions use the standard 5-field format: `minute hour day-of-month month day-of-week`. Minimum interval is 15 minutes.

### Weather Config Examples

```
/weather-config station KJFK
/weather-config city New York
/weather-config state NY
/weather-config timezone ET -5
```

Default location is Baltimore, MD (station KBWI). Weather data is fetched from the NOAA Weather API.

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
