# Goober Bot - Recurring Weather Updates Plan

## Overview
Add functionality to allow users to receive scheduled weather updates via the Telegram bot. Users will be able to set a cron-based schedule using the `/recurring-weather <cron>` command.

## Current State Analysis
- Single file Go project (main.go)
- Uses Telegram Bot API (github.com/go-telegram-bot-api/v5)
- Currently has greeting and weather command handlers
- No persistent storage or database
- Weather data hardcoded for Baltimore/KBMI station
- Runs in a simple for-loop accepting updates

## Implementation Plan

### 1. Dependencies
Add SQLite database driver to go.mod:
```go
go get modernc.org/sqlite
```

Optionally add cron library for parsing expressions:
```go
go get github.com/robfig/cron/v3
```

### 2. Database Schema
Create internal/database/schema.sql with table:
```
CREATE TABLE IF NOT EXISTS schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL UNIQUE,
    cron_expression TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_run TIMESTAMP
);

CREATE TABLE IF NOT EXISTS weather_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    message TEXT NOT NULL,
    run_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (chat_id) REFERENCES schedules(chat_id)
);
```

### 3. Database Manager
Create internal/database/db.go:
- Initialize SQLite connection
- Open/create database file in project directory
- Implement functions:
  - `AddSchedule(chatID, cronExpr)` - insert new schedule
  - `RemoveSchedule(chatID)` - delete schedule
  - `GetSchedule(chatID)` - retrieve schedule
  - `ListSchedules()` - list all schedules (for admin view)
  - `LogWeather(chatID, message)` - record weather output

### 4. Weather Scheduler
Create internal/scheduler/scheduler.go:
- Use robfig/cron for scheduling
- Load saved schedules from database
- For each schedule:
  - Parse cron expression
  - Schedule weather send at configured times
- Handle cron errors gracefully

### 5. Command Handlers
Create internal/command/commands.go:
- `/recurring-weather <cron>` - Set or update schedule
- `/cancel-weather` - Remove current schedule
- `/weather-schedules` - View active schedules (if needed)

Command logic:
- Validate cron expression format
- Check if schedule already exists for chat
- Update or insert in database
- Restart scheduler to pick up changes

### 6. Main Integration
Update main.go:
- Initialize database on startup
- Initialize scheduler with existing schedules
- Remove command handler from main loop
- Add scheduling module initialization
- Pass database connection between modules

### 7. Weather Data Function Extraction
Extract sendWeather() to reusable function in internal/weather/weather.go:
- Take chatID and bot as parameters
- Return formatted message string
- Keep API call logic separated

### 8. Error Handling
- Validate cron expressions
- Handle database connection errors
- Gracefully handle weather API failures
- Log errors without crashing

### 9. Testing
- Unit tests for database operations
- Cron expression validation tests
- Integration tests for command flow

## Files to Create
- internal/database/schema.sql
- internal/database/db.go
- internal/scheduler/scheduler.go
- internal/commands/commands.go
- internal/weather/weather.go
- internal/config/config.go (optional - centralize constants)

## Files to Modify
- main.go - integrate new modules
- go.mod - add dependencies

## User Experience
User workflow:
1. Send "/recurring-weather '0 9 * * 1-5'" to set daily morning weather
2. Bot confirms schedule and sends first update
3. Schedule persists across bot restarts
4. User sends "/cancel-weather" to stop receiving updates